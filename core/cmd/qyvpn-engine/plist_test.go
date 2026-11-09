// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestHelperPlistIsValidXML launchd 对 plist 的要求很严：一旦不是合法 XML 或缺少必需键，
// 只会得到「Bootstrap failed: 5: Input/output error」，从现象根本反推不到原因。
func TestHelperPlistIsValidXML(t *testing.T) {
	const (
		label  = "cn.qo8.qyvpn.engine"
		binary = "/Library/PrivilegedHelperTools/cn.qo8.qyvpn.engine"
		log    = "/Library/Logs/cn.qo8.qyvpn.engine.log"
	)
	raw := helperPlist(label, binary, log)

	if strings.Contains(raw, "%!") {
		t.Fatalf("格式占位符没有全部替换：\n%s", raw)
	}

	dec := xml.NewDecoder(strings.NewReader(raw))
	var keys []string
	var inKey bool
	var buf string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if v.Name.Local == "key" {
				inKey = true
				buf = ""
			}
		case xml.CharData:
			if inKey {
				buf += string(v)
			}
		case xml.EndElement:
			if v.Name.Local == "key" && inKey {
				keys = append(keys, strings.TrimSpace(buf))
				inKey = false
			}
		}
	}

	required := []string{"Label", "ProgramArguments", "RunAtLoad", "KeepAlive", "StandardOutPath", "StandardErrorPath"}
	have := map[string]bool{}
	for _, k := range keys {
		have[k] = true
	}
	for _, k := range required {
		if !have[k] {
			t.Fatalf("plist 缺少 %s，实际键：%v", k, keys)
		}
	}

	// 这三点是踩过的坑：Label 与方法名要对上、程序路径要写全、不要再带 ProcessType Interactive
	//（那是给与窗口服务器交互的作业用的，系统域守护进程写了可能被拒）。
	if !strings.Contains(raw, "<string>"+label+"</string>") {
		t.Fatal("Label 未写入")
	}
	if !strings.Contains(raw, "<string>"+binary+"</string>") {
		t.Fatal("程序路径未写入")
	}
	if !strings.Contains(raw, "<string>run</string>") {
		t.Fatal("缺少 run 子命令")
	}
	if strings.Contains(raw, "ProcessType") {
		t.Fatal("不应写 ProcessType")
	}
}
