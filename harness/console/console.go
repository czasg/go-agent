package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/middleware/approve"
	"github.com/czasg/go-agent/ext/tool/ask"
	"github.com/czasg/go-agent/schema"
)

// stdout 是所有默认输出的落点，抽成函数便于测试替换。
func stdout() io.Writer { return os.Stdout }

// This console is the single place where terminal I/O happens. The REPL and the
// in-loop interactive tools (ask / approve) all read from one shared lineReader,
// which is what makes them safe to combine.

// Console 是终端交互入口：读一行输入 + 向用户提问 + 请求审批。
//
// 它同时满足：
//   - ask.Prompter（Ask）—— 模型主动向用户提问
//   - approve.Approver（Approve）—— 框架级审批中间件回调
//
// 于是 human-in-the-loop 工具与审批中间件可以直接用同一个 Console 构造，
// 和 REPL 共享同一份 stdin，不会再出现"两个 scanner 抢标准输入"的问题：
//
//	console := harness.NewConsole()
//	askTool, _ := ask.New(console)
//	app := harness.NewApp(agent,
//	    harness.WithConsole(console),
//	    harness.WithToolMiddlewares(
//	        ga.ForTools(approve.New(console), "delete_file", "drop_table"),
//	    ),
//	)
type Console struct {
	lr    *lineReader
	w     io.Writer
	theme Theme

	// themeSet 记录主题是调用方显式指定的，还是"零值待补"。
	// App 据此判断能否把 App 级主题下发给这个 Console。
	themeSet bool
}

var (
	_ ask.Prompter     = (*Console)(nil)
	_ approve.Approver = (*Console)(nil)
)

// ConsoleOption 配置 Console。
type ConsoleOption func(*Console)

// WithConsoleInput 指定输入源（默认 os.Stdin）。
// 传入非 os.Stdin 的流时，REPL 与交互工具都会读它——测试据此驱动整个流程。
func WithConsoleInput(r io.Reader) ConsoleOption {
	return func(c *Console) { c.lr = newLineReader(r) }
}

// WithConsoleOutput 指定输出目标（默认 os.Stdout）。
func WithConsoleOutput(w io.Writer) ConsoleOption {
	return func(c *Console) { c.w = w }
}

// WithConsoleTheme 指定主题（文案 + 配色）。
func WithConsoleTheme(t Theme) ConsoleOption {
	return func(c *Console) {
		c.theme = t.withDefaults()
		c.themeSet = true
	}
}

// NewConsole 构造终端交互入口。零选项等价于"标准输入 + 标准输出 + 默认主题"。
func NewConsole(opts ...ConsoleOption) *Console {
	c := &Console{
		w:     stdout(),
		theme: DefaultTheme(),
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.lr == nil {
		c.lr = newLineReader(os.Stdin)
	}
	return c
}

// Writer 返回 Console 的输出目标，便于业务侧用同一处输出保持版式一致。
func (c *Console) Writer() io.Writer { return c.w }

// Theme 返回 Console 使用的主题（已补齐默认值）。
func (c *Console) Theme() Theme { return c.theme }

// applyTheme 由 App 在组装时调用：把 App 级主题下发给 Console。
// 若调用方已通过 WithConsoleTheme 显式指定主题，则尊重调用方的选择。
func (c *Console) applyTheme(t Theme) {
	if c.themeSet {
		return
	}
	c.theme = t.withDefaults()
}

// ReadLine 读取一行输入（已去首尾空白）。上下文取消时立即返回 ctx.Err()。
// App 的 REPL 循环用它，业务侧自定义循环也可以复用。
func (c *Console) ReadLine(ctx context.Context) (string, error) {
	return c.lr.Read(ctx)
}

// Ask 实现 ask.Prompter：渲染问题与选项，阻塞等用户输入编号。
//
// 输入格式：
//   - 多选用逗号分隔，如 "1,3"
//   - 多个问题用空格分隔，如 "1,3 2"
//
// 返回文本格式的回答，直接作为 tool result 喂给模型。
func (c *Console) Ask(ctx *ga.Context, questions []*ask.Question) (string, error) {
	hasMultiSelect := false
	for _, q := range questions {
		if q.MultiSelect {
			hasMultiSelect = true
			break
		}
	}

	for _, q := range questions {
		title := q.Header
		if title == "" {
			title = q.Question
		}
		fmt.Fprintf(c.w, "\n%s", c.theme.paint(c.theme.MetaColor, "❓ "+title))
		if q.Header != "" {
			fmt.Fprintf(c.w, "\n%s", c.theme.paint(c.theme.MetaColor, "  "+q.Question))
		}
		fmt.Fprintln(c.w)
		for j, opt := range q.Options {
			fmt.Fprintf(c.w, "  %s", c.theme.paint(c.theme.PromptColor, fmt.Sprintf("%d.", j+1)))
			fmt.Fprintf(c.w, " %s", opt.Label)
			if opt.Description != "" {
				fmt.Fprintf(c.w, " (%s)", opt.Description)
			}
			fmt.Fprintln(c.w)
		}
	}

	// 提示输入格式
	switch {
	case len(questions) == 1 && !hasMultiSelect:
		fmt.Fprintf(c.w, "%s ", c.theme.paint(c.theme.PromptColor, ">"))
	case len(questions) == 1:
		fmt.Fprintf(c.w, "%s ", c.theme.paint(c.theme.PromptColor, "> (多选用逗号分隔)"))
	default:
		fmt.Fprintf(c.w, "%s ", c.theme.paint(c.theme.PromptColor, "> (多选用逗号分隔，多个问题用空格分隔)"))
	}

	line, err := c.lr.Read(ctx)
	if err != nil {
		return "", err
	}

	// 解析编号，回显问题 + 选中的选项文本。
	// 多个问题用空格分隔，多选用逗号分隔。
	parts := strings.Split(strings.TrimSpace(line), " ")
	var result []string
	for i, q := range questions {
		title := q.Header
		if title == "" {
			title = q.Question
		}
		var selected []string
		if i < len(parts) {
			for _, idx := range strings.Split(strings.TrimSpace(parts[i]), ",") {
				idx = strings.TrimSpace(idx)
				if idx == "" {
					continue
				}
				var n int
				if _, err := fmt.Sscanf(idx, "%d", &n); err == nil && n >= 1 && n <= len(q.Options) {
					selected = append(selected, q.Options[n-1].Label)
				}
			}
		}
		if len(selected) == 0 {
			selected = []string{q.Options[0].Label}
		}
		result = append(result, fmt.Sprintf("%s: %s", title, strings.Join(selected, ", ")))
	}
	return strings.Join(result, "\n"), nil
}

// Approve 实现 approve.Approver：请求审批并解析 "y"/"n"（可附备注）。
//
// 解析规则："y 看起来没问题" → 批准 + 备注；"n 太危险" → 拒绝 + 理由。
// 回车即默认拒绝，避免误放行危险操作。
func (c *Console) Approve(ctx context.Context, call *schema.ToolCall) (approve.Decision, error) {
	fmt.Fprintf(c.w, "\n%s\n%s ",
		c.theme.paint(c.theme.MetaColor, fmt.Sprintf("🔐 请求审批: %s(%s)", call.Function.Name, call.Function.Arguments)),
		c.theme.paint(c.theme.PromptColor, "批准? [y/N] >"))

	line, err := c.lr.Read(ctx)
	if err != nil {
		return approve.Decision{}, err
	}
	return parseDecision(line), nil
}

// parseDecision 解析 "y"、"n"、"y 备注"、"n 理由"。
func parseDecision(line string) approve.Decision {
	fields := strings.SplitN(line, " ", 2)
	yn := strings.ToLower(strings.TrimSpace(fields[0]))
	var reason string
	if len(fields) == 2 {
		reason = strings.TrimSpace(fields[1])
	}
	return approve.Decision{
		Approved: yn == "y" || yn == "yes",
		Reason:   reason,
	}
}
