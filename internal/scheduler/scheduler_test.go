package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"qka/internal/engine"
)

func TestTaskHashIsStableAndConfigDirSpecific(t *testing.T) {
	a := TaskHash("/tmp/qka-a")
	b := TaskHash("/tmp/qka-b")
	if len(a) != 8 || a == b {
		t.Fatalf("配置目录应派生不同短哈希，a=%q b=%q", a, b)
	}
	if again := TaskHash("/tmp/qka-a/"); again != a {
		t.Fatalf("规范化路径后短哈希应稳定，first=%q again=%q", a, again)
	}
	if !strings.HasPrefix(LaunchdLabel("/tmp/qka-a"), "com.qka.agent.") ||
		!strings.Contains(WindowsTaskName("/tmp/qka-a"), "QKA") {
		t.Fatal("调度任务名应可识别 QKA")
	}
}

func TestTaskHashWindowsNormalization(t *testing.T) {
	old := taskHashWindows
	t.Cleanup(func() { taskHashWindows = old })
	taskHashWindows = true
	if got, want := TaskHash("/tmp/QKA/Config"), TaskHash("/tmp/qka/config"); got != want {
		t.Fatalf("Windows 路径应大小写归一，got=%s want=%s", got, want)
	}
}

func TestLaunchdPlistUsesTaskRunAndNoSecrets(t *testing.T) {
	data, err := LaunchdPlist(Spec{
		Executable:      "/Applications/QKA.app/Contents/MacOS/qka",
		ConfigDir:       "/tmp/qka-a",
		IntervalMinutes: 30,
		Enabled:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"<string>task-run</string>",
		"<string>--scheduled</string>",
		"<string>--config-dir</string>",
		"<string>/tmp/qka-a</string>",
		"<key>StartInterval</key>",
		"<integer>1800</integer>",
		"<key>AbandonProcessGroup</key>",
		"<key>RunAtLoad</key>",
		"<true/>",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plist 缺少 %q:\n%s", want, text)
		}
	}
	assertNoSecrets(t, text)
}

func TestLaunchdPlistRejectsInvalidInputAndHandlesBareExecutable(t *testing.T) {
	if _, err := LaunchdPlist(Spec{Executable: "\x00", ConfigDir: "/tmp/q", IntervalMinutes: 30}); err == nil {
		t.Fatal("含 NUL 的 executable 必须拒绝")
	}
	for _, interval := range []int{4, 7*24*60 + 1} {
		if _, err := LaunchdPlist(Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: interval}); err == nil {
			t.Errorf("非法间隔 %d 必须拒绝", interval)
		}
	}
	data, err := LaunchdPlist(Spec{Executable: "/tmp/qka", ConfigDir: "/tmp/q", IntervalMinutes: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "AssociatedBundleIdentifiers") {
		t.Fatal("独立 executable 不应关联 App bundle")
	}
}

func TestWindowsTaskXMLIsLeastPrivilegeAndWakeCapable(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, engine.Beijing)
	data, err := WindowsTaskXML(Spec{
		Executable:      `C:\Program Files\QKA\qka.exe`,
		ConfigDir:       `C:\Users\test\.qka`,
		IntervalMinutes: 30,
		Enabled:         true,
	}, now, `QKA-TEST\user`)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"<RunLevel>LeastPrivilege</RunLevel>",
		"<UserId>QKA-TEST\\user</UserId>",
		"<StartWhenAvailable>true</StartWhenAvailable>",
		"<WakeToRun>true</WakeToRun>",
		"<Interval>PT30M</Interval>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		`task-run --scheduled --config-dir C:\Users\test\.qka`,
		`<Command>C:\Program Files\QKA\qka.exe</Command>`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Task Scheduler XML 缺少 %q:\n%s", want, text)
		}
	}
	assertNoSecrets(t, text)
}

func TestExecutableWithNULRejected(t *testing.T) {
	_, err := LaunchdPlist(Spec{
		Executable: "/tmp/qka\x00", ConfigDir: "/tmp/q", IntervalMinutes: 30,
	})
	if err == nil {
		t.Fatal("含 NUL 的可执行路径必须拒绝")
	}
}

func TestParseClock(t *testing.T) {
	for value, want := range map[string][2]int{
		"00:00": {0, 0}, "09:10": {9, 10}, "23:59": {23, 59},
	} {
		hour, minute, err := parseClock(value)
		if err != nil || hour != want[0] || minute != want[1] {
			t.Errorf("parseClock(%q)=(%d,%d,%v), want %v", value, hour, minute, err, want)
		}
	}
	for _, value := range []string{"", "9:10", "24:00", "12:60", "12:00:00", " 09:10"} {
		if _, _, err := parseClock(value); err == nil {
			t.Errorf("parseClock(%q) 应拒绝", value)
		}
	}
}

func TestTaskHashCanonicalizesSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if got, want := TaskHash(link), TaskHash(target); got != want {
		t.Fatalf("符号链接与目标应派生相同任务哈希: %s != %s", got, want)
	}
}

func TestWindowsTaskXMLValidationAndTimeBoundary(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local)
	if _, err := WindowsTaskXML(Spec{Executable: " \t", IntervalMinutes: 30}, now, ""); err == nil {
		t.Fatal("空白 executable 必须拒绝")
	}
	for _, interval := range []int{4, 7*24*60 + 1} {
		if _, err := WindowsTaskXML(Spec{Executable: "qka.exe", IntervalMinutes: interval}, now, ""); err == nil {
			t.Errorf("间隔 %d 必须拒绝", interval)
		}
	}
	minuteBoundary := time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local)
	data, err := WindowsTaskXML(Spec{Executable: "qka.exe", IntervalMinutes: 5}, minuteBoundary, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "2026-10-30T08:00:00") {
		t.Fatalf("整分钟边界不应额外进位: %s", data)
	}
	betweenMinutes := minuteBoundary.Add(30 * time.Second)
	data, err = WindowsTaskXML(Spec{Executable: "qka.exe", IntervalMinutes: 5}, betweenMinutes, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "2026-10-30T08:01:00") {
		t.Fatalf("非整分钟起始时间应进位到下一分钟: %s", data)
	}
}

func TestRealRunnerAndNewAreUsableWithoutInstallingTasks(t *testing.T) {
	if _, err := realRunner(context.Background(), "/usr/bin/true"); err != nil {
		t.Fatalf("本地无副作用命令应运行成功: %v", err)
	}
	if _, err := realRunner(context.Background(), "/usr/bin/false"); err == nil {
		t.Fatal("非零退出状态必须由 realRunner 返回错误")
	}
	if New() == nil {
		t.Fatal("New 必须返回 scheduler")
	}
}

func TestWindowsTaskXMLQuotesConfigDir(t *testing.T) {
	data, err := WindowsTaskXML(Spec{
		Executable:      `C:\QKA & Tools\QKA.exe`,
		ConfigDir:       `C:\Users\me\QKA & Data`,
		IntervalMinutes: 30,
	}, time.Date(2026, 10, 30, 8, 0, 0, 0, time.Local), "")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`C:\QKA &amp; Tools\QKA.exe`,
		`--config-dir &#34;C:\Users\me\QKA &amp; Data&#34;`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("Windows XML 缺少转义后的 %q:\n%s", want, text)
		}
	}
}

func TestQuoteWindowsArgumentEscapesQuotesAndTrailingBackslashes(t *testing.T) {
	got := quoteWindowsArgument("C:\\QKA\"Data\\")
	want := `"C:\QKA\"Data\\"`
	if got != want {
		t.Fatalf("quoted argument = %q, want %q", got, want)
	}
}

func assertNoSecrets(t *testing.T, text string) {
	t.Helper()
	lower := strings.ToLower(text)
	for _, forbidden := range []string{"access_token", "refresh_token", "authorization", "cookie", "uid"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("调度文件不应包含敏感字段 %q: %s", forbidden, text)
		}
	}
}
