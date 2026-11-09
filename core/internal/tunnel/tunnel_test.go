// SPDX-License-Identifier: GPL-2.0-or-later
package tunnel

import "testing"

// 真实的 macOS `netstat -ibn` 输出片段（含 Address 缺失与存在两种行）。
const netstatSample = `Name       Mtu   Network       Address            Ipkts Ierrs     Ibytes    Opkts Oerrs     Obytes  Coll
lo0       16384 <Link#1>                        123456     0   12345678   123456     0   12345678     0
lo0       16384 127           localhost          12345     0    1234567     1234     0     1234567     0
en0       1500  <Link#4>    aa:bb:cc:dd:ee:ff  1234567     0  123456789     1234     0   12345678     0
en0       1500  192.168.1.5  192.168.1.10     1234567     0  123456789     1234     0   12345678     0
utun4     1400  <Link#12>                        98765     0   98765432     5432     0    9876543     0
utun4     1400  10.77.88.11  10.77.88.11         98765     0   98765432     5432     0    9876543     0
`

func TestParseNetstat(t *testing.T) {
	s, err := parseNetstat(netstatSample, "utun4")
	if err != nil {
		t.Fatal(err)
	}
	if s.Rx != 98765432 || s.Tx != 9876543 {
		t.Fatalf("utun4 统计 = %+v", s)
	}
	s, err = parseNetstat(netstatSample, "en0")
	if err != nil {
		t.Fatal(err)
	}
	if s.Rx != 123456789 || s.Tx != 12345678 {
		t.Fatalf("en0 统计 = %+v", s)
	}
	if _, err := parseNetstat(netstatSample, "utun9"); err == nil {
		t.Fatal("不存在的接口应报错")
	}
}

func TestParseDefaultRoute(t *testing.T) {
	out := `   route to: default
destination: default
       mask: default
    gateway: 192.168.1.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500         0
`
	gateway, iface := parseDefaultRoute(out)
	if gateway != "192.168.1.1" || iface != "en0" {
		t.Fatalf("默认路由解析 = %q %q", gateway, iface)
	}
}

func TestNormalizeCIDR(t *testing.T) {
	cases := map[string]string{
		"1.2.3.0/24":  "1.2.3.0/24",
		"8.8.8.8":     "8.8.8.8/32",
		" 10.0.0.0/8": "10.0.0.0/8",
		"":            "",
		"#comment":    "",
		"bad":         "",
	}
	for in, want := range cases {
		got, ok := normalizeCIDR(in)
		if want == "" {
			if ok {
				t.Fatalf("normalizeCIDR(%q) 应被拒绝，却得到 %q", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("normalizeCIDR(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}
