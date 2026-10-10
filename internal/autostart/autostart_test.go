package autostart

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func testCfg() TaskConfig {
	return TaskConfig{
		UserID:           "S-1-5-21-1111-2222-3333-1001",
		Command:          `C:\Program Files\KNcloud & Co\KNcloud-WIN.exe`,
		Arguments:        Arg,
		WorkingDirectory: `C:\Program Files\KNcloud & Co`,
		Delay:            DefaultDelay,
	}
}

func TestBuildTaskXMLIsWellFormedAndHasRequiredSettings(t *testing.T) {
	x, err := BuildTaskXML(testCfg())
	if err != nil {
		t.Fatal(err)
	}
	// 必须是合法 XML（& 等字符已转义）
	dec := xml.NewDecoder(strings.NewReader(x))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	for {
		if _, err := dec.Token(); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("not well-formed: %v", err)
		}
	}
	for _, want := range []string{
		`encoding="UTF-16"`,
		`<LogonTrigger>`,
		`<UserId>S-1-5-21-1111-2222-3333-1001</UserId>`,
		`<Delay>PT15S</Delay>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<RunLevel>HighestAvailable</RunLevel>`,
		`<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>`,
		`<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`<StopOnIdleEnd>false</StopOnIdleEnd>`,
		`<RunOnlyIfIdle>false</RunOnlyIfIdle>`,
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`<Command>"C:\Program Files\KNcloud &amp; Co\KNcloud-WIN.exe"</Command>`,
		`<Arguments>--autostart</Arguments>`,
		`<WorkingDirectory>C:\Program Files\KNcloud &amp; Co</WorkingDirectory>`,
		`<URI>\KNcloud-WIN</URI>`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("task xml missing %q", want)
		}
	}
}

func TestLogonTriggerChildOrder(t *testing.T) {
	// 模式要求 LogonTrigger 子元素按 Enabled → UserId → Delay 的顺序
	x, _ := BuildTaskXML(testCfg())
	start := strings.Index(x, "<LogonTrigger>")
	end := strings.Index(x, "</LogonTrigger>")
	seg := x[start:end]
	e, u, d := strings.Index(seg, "<Enabled>"), strings.Index(seg, "<UserId>"), strings.Index(seg, "<Delay>")
	if !(e >= 0 && e < u && u < d) {
		t.Fatalf("bad LogonTrigger child order: %s", seg)
	}
}

func TestBuildTaskXMLRejectsEmpty(t *testing.T) {
	c := testCfg()
	c.Command = " "
	if _, err := BuildTaskXML(c); err == nil {
		t.Fatal("want error for empty command")
	}
	c = testCfg()
	c.UserID = ""
	if _, err := BuildTaskXML(c); err == nil {
		t.Fatal("want error for empty user")
	}
}

func TestNoDelayOmitsElement(t *testing.T) {
	c := testCfg()
	c.Delay = ""
	x, _ := BuildTaskXML(c)
	if strings.Contains(x, "<Delay>") {
		t.Fatal("delay element should be omitted")
	}
}

func TestUTF16RoundTripAndParse(t *testing.T) {
	c := testCfg()
	c.Command = `D:\软件\KNcloud-WIN\KNcloud-WIN.exe`
	x, _ := BuildTaskXML(c)
	raw := EncodeUTF16LE(x)
	if raw[0] != 0xFF || raw[1] != 0xFE {
		t.Fatal("missing UTF-16LE BOM")
	}
	if got := DecodeText(raw); got != x {
		t.Fatal("UTF-16 round trip mismatch")
	}
	info, err := ParseTaskXML(raw)
	if err != nil {
		t.Fatal(err)
	}
	if info.Command != `"D:\软件\KNcloud-WIN\KNcloud-WIN.exe"` || info.Arguments != Arg || !info.Enabled || info.RunLevel != "HighestAvailable" {
		t.Fatalf("unexpected parse result: %+v", info)
	}
	if !info.PointsTo(`d:\软件\kncloud-win\KNCLOUD-WIN.EXE`, nil) {
		t.Fatal("should match current exe case-insensitively")
	}
	if info.PointsTo(`C:\Program Files\KNcloud-WIN\KNcloud-WIN.exe`, nil) {
		t.Fatal("must not match a different install path")
	}
}

func TestParseTaskXMLVariants(t *testing.T) {
	// 任务计划程序导出的典型 XML（UTF-8 文本、无 BOM、带默认命名空间、Enabled=false、无参数）
	src := `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Principals><Principal id="Author"><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><Enabled>false</Enabled></Settings>
  <Actions Context="Author"><Exec><Command>%LOCALAPPDATA%\KNcloud\KNcloud-WIN.exe</Command></Exec></Actions>
</Task>`
	info, err := ParseTaskXML([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if info.Enabled || info.RunLevel != "LeastPrivilege" || info.Arguments != "" {
		t.Fatalf("unexpected: %+v", info)
	}
	lookup := func(k string) (string, bool) {
		if strings.EqualFold(k, "LOCALAPPDATA") {
			return `C:\Users\kn\AppData\Local`, true
		}
		return "", false
	}
	if !SamePath(info.Command, `C:\Users\kn\AppData\Local\KNcloud\KNcloud-WIN.exe`, lookup) {
		t.Fatal("env var expansion failed")
	}
	// 禁用 / 非最高权限 / 缺参数 都不算「已正确配置」
	if info.PointsTo(`C:\Users\kn\AppData\Local\KNcloud\KNcloud-WIN.exe`, lookup) {
		t.Fatal("disabled least-privilege task must not count as enabled")
	}

	if _, err := ParseTaskXML([]byte("<Task/>")); err == nil {
		t.Fatal("want error when no exec action")
	}
	if _, err := ParseTaskXML([]byte("not xml")); err == nil {
		t.Fatal("want parse error")
	}
	// UTF-16LE 无 BOM
	noBOM := EncodeUTF16LE(src)[2:]
	if _, err := ParseTaskXML(noBOM); err != nil {
		t.Fatalf("utf-16 without BOM: %v", err)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		`  "C:/A/B.exe"  `: `C:\A\B.exe`,
		`C:\A\`:            `C:\A`,
		`C:\`:              `C:\`,
		`%NOPE%\x.exe`:     `%NOPE%\x.exe`,
		`50%off\x.exe`:     `50%off\x.exe`,
	}
	for in, want := range cases {
		if got := NormalizePath(in, func(string) (string, bool) { return "", false }); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
	if SamePath("", "", nil) {
		t.Error("empty paths must not compare equal")
	}
}

func TestHasArg(t *testing.T) {
	yes := [][]string{{"--autostart"}, {"--AUTOSTART"}, {"x", `"--autostart"`}, {"/autostart"}, {"-autostart"}}
	no := [][]string{nil, {}, {"--autostarted"}, {"kncloud://login?x=--autostart"}, {"--wait-pid", "12"}}
	for _, a := range yes {
		if !HasArg(a) {
			t.Errorf("HasArg(%q) = false", a)
		}
	}
	for _, a := range no {
		if HasArg(a) {
			t.Errorf("HasArg(%q) = true", a)
		}
	}
}

func TestQuoteCommand(t *testing.T) {
	if QuoteCommand(`C:\a b\c.exe`) != `"C:\a b\c.exe"` {
		t.Fatal("quote")
	}
	if QuoteCommand(`"C:\a b\c.exe"`) != `"C:\a b\c.exe"` {
		t.Fatal("double quote")
	}
}
