package autostart

import (
	"strings"
	"testing"
)

func TestPlistContents(t *testing.T) {
	data := Plist("com.qka.agent", "/Applications/qka.app/qka")
	s := string(data)
	for _, want := range []string{
		"<string>com.qka.agent</string>",
		"<string>/Applications/qka.app/qka</string>",
		"<string>tray</string>",
		"<key>RunAtLoad</key>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("plist 应含 %s", want)
		}
	}
}

func TestAutostartOnOff(t *testing.T) {
	home := t.TempDir()
	if IsEnabled(home, "") {
		t.Fatal("默认应未启用")
	}
	if err := On(home, "/Applications/qka.app/qka", ""); err != nil {
		t.Fatal(err)
	}
	if !IsEnabled(home, "") {
		t.Fatal("On 后应启用")
	}
	if err := Off(home, ""); err != nil {
		t.Fatal(err)
	}
	if IsEnabled(home, "") {
		t.Fatal("Off 后应未启用")
	}
	// 再次 Off 幂等。
	if err := Off(home, ""); err != nil {
		t.Fatalf("重复 Off 应幂等，实际 %v", err)
	}
}
