package console

// ANSI 颜色码。Theme 里的颜色字段直接填这些常量，空串表示不着色。
// 需要换配色时自定义即可，例如 256 色：Theme{ContentColor: "\033[38;5;39m"}。
const (
	ColorReset   = "\033[0m"
	ColorBold    = "\033[1m"
	ColorDim     = "\033[90m"
	ColorRed     = "\033[31m"
	ColorGreen   = "\033[32m"
	ColorYellow  = "\033[33m"
	ColorBlue    = "\033[34m"
	ColorMagenta = "\033[35m"
	ColorCyan    = "\033[36m"
)

// Labels 是终端上出现的一切文案。留空字段用 DefaultLabels() 补齐，
// 所以只想改一两条时，先取 DefaultLabels() 再覆盖即可。
type Labels struct {
	Title          string // 启动横幅标题
	Intro          string // 启动横幅副标题（一般提示 /help）
	You            string // 用户输入提示符
	Assistant      string // 助手正文头部
	Thinking       string // 思考过程头部
	ToolCall       string // 工具调用头部
	ToolResult     string // 工具结果头部
	SubAgentStart  string // 子智能体启动标记（渲染器会自动补 [id]）
	SubAgentEnd    string // 子智能体结束标记（同上）
	Error          string // 错误前缀
	Bye            string // 退出语
	Interrupted    string // 被 Ctrl+C 打断时的提示
	Cleared        string // /clear 后的提示
	UnknownCommand string // 未知 /命令 的提示
	Stats          string // 统计信息前缀
}

// DefaultLabels 返回中文 + emoji 的默认文案。
func DefaultLabels() Labels {
	return Labels{
		Title:          "💬 终端助手",
		Intro:          "输入消息开始对话；/help 查看命令，/exit 退出",
		You:            "你",
		Assistant:      "🤖 助手",
		Thinking:       "💭 思考",
		ToolCall:       "📋 工具调用",
		ToolResult:     "📄 结果",
		SubAgentStart:  "┌─ 子智能体",
		SubAgentEnd:    "└─ 子智能体",
		Error:          "❌ 错误",
		Bye:            "再见！",
		Interrupted:    "（已中断本轮）",
		Cleared:        "上下文已清空。",
		UnknownCommand: "未知命令",
		Stats:          "──",
	}
}

// PlainLabels 返回纯 ASCII 文案，适合管道、日志、CI 等不适合渲染 emoji 的场景。
func PlainLabels() Labels {
	return Labels{
		Title:          "terminal agent",
		Intro:          "type a message; /help for commands, /exit to quit",
		You:            "you",
		Assistant:      "assistant",
		Thinking:       "thinking",
		ToolCall:       "tool call",
		ToolResult:     "tool result",
		SubAgentStart:  "+- sub agent",
		SubAgentEnd:    "+- sub agent",
		Error:          "error",
		Bye:            "bye",
		Interrupted:    "(interrupted)",
		Cleared:        "context cleared",
		UnknownCommand: "unknown command",
		Stats:          "--",
	}
}

// Theme 决定终端渲染的文案与配色。零值不可用，请用 DefaultTheme/PlainTheme
// 起手再改字段。
type Theme struct {
	Labels Labels

	// 配色（ANSI 转义序列，空串 = 不着色）
	PromptColor     string // 用户提示符
	ReasoningColor  string // 思考过程
	ContentColor    string // 助手正文
	ToolColor       string // 工具调用
	ToolResultColor string // 工具结果
	SubAgentColor   string // 子智能体
	MetaColor       string // 统计、提示等元信息
	ErrorColor      string // 错误

	NoColor bool // 为 true 时忽略以上全部配色（内容原样输出）
}

// DefaultTheme 是带上色与 emoji 的默认主题。
func DefaultTheme() Theme {
	return Theme{
		Labels:          DefaultLabels(),
		PromptColor:     ColorBold,
		ReasoningColor:  ColorDim,
		ContentColor:    ColorCyan,
		ToolColor:       ColorYellow,
		ToolResultColor: ColorGreen,
		SubAgentColor:   ColorMagenta,
		MetaColor:       ColorDim,
		ErrorColor:      ColorRed,
	}
}

// PlainTheme 是无配色、纯 ASCII 的主题，便于把输出重定向到文件或喂给别的程序。
func PlainTheme() Theme {
	return Theme{Labels: PlainLabels(), NoColor: true}
}

// withDefaults 把零值字段补齐成 DefaultTheme 的值，让 Theme{} 也能安全使用。
// 只补文案与配色，不补 NoColor（那是显式语义）。
func (t Theme) withDefaults() Theme {
	d := DefaultTheme()
	if t.Labels.Title == "" && t.Labels.You == "" && t.Labels.Assistant == "" {
		t.Labels = d.Labels
	} else {
		t.Labels = fillLabels(t.Labels, d.Labels)
	}
	if t.PromptColor == "" {
		t.PromptColor = d.PromptColor
	}
	if t.ReasoningColor == "" {
		t.ReasoningColor = d.ReasoningColor
	}
	if t.ContentColor == "" {
		t.ContentColor = d.ContentColor
	}
	if t.ToolColor == "" {
		t.ToolColor = d.ToolColor
	}
	if t.ToolResultColor == "" {
		t.ToolResultColor = d.ToolResultColor
	}
	if t.SubAgentColor == "" {
		t.SubAgentColor = d.SubAgentColor
	}
	if t.MetaColor == "" {
		t.MetaColor = d.MetaColor
	}
	if t.ErrorColor == "" {
		t.ErrorColor = d.ErrorColor
	}
	return t
}

// fillLabels 用 def 补齐 base 里为空的文案。
func fillLabels(base, def Labels) Labels {
	set := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	set(&base.Title, def.Title)
	set(&base.Intro, def.Intro)
	set(&base.You, def.You)
	set(&base.Assistant, def.Assistant)
	set(&base.Thinking, def.Thinking)
	set(&base.ToolCall, def.ToolCall)
	set(&base.ToolResult, def.ToolResult)
	set(&base.SubAgentStart, def.SubAgentStart)
	set(&base.SubAgentEnd, def.SubAgentEnd)
	set(&base.Error, def.Error)
	set(&base.Bye, def.Bye)
	set(&base.Interrupted, def.Interrupted)
	set(&base.Cleared, def.Cleared)
	set(&base.UnknownCommand, def.UnknownCommand)
	set(&base.Stats, def.Stats)
	return base
}

// paint 给 s 套上色码；s 为空或主题关闭着色时原样返回。
func (t Theme) paint(color, s string) string {
	if s == "" || color == "" || t.NoColor {
		return s
	}
	return color + s + ColorReset
}

// reset 返回颜色重置序列（关闭着色时为空串）。
func (t Theme) reset() string {
	if t.NoColor {
		return ""
	}
	return ColorReset
}

// Header 渲染一个"小标题"，供业务侧自定义输出复用：
//
//	fmt.Fprintln(out, th.Header(th.Labels.ToolCall))
func (t Theme) Header(label string) string {
	return t.paint(t.MetaColor, label)
}
