// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"qingyou/qyvpn/internal/model"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.2", "1.10", -1},
		{"2.0", "1.9.9", 1},
		{"1.0.0-beta", "1.0.0", 0},
		{"", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Fatalf("compareVersion(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestLedgerAccumulatesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	l := loadLedger(path)
	month := model.Month(time.Now())
	l.add("node-1", month, 100, 200)
	l.add("node-1", month, 50, 25)
	if got := l.used("node-1", month); got != 375 {
		t.Fatalf("used = %d, want 375", got)
	}
	up, down := l.totals("node-1", month)
	if up != 150 || down != 225 {
		t.Fatalf("totals = %d/%d", up, down)
	}
	if seq := l.nextSeq(); seq != 1 {
		t.Fatalf("首个序号 = %d", seq)
	}
	if seq := l.nextSeq(); seq != 2 {
		t.Fatalf("第二个序号 = %d", seq)
	}
	l.save()

	again := loadLedger(path)
	if got := again.used("node-1", month); got != 375 {
		t.Fatalf("重新加载后 used = %d", got)
	}
	if again.Sequence != 2 {
		t.Fatalf("重新加载后序号 = %d", again.Sequence)
	}
}

func TestLedgerDropsOldMonths(t *testing.T) {
	dir := t.TempDir()
	l := loadLedger(filepath.Join(dir, "ledger.json"))
	l.add("n", "2020-01", 1, 1)
	l.add("n", model.Month(time.Now()), 5, 5)
	l.save()
	if _, ok := l.Nodes["n"]["2020-01"]; ok {
		t.Fatal("两个月以前的记录应被清理")
	}
	if got := l.used("n", model.Month(time.Now())); got != 10 {
		t.Fatalf("当月记录被误删：%d", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	in := Settings{SelectedNode: "abc", BypassEnabled: true, BypassChina: true, BypassDomains: "baidu.com", AutoConnect: true}
	if err := saveSettings(path, in); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		// Windows 不保留 Unix 权限位；macOS 上必须是 0600。
		t.Fatalf("设置文件权限 = %v", info.Mode().Perm())
	}
	if out := loadSettings(path); out != in {
		t.Fatalf("读取结果 = %+v", out)
	}
}

func TestLogRedaction(t *testing.T) {
	cases := map[string]string{
		"password=abc123":                            "password=***",
		"Password: hunter2":                          "Password=***",
		"Authorization: Bearer abcdefghijklmnopqrst": "Authorization=***",
		"正在连接节点 美国1":                                 "正在连接节点 美国1",
	}
	for in, want := range cases {
		if got := redact(in); got != want {
			t.Fatalf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogRotateAndTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connection.log")
	lf, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	for i := 0; i < 20000; i++ {
		lf.write("填充日志行 %d，用于触发轮转", i)
	}
	if info, err := os.Stat(path); err == nil && info.Size() > logMaxBytes*2 {
		t.Fatalf("日志没有轮转：%d 字节", info.Size())
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("缺少轮转备份文件")
	}
	lines := lf.tail(5)
	if len(lines) != 5 {
		t.Fatalf("tail 行数 = %d", len(lines))
	}
}

func TestChinaCIDRsLoaded(t *testing.T) {
	cidrs, err := loadChinaCIDRs()
	if err != nil {
		t.Fatal(err)
	}
	if len(cidrs) < 5000 {
		t.Fatalf("国内网段数量异常：%d", len(cidrs))
	}
}
