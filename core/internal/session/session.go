// SPDX-License-Identifier: GPL-2.0-or-later
// Package session 实现一次完整的 SSTP 连接：TLS 引导、SSTP 建链、PPP（LCP、
// MSCHAPv2 / EAP-MSCHAPv2 / PAP 认证、IPCP），随后把 IP 包在隧道与 TUN 之间转发。
package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"qingyou/qyvpn/internal/auth"
	"qingyou/qyvpn/internal/model"
	"qingyou/qyvpn/internal/ppp"
	"qingyou/qyvpn/internal/sstp"
)

// 错误分类：上层据此决定「自动重连」还是「停止并提示用户」。
var (
	ErrAuth         = errors.New("账号认证失败")
	ErrCertificate  = errors.New("服务器证书校验失败")
	ErrRejected     = errors.New("服务器拒绝了连接请求")
	ErrTimeout      = errors.New("连接超时")
	ErrDisconnected = errors.New("连接已被服务器断开")
	ErrCanceled     = errors.New("用户取消了连接")
)

// Tunnel 是 TUN 设备抽象，macOS 上由 internal/tunnel 实现。
// 地址、路由与 DNS 在创建时完成配置，这里只负责收发与关闭。
type Tunnel interface {
	// Read 读取一个 IP 包。
	Read(buf []byte) (int, error)
	// Write 写入一个 IP 包。
	Write(p []byte) (int, error)
	// Close 关闭设备并撤销路由/DNS。
	Close() error
}

// Meter 在数据面直接统计隧道内的字节数（比读网卡计数更准，也不占系统资源）。
type Meter struct {
	rx atomic.Int64 // 从服务器收到的字节（下载）
	tx atomic.Int64 // 发往服务器的字节（上传）
}

// RX 返回累计下载字节。
func (m *Meter) RX() int64 { return m.rx.Load() }

// TX 返回累计上传字节。
func (m *Meter) TX() int64 { return m.tx.Load() }

// Factory 创建 TUN 设备（拿到 IPCP 分配到的地址后才调用）。
type Factory func(ip net.IP, dns []net.IP, mtu int) (Tunnel, error)

// Options 是一次连接的参数。
type Options struct {
	MTU     int
	MRU     int
	Factory Factory
	Log     func(format string, args ...any)
	// OnIPCP 在拿到隧道地址后回调，用于向界面报告进度。
	OnIPCP func(ip string, dns []string)
	// OnConnected 在隧道可用后回调。
	OnConnected func()
	// PinnedCA 是节点自带的 CA（PEM）。为空时使用系统根证书（当前全部节点均为
	// Let's Encrypt 公网证书）。
	PinnedCA string
	// Meter 用于统计隧道流量；为空时不做统计。
	Meter *Meter
	// Login 覆盖节点账号（用于「账号@HUB 被拒时再用裸账号重试」）。
	Login string
}

const (
	sstpEchoInterval = 20 * time.Second
	pppEchoInterval  = 20 * time.Second
	deadAfter        = 70 * time.Second
	cfgRetryInterval = 3 * time.Second
	cfgRetryCount    = 10
)

// Session 保存一次连接的运行状态。
type Session struct {
	node  *model.Node
	opts  Options
	login string

	conn *tls.Conn
	rw   *bufio.Reader
	wmu  sync.Mutex

	certDER []byte
	nonce   []byte
	hlak    []byte
	hashAlg byte

	// serverAuth 是服务器在 LCP 里要求的认证协议（0 表示未要求）。
	serverAuth uint16

	tun Tunnel

	ctlCh  chan *sstp.Control
	lcpCh  chan pppFrame
	chapCh chan pppFrame
	eapCh  chan pppFrame
	papCh  chan pppFrame
	pppCh  chan pppFrame
	ipcpCh chan pppFrame
	ipCh   chan []byte

	lastRecv atomic.Int64
	errOnce  sync.Once
	errCh    chan error

	localIP  net.IP
	dns      []net.IP
	ipcpDone bool
}

type pppFrame struct {
	id   byte
	code byte
	data []byte // 长度字段之后的数据
}

func (s *Session) logf(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(format, args...)
	}
}

// Run 建立连接并阻塞到连接结束。ctx 取消即断开。
func Run(ctx context.Context, node *model.Node, opts Options) error {
	if err := node.Validate(); err != nil {
		return err
	}
	login := opts.Login
	if login == "" {
		login = node.Login()
	}
	if opts.MTU <= 0 || opts.MTU > 1500 {
		opts.MTU = 1400
	}
	if opts.MRU <= 0 || opts.MRU > 1500 {
		opts.MRU = opts.MTU
	}
	s := &Session{
		node:   node,
		opts:   opts,
		login:  login,
		ctlCh:  make(chan *sstp.Control, 16),
		lcpCh:  make(chan pppFrame, 16),
		chapCh: make(chan pppFrame, 16),
		eapCh:  make(chan pppFrame, 16),
		papCh:  make(chan pppFrame, 16),
		pppCh:  make(chan pppFrame, 16),
		ipcpCh: make(chan pppFrame, 16),
		ipCh:   make(chan []byte, 64),
		errCh:  make(chan error, 4),
	}
	s.lastRecv.Store(time.Now().UnixNano())
	defer s.cleanup()

	if err := s.bootstrap(ctx); err != nil {
		return err
	}
	// 读取循环必须在协商之前启动：LCP/认证/IPCP 的应答都由它分发。
	go s.readerLoop()
	if err := s.negotiateLCP(ctx); err != nil {
		return err
	}
	if err := s.authenticate(ctx); err != nil {
		return err
	}
	if err := s.sendCallConnected(); err != nil {
		return err
	}
	if err := s.negotiateIPCP(ctx); err != nil {
		return err
	}
	if err := s.startTunnel(); err != nil {
		return err
	}

	s.logf("隧道已建立：本机 %s，DNS %s，MTU %d", s.localIP, joinIPs(s.dns), s.opts.MTU)
	if s.opts.OnConnected != nil {
		s.opts.OnConnected()
	}

	go s.echoLoop(ctx)
	go s.pumpToTunnel(ctx)

	select {
	case <-ctx.Done():
		s.sendQuietly(sstp.Disconnect())
		return ErrCanceled
	case err := <-s.errCh:
		return err
	}
}

// bootstrap 建立 TLS 并完成 SSTP_DUPLEX_POST 引导。
func (s *Session) bootstrap(ctx context.Context) error {
	host := net.JoinHostPort(s.node.Host, fmt.Sprint(s.node.Port))
	ctxDial, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	raw, err := dialer.DialContext(ctxDial, "tcp", host)
	if err != nil {
		return fmt.Errorf("无法连接节点 %s：%w", host, err)
	}
	if tc, ok := raw.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}

	cfg := &tls.Config{
		ServerName:         s.node.RemoteIdentity(),
		MinVersion:         tls.VersionTLS12,
		ClientSessionCache: nil,
	}
	if s.opts.PinnedCA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(s.opts.PinnedCA)) {
			raw.Close()
			return fmt.Errorf("节点 CA 证书无法解析")
		}
		cfg.RootCAs = pool
	}
	tlsConn := tls.Client(raw, cfg)
	_ = tlsConn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := tlsConn.HandshakeContext(ctxDial); err != nil {
		raw.Close()
		return fmt.Errorf("%w：%v", ErrCertificate, err)
	}
	_ = tlsConn.SetDeadline(time.Time{})
	s.conn = tlsConn
	s.rw = bufio.NewReaderSize(tlsConn, 32*1024)
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		s.certDER = state.PeerCertificates[0].Raw
	} else {
		return fmt.Errorf("%w：未取得服务器证书", ErrCertificate)
	}
	s.logf("TLS 已就绪：%s（%s）", s.node.RemoteIdentity(), tlsVersion(state.Version))

	guid := newGUID()
	request := "SSTP_DUPLEX_POST /sra_{BA195980-CD49-458b-9E23-C84EE0ADCD75}/ HTTP/1.1\r\n" +
		"Host: " + s.node.Host + "\r\n" +
		"Content-Length: 18446744073709551615\r\n" +
		"SSTPCORRELATIONID: {" + guid + "}\r\n\r\n"
	if err := s.writeRaw([]byte(request)); err != nil {
		return fmt.Errorf("SSTP 引导请求发送失败：%w", err)
	}
	line, err := s.rw.ReadString('\n')
	if err != nil {
		return fmt.Errorf("SSTP 引导响应失败：%w", err)
	}
	if !strings.HasPrefix(line, "HTTP/1.1 200") && !strings.HasPrefix(line, "HTTP/1.0 200") {
		return fmt.Errorf("%w：%s", ErrRejected, strings.TrimSpace(line))
	}
	// 读完剩余响应头。
	for {
		l, err := s.rw.ReadString('\n')
		if err != nil {
			return fmt.Errorf("SSTP 引导响应中断：%w", err)
		}
		if l == "\r\n" || l == "\n" {
			break
		}
	}
	s.logf("SSTP 通道已接受，开始建链")

	if err := s.writeRaw(sstp.CallConnectRequest()); err != nil {
		return err
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ErrCanceled
		case <-deadline.C:
			return fmt.Errorf("%w：服务器未响应建链请求", ErrTimeout)
		default:
		}
		frame, err := s.readFrame(5 * time.Second)
		if err != nil {
			if isTimeout(err) {
				continue
			}
			return err
		}
		if frame.typ != sstp.PacketTypeControl {
			continue
		}
		ctl, err := sstp.ParseControl(frame.payload)
		if err != nil {
			return err
		}
		switch ctl.Type {
		case sstp.MsgCallConnectAck:
			req, err := sstp.ParseCryptoBindingRequest(ctl)
			if err != nil {
				return err
			}
			alg, err := req.HashProtocol()
			if err != nil {
				return err
			}
			s.hashAlg = alg
			s.nonce = req.Nonce
			s.logf("建链被接受（证书哈希 %s）", hashName(alg))
			return nil
		case sstp.MsgCallConnectNak:
			reason := "未提供原因"
			if a := ctl.Attr(sstp.AttrStatusInfo); a != nil && len(a.Value) >= 8 {
				reason = fmt.Sprintf("状态 0x%08X", binary.BigEndian.Uint32(a.Value[4:8]))
			}
			return fmt.Errorf("%w：%s", ErrRejected, reason)
		case sstp.MsgCallAbort:
			return ErrDisconnected
		}
	}
}

// negotiateLCP 完成 LCP 配置，并确定后续使用的认证协议。
func (s *Session) negotiateLCP(ctx context.Context) error {
	s.logf("开始 PPP 链路协商")
	requestID := frameID()
	sent := 0
	weReady, peerReady := false, false

	sendRequest := func() error {
		sent++
		opts := []ppp.Option{ppp.MRUOption(s.opts.MRU)}
		return s.writePPP(ppp.ProtoLCP, ppp.CodeConfigureRequest, requestID, ppp.EncodeOptions(opts))
	}
	if err := sendRequest(); err != nil {
		return err
	}

	for sent <= cfgRetryCount {
		select {
		case <-ctx.Done():
			return ErrCanceled
		case f := <-s.lcpCh:
			switch f.code {
			case ppp.CodeConfigureRequest:
				peerReady = false
				opts, err := ppp.ParseOptions(f.data)
				if err != nil {
					return err
				}
				if reject := s.lcpReject(f.id, opts); reject != nil {
					if err := s.writeFrame(reject); err != nil {
						return err
					}
					continue
				}
				if nak := s.lcpNak(f.id, opts); nak != nil {
					if err := s.writeFrame(nak); err != nil {
						return err
					}
					continue
				}
				if err := s.writePPP(ppp.ProtoLCP, ppp.CodeConfigureAck, f.id, ppp.EncodeOptions(opts)); err != nil {
					return err
				}
				peerReady = true
			case ppp.CodeConfigureAck:
				if f.id == requestID {
					weReady = true
				}
			case ppp.CodeConfigureNak, ppp.CodeConfigureReject:
				if f.id != requestID {
					continue
				}
				opts, _ := ppp.ParseOptions(f.data)
				if mru := ppp.Find(opts, ppp.OptMRU); mru != nil {
					if v, ok := mru.Uint16(); ok && v >= 68 {
						if f.code == ppp.CodeConfigureNak {
							s.opts.MRU = v
						}
					}
				}
				requestID = frameID()
				if err := sendRequest(); err != nil {
					return err
				}
			}
			if weReady && peerReady {
				s.logf("PPP 链路协商完成（对端认证方式：%s）", s.authLabel())
				return nil
			}
		case <-time.After(cfgRetryInterval):
			if weReady && peerReady {
				return nil
			}
			sent++
			requestID = frameID()
			if err := sendRequest(); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("%w：PPP 链路协商无响应", ErrTimeout)
}

// lcpReject 拒绝自己不认识的选项。
func (s *Session) lcpReject(id byte, opts []ppp.Option) []byte {
	var unknown []ppp.Option
	for _, o := range opts {
		if o.Type != ppp.OptMRU && o.Type != ppp.OptAuth {
			unknown = append(unknown, o)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return ppp.EncodePPP(ppp.ProtoLCP, ppp.CodeConfigureReject, id, ppp.EncodeOptions(unknown))
}

// lcpNak 对 MRU 偏小或认证方式不合适给出反建议。
func (s *Session) lcpNak(id byte, opts []ppp.Option) []byte {
	var nak []ppp.Option
	if mru := ppp.Find(opts, ppp.OptMRU); mru != nil {
		if v, ok := mru.Uint16(); ok && v < s.opts.MTU {
			nak = append(nak, ppp.MRUOption(s.opts.MTU))
		}
	}
	authOpt := ppp.Find(opts, ppp.OptAuth)
	if authOpt != nil {
		s.serverAuth, _ = authOpt.Protocol()
		if s.serverAuth == ppp.ProtoCHAP {
			if len(authOpt.Value) < 3 || authOpt.Value[2] != ppp.ChapMSCHAPv2 {
				s.serverAuth = 0
			}
		}
		switch s.serverAuth {
		case ppp.ProtoCHAP, ppp.ProtoEAP, ppp.ProtoPAP:
		default:
			s.serverAuth = 0
			// 不接受时反建议 CHAP + MSCHAPv2。
			nak = append(nak, ppp.AuthOption(ppp.ProtoCHAP, ppp.ChapMSCHAPv2))
		}
	}
	if len(nak) == 0 {
		return nil
	}
	return ppp.EncodePPP(ppp.ProtoLCP, ppp.CodeConfigureNak, id, ppp.EncodeOptions(nak))
}

func (s *Session) authLabel() string {
	switch s.serverAuth {
	case ppp.ProtoCHAP:
		return "CHAP/MSCHAPv2"
	case ppp.ProtoEAP:
		return "EAP-MSCHAPv2"
	case ppp.ProtoPAP:
		return "PAP"
	}
	return "未请求认证"
}

// authenticate 按服务器要求完成 PPP 认证，并保留 HLAK 供 crypto binding 使用。
func (s *Session) authenticate(ctx context.Context) error {
	auth := auth.New(s.login, s.node.Password)
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()

	switch s.serverAuth {
	case ppp.ProtoCHAP:
		return s.authCHAP(ctx, auth, timeout)
	case ppp.ProtoEAP:
		return s.authEAP(ctx, auth, timeout)
	case ppp.ProtoPAP:
		return s.authPAP(ctx, timeout)
	}
	// 服务器没要求认证：按无认证继续（SoftEther 不会这样，但不要卡死）。
	s.logf("服务器未要求认证")
	return nil
}

func (s *Session) authCHAP(ctx context.Context, a *auth.MSCHAPv2, timeout *time.Timer) error {
	for {
		select {
		case <-ctx.Done():
			return ErrCanceled
		case <-timeout.C:
			return fmt.Errorf("%w：认证无响应", ErrTimeout)
		case f := <-s.chapCh:
			switch f.code {
			case ppp.CodeChallenge:
				if len(f.data) < 1 {
					return fmt.Errorf("%w：挑战包无效", ErrAuth)
				}
				size := int(f.data[0])
				if len(f.data) < 1+size {
					return fmt.Errorf("%w：挑战包长度无效", ErrAuth)
				}
				if err := a.SetAuthChallenge(f.data[1 : 1+size]); err != nil {
					return err
				}
				peer := make([]byte, 16)
				if _, err := rand.Read(peer); err != nil {
					return err
				}
				resp, err := a.Response(peer)
				if err != nil {
					return err
				}
				value := make([]byte, 0, 1+49+len(s.login))
				value = append(value, 49)
				value = append(value, peer...)
				value = append(value, resp...)
				value = append(value, []byte(s.login)...)
				if err := s.writePPP(ppp.ProtoCHAP, ppp.CodeResponse, f.id, value); err != nil {
					return err
				}
				s.logf("已提交认证应答")
			case ppp.CodeSuccess:
				if !a.VerifyAuthenticator(f.data) {
					return fmt.Errorf("%w：服务器认证响应校验失败", ErrAuth)
				}
				s.hlak = a.HLAK()
				s.logf("认证成功（MSCHAPv2）")
				return nil
			case ppp.CodeFailure:
				return fmt.Errorf("%w：%s", ErrAuth, chapMessage(f.data))
			}
		}
	}
}

func (s *Session) authEAP(ctx context.Context, a *auth.MSCHAPv2, timeout *time.Timer) error {
	var innerID byte
	acceptable := false
	for {
		select {
		case <-ctx.Done():
			return ErrCanceled
		case <-timeout.C:
			return fmt.Errorf("%w：认证无响应", ErrTimeout)
		case f := <-s.eapCh:
			switch f.code {
			case ppp.CodeChallenge: // EAP-Request
				if len(f.data) < 1 {
					return fmt.Errorf("%w：EAP 请求无效", ErrAuth)
				}
				typ := f.data[0]
				body := f.data[1:]
				switch typ {
				case ppp.EAPIdentity:
					if err := s.writePPP(ppp.ProtoEAP, ppp.CodeResponse, f.id, append([]byte{ppp.EAPIdentity}, []byte(s.login)...)); err != nil {
						return err
					}
				case ppp.EAPMSAuth:
					if len(body) < 4 {
						return fmt.Errorf("%w：EAP-MSCHAPv2 数据无效", ErrAuth)
					}
					op := body[0]
					innerID = body[1]
					length := int(binary.BigEndian.Uint16(body[2:4]))
					inner := body[4:]
					if length-4 < len(inner) {
						inner = inner[:length-4]
					}
					switch op {
					case ppp.CodeChallenge:
						acceptable = false
						if len(inner) < 1 {
							return fmt.Errorf("%w：内层挑战无效", ErrAuth)
						}
						size := int(inner[0])
						if len(inner) < 1+size {
							return fmt.Errorf("%w：内层挑战长度无效", ErrAuth)
						}
						if err := a.SetAuthChallenge(inner[1 : 1+size]); err != nil {
							return err
						}
						peer := make([]byte, 16)
						if _, err := rand.Read(peer); err != nil {
							return err
						}
						resp, err := a.Response(peer)
						if err != nil {
							return err
						}
						payload := make([]byte, 0, 4+16+24+len(s.login))
						payload = append(payload, ppp.CodeResponse, innerID, 0, 0)
						payload = append(payload, peer...)
						payload = append(payload, resp...)
						payload = append(payload, []byte(s.login)...)
						binary.BigEndian.PutUint16(payload[2:4], uint16(len(payload)))
						if err := s.writePPP(ppp.ProtoEAP, ppp.CodeResponse, f.id, append([]byte{ppp.EAPMSAuth}, payload...)); err != nil {
							return err
						}
						s.logf("已提交 EAP-MSCHAPv2 应答")
					case ppp.CodeSuccess:
						if !a.VerifyAuthenticator(inner) {
							return fmt.Errorf("%w：服务器认证响应校验失败", ErrAuth)
						}
						acceptable = true
						s.hlak = a.HLAK()
						if err := s.writePPP(ppp.ProtoEAP, ppp.CodeResponse, f.id, []byte{ppp.EAPMSAuth, ppp.CodeSuccess}); err != nil {
							return err
						}
					case ppp.CodeFailure:
						return fmt.Errorf("%w：%s", ErrAuth, chapMessage(inner))
					}
				default:
					// 只支持 MSCHAPv2，其它类型一律 NAK。
					if err := s.writePPP(ppp.ProtoEAP, ppp.CodeResponse, f.id, []byte{ppp.EAPNak, ppp.EAPMSAuth}); err != nil {
						return err
					}
				}
			case ppp.CodeSuccess:
				if !acceptable {
					continue
				}
				s.logf("认证成功（EAP-MSCHAPv2）")
				return nil
			case ppp.CodeFailure:
				return fmt.Errorf("%w：服务器拒绝了认证", ErrAuth)
			}
		}
	}
}

func (s *Session) authPAP(ctx context.Context, timeout *time.Timer) error {
	// PAP：客户端先发 Authenticate-Request(1)，服务器回 Ack(2)/Nak(3)。
	id := frameID()
	value := append([]byte{byte(len(s.login))}, []byte(s.login)...)
	value = append(value, byte(len(s.node.Password)))
	value = append(value, []byte(s.node.Password)...)
	if err := s.writePPP(ppp.ProtoPAP, pppPAPRequest, id, value); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ErrCanceled
		case <-timeout.C:
			return fmt.Errorf("%w：认证无响应", ErrTimeout)
		case f := <-s.papCh:
			if f.id != id {
				continue
			}
			switch f.code {
			case pppPAPAck:
				// PAP 没有 MSCHAPv2 密钥，SSTP crypto binding 按协议用全零 HLAK。
				s.logf("认证成功（PAP）")
				s.hlak = make([]byte, 32)
				return nil
			case pppPAPNak:
				return fmt.Errorf("%w：%s", ErrAuth, strings.TrimSpace(string(f.data)))
			}
		}
	}
}

// PAP 的代码编号与 CHAP 不同：1=请求，2=成功，3=失败。
const (
	pppPAPRequest byte = 1
	pppPAPAck     byte = 2
	pppPAPNak     byte = 3
)

// sendCallConnected 发送带 crypto binding 的建链完成包。
func (s *Session) sendCallConnected() error {
	certHash := sstp.CertHash(s.hashAlg, s.certDER)
	frame, err := sstp.CallConnected(s.nonce, certHash, s.hashAlg, s.hlak)
	if err != nil {
		return err
	}
	if err := s.writeRaw(frame); err != nil {
		return err
	}
	s.logf("已提交 SSTP 绑定（CMAC）")
	return nil
}

// negotiateIPCP 获取隧道地址与 DNS。
func (s *Session) negotiateIPCP(ctx context.Context) error {
	s.logf("开始 IPCP 协商")
	requestID := frameID()
	sent := 0
	weReady, peerReady := false, false
	var local [4]byte
	var dns [4]byte

	sendRequest := func() error {
		sent++
		var opts []ppp.Option
		opts = append(opts, ppp.AddressOption(ppp.OptIP, local))
		opts = append(opts, ppp.AddressOption(ppp.OptDNS, dns))
		return s.writePPP(ppp.ProtoIPCP, ppp.CodeConfigureRequest, requestID, ppp.EncodeOptions(opts))
	}
	if err := sendRequest(); err != nil {
		return err
	}

	for sent <= cfgRetryCount {
		select {
		case <-ctx.Done():
			return ErrCanceled
		case f := <-s.ipcpCh:
			switch f.code {
			case ppp.CodeConfigureRequest:
				peerReady = false
				opts, err := ppp.ParseOptions(f.data)
				if err != nil {
					return err
				}
				// 客户端没有 DNS 服务器可分配：拒绝对端要 DNS 的请求，其余 ACK。
				var reject []ppp.Option
				var accept []ppp.Option
				for _, o := range opts {
					switch o.Type {
					case ppp.OptIP:
						accept = append(accept, o)
					case ppp.OptDNS:
						reject = append(reject, o)
					default:
						reject = append(reject, o)
					}
				}
				if len(reject) > 0 {
					if err := s.writePPP(ppp.ProtoIPCP, ppp.CodeConfigureReject, f.id, ppp.EncodeOptions(reject)); err != nil {
						return err
					}
					continue
				}
				if err := s.writePPP(ppp.ProtoIPCP, ppp.CodeConfigureAck, f.id, ppp.EncodeOptions(accept)); err != nil {
					return err
				}
				peerReady = true
			case ppp.CodeConfigureAck:
				if f.id == requestID {
					weReady = true
				}
			case ppp.CodeConfigureNak:
				if f.id != requestID {
					continue
				}
				opts, _ := ppp.ParseOptions(f.data)
				if o := ppp.Find(opts, ppp.OptIP); o != nil {
					if a, ok := o.Addr(); ok {
						local = a
					}
				}
				if o := ppp.Find(opts, ppp.OptDNS); o != nil {
					if a, ok := o.Addr(); ok {
						dns = a
					}
				}
				requestID = frameID()
				if err := sendRequest(); err != nil {
					return err
				}
			case ppp.CodeConfigureReject:
				if f.id != requestID {
					continue
				}
				opts, _ := ppp.ParseOptions(f.data)
				if ppp.Find(opts, ppp.OptDNS) != nil {
					dns = [4]byte{}
				}
				requestID = frameID()
				if err := sendRequest(); err != nil {
					return err
				}
			}
			if weReady && peerReady {
				if local == [4]byte{} {
					return fmt.Errorf("%w：服务器没有分配地址", ErrRejected)
				}
				s.localIP = net.IPv4(local[0], local[1], local[2], local[3])
				if dns != [4]byte{} {
					s.dns = []net.IP{net.IPv4(dns[0], dns[1], dns[2], dns[3])}
				}
				s.ipcpDone = true
				if s.opts.OnIPCP != nil {
					s.opts.OnIPCP(s.localIP.String(), ipStrings(s.dns))
				}
				return nil
			}
		case <-time.After(cfgRetryInterval):
			if weReady && peerReady {
				continue
			}
			sent++
			requestID = frameID()
			if err := sendRequest(); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("%w：IPCP 协商无响应", ErrTimeout)
}

func (s *Session) startTunnel() error {
	if s.opts.Factory == nil {
		return nil
	}
	tun, err := s.opts.Factory(s.localIP, s.dns, s.opts.MTU)
	if err != nil {
		return err
	}
	s.tun = tun
	return nil
}

// readerLoop 是唯一的读取方，负责分发帧与自动应答心跳。
func (s *Session) readerLoop() {
	for {
		frame, err := s.readFrame(deadAfter)
		if err != nil {
			if isTimeout(err) && time.Since(time.Unix(0, s.lastRecv.Load())) < deadAfter {
				continue
			}
			s.fail(err)
			return
		}
		s.lastRecv.Store(time.Now().UnixNano())

		switch frame.typ {
		case sstp.PacketTypeControl:
			ctl, err := sstp.ParseControl(frame.payload)
			if err != nil {
				s.logf("忽略无法解析的控制包：%v", err)
				continue
			}
			switch ctl.Type {
			case sstp.MsgEchoRequest:
				s.sendQuietly(sstp.EchoResponse())
			case sstp.MsgEchoResponse:
			case sstp.MsgCallDisconnect:
				s.sendQuietly(sstp.DisconnectAck())
				s.fail(ErrDisconnected)
				return
			case sstp.MsgCallAbort:
				s.fail(fmt.Errorf("%w：服务器中止了连接", ErrDisconnected))
				return
			default:
				select {
				case s.ctlCh <- ctl:
				default:
				}
			}
		case sstp.PacketTypeData:
			proto, data, err := sstp.SplitData(frame.payload)
			if err != nil {
				s.logf("忽略异常的 SSTP 数据帧：%v", err)
				continue
			}
			switch proto {
			case ppp.ProtoIPv4, ppp.ProtoIPv6:
				if s.tun != nil {
					if s.opts.Meter != nil {
						s.opts.Meter.rx.Add(int64(len(data)))
					}
					if _, err := s.tun.Write(data); err != nil {
						s.fail(fmt.Errorf("写入隧道失败：%w", err))
						return
					}
				}
			default:
				code, id, payload, err := ppp.ParsePPP(data)
				if err != nil {
					continue
				}
				if proto == ppp.ProtoLCP {
					// LCP 心跳直接在这里应答，避免和协商流程抢通道。
					switch code {
					case ppp.CodeEchoRequest:
						magic := uint32(0)
						if len(payload) >= 4 {
							magic = binary.BigEndian.Uint32(payload)
						}
						s.sendQuietly(ppp.Echo(ppp.CodeEchoReply, id, magic, payload[minInt(4, len(payload)):]))
						continue
					case ppp.CodeEchoReply, ppp.CodeDiscardRequest:
						continue
					case ppp.CodeTerminateRequest:
						s.sendQuietly(ppp.EncodePPP(ppp.ProtoLCP, ppp.CodeTerminateAck, id, nil))
						s.fail(fmt.Errorf("%w：对端终止了链路", ErrDisconnected))
						return
					}
				}
				f := pppFrame{id: id, code: code, data: payload}
				switch proto {
				case ppp.ProtoLCP:
					push(s.lcpCh, f)
				case ppp.ProtoCHAP:
					push(s.chapCh, f)
				case ppp.ProtoEAP:
					push(s.eapCh, f)
				case ppp.ProtoPAP:
					push(s.papCh, f)
				case ppp.ProtoIPCP:
					push(s.ipcpCh, f)
				default:
					s.logf("收到未知 PPP 协议 0x%04X，已回 Protocol-Reject", proto)
					s.sendQuietly(ppp.ProtocolReject(frameID(), proto, data))
				}
			}
		}
	}
}

func (s *Session) echoLoop(ctx context.Context) {
	sstpTicker := time.NewTicker(sstpEchoInterval)
	pppTicker := time.NewTicker(pppEchoInterval)
	defer sstpTicker.Stop()
	defer pppTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sstpTicker.C:
			if time.Since(time.Unix(0, s.lastRecv.Load())) > deadAfter {
				s.fail(fmt.Errorf("%w：长时间没有收到服务器数据", ErrTimeout))
				return
			}
			s.sendQuietly(sstp.EchoRequest())
		case <-pppTicker.C:
			magic := uint32(0x51595A31)
			s.sendQuietly(ppp.Echo(ppp.CodeEchoRequest, frameID(), magic, []byte("qingyou")))
		}
	}
}

func (s *Session) pumpToTunnel(ctx context.Context) {
	if s.tun == nil {
		return
	}
	buf := make([]byte, s.opts.MTU+64)
	for {
		if ctx.Err() != nil {
			return
		}
		n, err := s.tun.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.fail(fmt.Errorf("读取隧道失败：%w", err))
			return
		}
		if n <= 0 {
			continue
		}
		proto := uint16(ppp.ProtoIPv4)
		if buf[0]>>4 == 6 {
			proto = ppp.ProtoIPv6
		}
		if s.opts.Meter != nil {
			s.opts.Meter.tx.Add(int64(n))
		}
		if err := s.writeRaw(sstp.EncodeData(buf[:n], proto)); err != nil {
			s.fail(err)
			return
		}
	}
}

// --- 帧收发 ---

type frame struct {
	typ     uint16
	payload []byte
}

func (s *Session) readFrame(timeout time.Duration) (*frame, error) {
	if timeout > 0 {
		_ = s.conn.SetReadDeadline(time.Now().Add(timeout))
	} else {
		_ = s.conn.SetReadDeadline(time.Time{})
	}
	var head [4]byte
	if _, err := io.ReadFull(s.rw, head[:]); err != nil {
		return nil, err
	}
	typ := binary.BigEndian.Uint16(head[0:])
	length := int(binary.BigEndian.Uint16(head[2:]))
	if length < 4 || length > sstp.MaxFrame {
		return nil, fmt.Errorf("SSTP 帧长度异常：%d", length)
	}
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(s.rw, payload); err != nil {
		return nil, err
	}
	return &frame{typ: typ, payload: payload}, nil
}

func (s *Session) writeRaw(data []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := s.conn.Write(data)
	return err
}

func (s *Session) writeFrame(frame []byte) error {
	if frame == nil {
		return nil
	}
	return s.writeRaw(frame)
}

func (s *Session) writePPP(proto uint16, code, id byte, data []byte) error {
	return s.writeRaw(ppp.EncodePPP(proto, code, id, data))
}

func (s *Session) sendQuietly(data []byte) {
	if err := s.writeRaw(data); err != nil {
		s.fail(fmt.Errorf("%w：%v", ErrDisconnected, err))
	}
}

func (s *Session) fail(err error) {
	s.errOnce.Do(func() {
		if err == nil {
			err = ErrDisconnected
		}
		if errors.Is(err, io.EOF) {
			err = fmt.Errorf("%w：服务器关闭了连接", ErrDisconnected)
		}
		select {
		case s.errCh <- err:
		default:
		}
	})
}

func (s *Session) cleanup() {
	if s.tun != nil {
		_ = s.tun.Close()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

func joinIPs(ips []net.IP) string {
	if len(ips) == 0 {
		return "未下发"
	}
	return strings.Join(ipStrings(ips), ", ")
}

func tlsVersion(v uint16) string {
	switch v {
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return "TLS"
}

func hashName(alg byte) string {
	if alg == sstp.HashSHA1 {
		return "SHA-1"
	}
	return "SHA-256"
}

func chapMessage(data []byte) string {
	if len(data) == 0 {
		return "原因未知"
	}
	s := string(data)
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func isTimeout(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return false
}

func push(ch chan pppFrame, f pppFrame) {
	select {
	case ch <- f:
	default:
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// frameID 给 PPP 配置帧分配递增的标识符。
var frameCounter atomic.Int32

func frameID() byte {
	return byte(frameCounter.Add(1))
}

var guidCounter atomic.Uint32

// newGUID 生成 SSTP 的 correlation id（RFC 4122 形式）。
func newGUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := guidCounter.Add(1)
		binary.BigEndian.PutUint32(b[0:], n)
	}
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	return fmt.Sprintf("%02X%02X%02X%02X-%02X%02X-%02X%02X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		b[0], b[1], b[2], b[3], b[4], b[5], b[6], b[7], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}
