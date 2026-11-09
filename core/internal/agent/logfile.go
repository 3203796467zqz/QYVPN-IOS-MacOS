// SPDX-License-Identifier: GPL-2.0-or-later
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	logMaxBytes = 1 << 20
	logKeep     = 1
)

// logFile 是连接日志：单文件约 1 MiB 轮转，密码与令牌脱敏。
type logFile struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func openLog(path string) (*logFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, _ := f.Stat()
	l := &logFile{path: path, file: f}
	if info != nil {
		l.size = info.Size()
	}
	return l, nil
}

func (l *logFile) write(format string, args ...any) {
	if l == nil {
		return
	}
	msg := redact(fmt.Sprintf(format, args...))
	line := time.Now().Format("2006-01-02 15:04:05") + "  " + msg + "\n"
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(line)) > logMaxBytes {
		l.rotate()
	}
	n, err := l.file.WriteString(line)
	if err == nil {
		l.size += int64(n)
	}
}

func (l *logFile) rotate() {
	_ = l.file.Close()
	backup := l.path + ".1"
	for i := logKeep; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", l.path, i)
		if i == logKeep {
			_ = os.Remove(src)
			continue
		}
		_ = os.Rename(src, fmt.Sprintf("%s.%d", l.path, i+1))
	}
	_ = os.Rename(l.path, backup)
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return
	}
	l.file = f
	l.size = 0
}

// tail 返回最后 n 行日志。
func (l *logFile) tail(n int) []string {
	if l == nil {
		return nil
	}
	if n <= 0 || n > 2000 {
		n = 200
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func (l *logFile) Close() {
	if l == nil || l.file == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.file.Close()
}

var (
	passwordRE = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[=:]\s*\S+`)
	tokenRE    = regexp.MustCompile(`(?i)(token|authorization|bearer)\s*[:=]?\s*(?:bearer\s+)?[A-Za-z0-9._\-]{16,}`)
)

// redact 把可能出现在日志里的密码与令牌换掉。
func redact(s string) string {
	s = passwordRE.ReplaceAllString(s, "$1=***")
	s = tokenRE.ReplaceAllString(s, "$1=***")
	return s
}
