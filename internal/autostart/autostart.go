// Package autostart 生成 / 解析「登录时自启」用的 Windows 计划任务 XML，
// 以及自启相关的命令行参数处理。
//
// 这里只放纯逻辑（不调用任何 Windows API），所以可以在 Linux CI / 开发机上跑单测；
// 真正调用 schtasks.exe、读写注册表的部分在主包的 autostart_windows.go。
//
// 为什么不用 HKCU\...\Run：程序清单是 requireAdministrator，Windows 登录时
// 不会为 Run 键 / 启动文件夹里需要提权的程序弹 UAC，而是直接跳过，所以开机永远不自启。
// 计划任务由 Task Scheduler 服务以「最高权限」（RunLevel=HighestAvailable）启动，
// 管理员账户登录后可以不弹 UAC 直接拉起提权进程。
package autostart

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

const (
	// TaskName 是计划任务名（根目录下），在「任务计划程序」里可以看到。
	TaskName = "KNcloud-WIN"

	// Arg 由计划任务传给程序：表示这次是开机自启，主窗口不弹出、只放托盘。
	Arg = "--autostart"

	// DefaultDelay 登录后延迟启动，给网络 / 资源管理器托盘区一点就绪时间。
	DefaultDelay = "PT15S"
)

// TaskConfig 描述要注册的计划任务。
type TaskConfig struct {
	// UserID 触发与运行身份：当前用户的 SID（推荐）或 DOMAIN\user。
	UserID string
	// Command 可执行文件绝对路径（不带引号，生成时会加引号）。
	Command string
	// Arguments 传给程序的参数，通常是 Arg。
	Arguments string
	// WorkingDirectory 工作目录，通常是 exe 所在目录；为空则不写。
	WorkingDirectory string
	// Delay 登录后延迟（xs:duration，如 PT15S）；为空则不延迟。
	Delay string
	// Description 任务描述。
	Description string
}

// textEscaper 转义 XML 文本节点。只处理 & < >（引号在文本节点里不必转义，
// 保持 <Command>"C:\..."</Command> 与任务计划程序导出的格式一致、便于人工查看）。
var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escape(s string) string { return textEscaper.Replace(s) }

// QuoteCommand 给路径加双引号（已带引号的不重复加）。
func QuoteCommand(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 2 && strings.HasPrefix(path, `"`) && strings.HasSuffix(path, `"`) {
		return path
	}
	return `"` + path + `"`
}

// BuildTaskXML 生成 Task Scheduler 1.2 格式的任务 XML（字符串本身是 UTF-8 的 Go string，
// 写文件给 schtasks /XML 时请用 EncodeUTF16LE 转成带 BOM 的 UTF-16LE，与声明的 encoding 一致）。
//
// 要点：
//   - LogonTrigger 绑定当前用户，Principal 用 InteractiveToken（只在用户登录的会话里运行，才能显示托盘 / 窗口）；
//   - RunLevel=HighestAvailable：以最高权限运行，满足清单里的 requireAdministrator；
//   - 关掉「仅交流电」「空闲」等限制，ExecutionTimeLimit=PT0S 不限时（默认 72 小时会被强杀）；
//   - Priority=4：普通优先级（任务默认 7 是「低于正常」）；
//   - MultipleInstancesPolicy=IgnoreNew：任务已在运行时不再起新实例（程序自身也有单实例锁）。
func BuildTaskXML(c TaskConfig) (string, error) {
	if strings.TrimSpace(c.Command) == "" {
		return "", errors.New("autostart: empty command")
	}
	if strings.TrimSpace(c.UserID) == "" {
		return "", errors.New("autostart: empty user id")
	}
	desc := c.Description
	if desc == "" {
		desc = "登录 Windows 后自动启动 KNcloud-WIN（由 KNcloud-WIN 设置页「开机自动启动」管理）"
	}

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-16"?>` + "\r\n")
	b.WriteString(`<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\r\n")
	b.WriteString("  <RegistrationInfo>\r\n")
	b.WriteString("    <Author>KNcloud-WIN</Author>\r\n")
	fmt.Fprintf(&b, "    <Description>%s</Description>\r\n", escape(desc))
	fmt.Fprintf(&b, "    <URI>\\%s</URI>\r\n", escape(TaskName))
	b.WriteString("  </RegistrationInfo>\r\n")

	// LogonTrigger 子元素有顺序要求：Enabled → UserId → Delay
	b.WriteString("  <Triggers>\r\n")
	b.WriteString("    <LogonTrigger>\r\n")
	b.WriteString("      <Enabled>true</Enabled>\r\n")
	fmt.Fprintf(&b, "      <UserId>%s</UserId>\r\n", escape(c.UserID))
	if c.Delay != "" {
		fmt.Fprintf(&b, "      <Delay>%s</Delay>\r\n", escape(c.Delay))
	}
	b.WriteString("    </LogonTrigger>\r\n")
	b.WriteString("  </Triggers>\r\n")

	b.WriteString("  <Principals>\r\n")
	b.WriteString(`    <Principal id="Author">` + "\r\n")
	fmt.Fprintf(&b, "      <UserId>%s</UserId>\r\n", escape(c.UserID))
	b.WriteString("      <LogonType>InteractiveToken</LogonType>\r\n")
	b.WriteString("      <RunLevel>HighestAvailable</RunLevel>\r\n")
	b.WriteString("    </Principal>\r\n")
	b.WriteString("  </Principals>\r\n")

	b.WriteString("  <Settings>\r\n")
	b.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\r\n")
	b.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\r\n")
	b.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\r\n")
	b.WriteString("    <AllowHardTerminate>true</AllowHardTerminate>\r\n")
	b.WriteString("    <StartWhenAvailable>false</StartWhenAvailable>\r\n")
	b.WriteString("    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>\r\n")
	b.WriteString("    <IdleSettings>\r\n")
	b.WriteString("      <StopOnIdleEnd>false</StopOnIdleEnd>\r\n")
	b.WriteString("      <RestartOnIdle>false</RestartOnIdle>\r\n")
	b.WriteString("    </IdleSettings>\r\n")
	b.WriteString("    <AllowStartOnDemand>true</AllowStartOnDemand>\r\n")
	b.WriteString("    <Enabled>true</Enabled>\r\n")
	b.WriteString("    <Hidden>false</Hidden>\r\n")
	b.WriteString("    <RunOnlyIfIdle>false</RunOnlyIfIdle>\r\n")
	b.WriteString("    <WakeToRun>false</WakeToRun>\r\n")
	b.WriteString("    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\r\n")
	b.WriteString("    <Priority>4</Priority>\r\n")
	b.WriteString("  </Settings>\r\n")

	b.WriteString(`  <Actions Context="Author">` + "\r\n")
	b.WriteString("    <Exec>\r\n")
	fmt.Fprintf(&b, "      <Command>%s</Command>\r\n", escape(QuoteCommand(c.Command)))
	if c.Arguments != "" {
		fmt.Fprintf(&b, "      <Arguments>%s</Arguments>\r\n", escape(c.Arguments))
	}
	if c.WorkingDirectory != "" {
		fmt.Fprintf(&b, "      <WorkingDirectory>%s</WorkingDirectory>\r\n", escape(c.WorkingDirectory))
	}
	b.WriteString("    </Exec>\r\n")
	b.WriteString("  </Actions>\r\n")
	b.WriteString("</Task>\r\n")
	return b.String(), nil
}

// EncodeUTF16LE 把字符串编码成带 BOM 的 UTF-16LE（schtasks /XML 要求文件编码与声明一致）。
func EncodeUTF16LE(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2+2*len(u))
	out[0], out[1] = 0xFF, 0xFE
	for i, v := range u {
		out[2+2*i] = byte(v)
		out[3+2*i] = byte(v >> 8)
	}
	return out
}

// DecodeText 把任务 XML 原始字节转成 UTF-8 字符串：识别 UTF-16LE/BE（有无 BOM 都行）与 UTF-8 BOM。
// %SystemRoot%\System32\Tasks\<名称> 文件是 UTF-16LE 带 BOM；schtasks /Query /XML 的输出视系统而定。
func DecodeText(data []byte) string {
	switch {
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE:
		return decodeUTF16(data[2:], false)
	case len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF:
		return decodeUTF16(data[2:], true)
	case len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF:
		return string(data[3:])
	case len(data) >= 2 && data[0] == '<' && data[1] == 0:
		return decodeUTF16(data, false)
	case len(data) >= 2 && data[0] == 0 && data[1] == '<':
		return decodeUTF16(data, true)
	}
	return string(data)
}

func decodeUTF16(b []byte, bigEndian bool) string {
	n := len(b) / 2
	u := make([]uint16, n)
	for i := 0; i < n; i++ {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
		}
	}
	return string(utf16.Decode(u))
}

// TaskInfo 是从已注册任务 XML 里读出的、判断「自启是否指向当前程序」所需的字段。
type TaskInfo struct {
	Command   string
	Arguments string
	Enabled   bool // 任务 Settings/Enabled（缺省为 true）
	RunLevel  string
}

type taskDoc struct {
	Settings struct {
		Enabled *bool `xml:"Enabled"`
	} `xml:"Settings"`
	Principals struct {
		Principal []struct {
			RunLevel string `xml:"RunLevel"`
		} `xml:"Principal"`
	} `xml:"Principals"`
	Actions struct {
		Exec []struct {
			Command   string `xml:"Command"`
			Arguments string `xml:"Arguments"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

// ParseTaskXML 解析任务 XML（原始字节，UTF-8 或 UTF-16 皆可），取第一个 Exec 动作。
func ParseTaskXML(data []byte) (TaskInfo, error) {
	text := DecodeText(data)
	dec := xml.NewDecoder(strings.NewReader(text))
	// 已经转成 UTF-8 了，声明里的 encoding="UTF-16" 直接放行
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var doc taskDoc
	if err := dec.Decode(&doc); err != nil {
		return TaskInfo{}, fmt.Errorf("autostart: parse task xml: %w", err)
	}
	if len(doc.Actions.Exec) == 0 {
		return TaskInfo{}, errors.New("autostart: task has no exec action")
	}
	info := TaskInfo{
		Command:   strings.TrimSpace(doc.Actions.Exec[0].Command),
		Arguments: strings.TrimSpace(doc.Actions.Exec[0].Arguments),
		Enabled:   doc.Settings.Enabled == nil || *doc.Settings.Enabled,
	}
	if len(doc.Principals.Principal) > 0 {
		info.RunLevel = strings.TrimSpace(doc.Principals.Principal[0].RunLevel)
	}
	return info, nil
}

// expandWindowsEnv 展开 %VAR% 形式的环境变量（Task Scheduler 允许 Command 里用），找不到的保持原样。
func expandWindowsEnv(s string, lookup func(string) (string, bool)) string {
	if lookup == nil || !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			break
		}
		name := s[i+1 : i+1+j]
		b.WriteString(s[:i])
		if v, ok := lookup(name); ok && name != "" {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : i+2+j])
		}
		s = s[i+2+j:]
	}
	return b.String()
}

// NormalizePath 规整 Windows 路径用于比较：去空白与引号、展开 %VAR%、统一反斜杠、去掉末尾分隔符。
func NormalizePath(p string, lookup func(string) (string, bool)) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"`)
	p = expandWindowsEnv(p, lookup)
	p = strings.ReplaceAll(p, "/", `\`)
	for len(p) > 3 && strings.HasSuffix(p, `\`) {
		p = strings.TrimSuffix(p, `\`)
	}
	return p
}

// SamePath 不区分大小写比较两个 Windows 路径（NTFS 默认不区分大小写）。
func SamePath(a, b string, lookup func(string) (string, bool)) bool {
	na, nb := NormalizePath(a, lookup), NormalizePath(b, lookup)
	return na != "" && strings.EqualFold(na, nb)
}

// PointsTo 判断已注册的任务是否就是「当前 exe + 自启参数 + 已启用 + 最高权限」，
// 任何一项不符（例如换了安装路径、老版本留下的任务）都应重建。
func (t TaskInfo) PointsTo(exe string, lookup func(string) (string, bool)) bool {
	return t.Enabled &&
		strings.EqualFold(t.RunLevel, "HighestAvailable") &&
		SamePath(t.Command, exe, lookup) &&
		HasArg(strings.Fields(t.Arguments))
}

// HasArg 命令行里是否带了自启参数（大小写不敏感，兼容 /autostart、-autostart 写法）。
func HasArg(args []string) bool {
	for _, a := range args {
		a = strings.ToLower(strings.Trim(strings.TrimSpace(a), `"`))
		switch a {
		case Arg, "-autostart", "/autostart":
			return true
		}
	}
	return false
}
