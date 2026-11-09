// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ledger 按「节点 + 计费月」保存累计流量与上报序号。
// 累计值单调递增，重试或延迟上报不会重复计数（与后台的幂等规则对应）。
type ledger struct {
	mu       sync.Mutex
	path     string
	Sequence int64                            `json:"sequence"`
	Nodes    map[string]map[string]*ledgerRow `json:"nodes"`
}

type ledgerRow struct {
	Upload   int64 `json:"upload"`
	Download int64 `json:"download"`
}

func loadLedger(path string) *ledger {
	l := &ledger{path: path, Nodes: map[string]map[string]*ledgerRow{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, l)
	}
	if l.Nodes == nil {
		l.Nodes = map[string]map[string]*ledgerRow{}
	}
	return l
}

func (l *ledger) row(nodeID, month string) *ledgerRow {
	byMonth, ok := l.Nodes[nodeID]
	if !ok {
		byMonth = map[string]*ledgerRow{}
		l.Nodes[nodeID] = byMonth
	}
	r, ok := byMonth[month]
	if !ok {
		r = &ledgerRow{}
		byMonth[month] = r
	}
	return r
}

// add 累加一次采样增量（up 为上传，down 为下载）。
func (l *ledger) add(nodeID, month string, up, down int64) {
	if up <= 0 && down <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.row(nodeID, month)
	r.Upload += up
	r.Download += down
}

// used 返回某节点当月累计流量。
func (l *ledger) used(nodeID, month string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if byMonth, ok := l.Nodes[nodeID]; ok {
		if r, ok := byMonth[month]; ok {
			return r.Upload + r.Download
		}
	}
	return 0
}

// totals 返回某节点当月上传/下载累计。
func (l *ledger) totals(nodeID, month string) (up, down int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if byMonth, ok := l.Nodes[nodeID]; ok {
		if r, ok := byMonth[month]; ok {
			return r.Upload, r.Download
		}
	}
	return 0, 0
}

// nextSeq 返回递增的上报序号（必须大于 0）。
func (l *ledger) nextSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Sequence++
	if l.Sequence < 1 {
		l.Sequence = 1
	}
	return l.Sequence
}

// save 落盘；只保留当月与上个月的记录，避免文件无限增长。
func (l *ledger) save() {
	l.mu.Lock()
	bj := time.Now().UTC().Add(8 * time.Hour)
	current := bj.Format("2006-01")
	previous := bj.AddDate(0, -1, 0).Format("2006-01")
	for nodeID, byMonth := range l.Nodes {
		for month := range byMonth {
			if month != current && month != previous {
				delete(byMonth, month)
			}
		}
		if len(byMonth) == 0 {
			delete(l.Nodes, nodeID)
		}
	}
	data, err := json.MarshalIndent(l, "", "  ")
	l.mu.Unlock()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}
