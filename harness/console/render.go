package console

// 本文件把 examples 里逐份复制的流式渲染逻辑收敛成一个终端渲染器。
//
// 原来的样板：每个 demo 都有一份几乎相同的 streamRenderer()（思考 / 正文 /
// 工具调用 / 工具结果 + oncePrint），差别只在文案和 emoji。harness 把它做成
// 一个可配置对象：文案、配色、显示开关都在 Theme / 选项里，业务侧不再复制。

import (
	"fmt"
	"io"
	"strings"
	"sync"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// DefaultMaxToolArgLen / DefaultMaxToolResultLen 是工具调用与结果的默认截断长度。
// 终端渲染的目标是"看得见过程"，完整内容会通过工具结果消息回喂模型，
// 因此这里截断只影响观感，不影响模型看到的信息。
const (
	DefaultMaxToolArgLen    = 500
	DefaultMaxToolResultLen = 300
)

// Renderer 是终端视图的统一入口。App 通过它消费事件、汇报结果。
//
// 拆成三个方法而不是只留 ga.OnEvent，是因为它们的信息来源不同：
//   - OnEvent 来自引擎流式事件（思考 / 正文 / 工具）；
//   - OnResult 来自 RunResult（tokens、耗时、迭代次数）；
//   - OnError 来自 Run 的 error 返回值（不在事件流里）。
//
// 想要静默模式用 NopRenderer，想自定义实现接口即可（比如接到 TUI / WebSocket）。
type Renderer interface {
	// OnEvent 消费一次引擎事件。签名与 ga.OnEvent 一致，可直接传给 ga.WithOnEvent。
	OnEvent(e ga.Event)
	// OnResult 在一轮 Run 正常结束后调用，用于打印统计。
	OnResult(r *ga.RunResult)
	// OnError 在一轮 Run 失败后调用。
	OnError(err error)
}

// InfoRenderer 是可选接口：渲染器若实现它，App 会把自身提示（启动横幅、/clear
// 回执、中断提示等）交给它统一排版。不实现则 App 用默认样式直接输出——
// 自定义渲染器不必被迫实现提示功能。
type InfoRenderer interface {
	OnInfo(msg string)
}

// ── TerminalRenderer ─────────────────────────────────────────

// streamState 是同一轮迭代内的一次性打印标记，避免重复打印小标题。
// 按 CallID 分别保存，使主会话与子智能体的流各自独立。
type streamState struct {
	reasoning bool // 思考头部已打印
	content   bool // 正文头部已打印
	result    bool // 工具结果头部已打印
}

// TerminalRenderer 把 ga 事件渲染成终端文本。
//
// 并发说明：工具是并发执行的，子智能体的 Context 又各有自己的事件锁，
// 因此事件回调可能并发到达，这里用 mu 串行化写操作，保证输出不交错。
type TerminalRenderer struct {
	mu    sync.Mutex
	w     io.Writer
	theme Theme

	maxArgLen    int
	maxResultLen int

	showReasoning bool // 是否打印思考过程
	showToolArgs  bool // 是否打印工具入参
	showToolCall  bool // 是否打印工具调用
	showToolRes   bool // 是否打印工具结果
	showStats     bool // 是否在每轮结束打印统计
	showSubAgent  bool // 是否打印子智能体标记

	states map[string]*streamState
}

var _ Renderer = (*TerminalRenderer)(nil)

// RenderOption 配置 TerminalRenderer。
type RenderOption func(*TerminalRenderer)

// WithRenderOutput 设置渲染器的输出目标（默认 os.Stdout）。
func WithRenderOutput(w io.Writer) RenderOption {
	return func(r *TerminalRenderer) { r.w = w }
}

// WithRenderTheme 设置主题（文案 + 配色）。
func WithRenderTheme(t Theme) RenderOption {
	return func(r *TerminalRenderer) { r.theme = t.withDefaults() }
}

// WithReasoning 控制是否展示思考过程（默认展示）。
func WithReasoning(show bool) RenderOption {
	return func(r *TerminalRenderer) { r.showReasoning = show }
}

// WithToolArgs 控制是否展示工具入参（默认展示）。
func WithToolArgs(show bool) RenderOption {
	return func(r *TerminalRenderer) { r.showToolArgs = show }
}

// WithToolResult 控制是否展示工具结果（默认展示）。
func WithToolResult(show bool) RenderOption {
	return func(r *TerminalRenderer) { r.showToolRes = show }
}

// WithStats 控制是否在每轮结束后打印 tokens / 耗时 / 迭代次数（默认展示）。
func WithStats(show bool) RenderOption {
	return func(r *TerminalRenderer) { r.showStats = show }
}

// WithTruncate 设置工具入参与结果的截断长度，<=0 表示不截断。
func WithTruncate(maxArgLen, maxResultLen int) RenderOption {
	return func(r *TerminalRenderer) {
		r.maxArgLen = maxArgLen
		r.maxResultLen = maxResultLen
	}
}

// NewTerminalRenderer 构造终端渲染器。
func NewTerminalRenderer(opts ...RenderOption) *TerminalRenderer {
	r := &TerminalRenderer{
		w:             stdout(),
		theme:         DefaultTheme(),
		maxArgLen:     DefaultMaxToolArgLen,
		maxResultLen:  DefaultMaxToolResultLen,
		showReasoning: true,
		showToolArgs:  true,
		showToolCall:  true,
		showToolRes:   true,
		showStats:     true,
		showSubAgent:  true,
		states:        make(map[string]*streamState),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// OnEvent 实现 ga.OnEvent。
func (r *TerminalRenderer) OnEvent(e ga.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sub := e.CallID != ""
	if sub && !r.showSubAgent {
		return
	}

	switch e.Type {
	case ga.IterationStartEvent:
		// 每轮迭代重置一次性标记，让新的一轮重新打印小标题。
		r.reset(e.CallID)
	case ga.MessageDeltaEvent:
		delta, ok := e.Data.(*schema.MessageDelta)
		if !ok || delta == nil {
			return
		}
		r.writeDelta(e.CallID, delta)
	case ga.MessageEndEvent:
		msg, ok := e.Data.(*schema.Message)
		if !ok || msg == nil {
			return
		}
		if !sub && r.showToolCall {
			r.writeToolCalls(msg.ToolCalls)
		}
	case ga.ToolEndEvent:
		if !sub && r.showToolRes {
			r.writeToolResult(e.Data)
		}
	case ga.AgentStartEvent:
		if sub {
			fmt.Fprintf(r.w, "\n%s\n", r.theme.paint(r.theme.SubAgentColor,
				fmt.Sprintf("%s [%s] ─┐", r.theme.Labels.SubAgentStart, shortID(e.CallID))))
		}
	case ga.AgentEndEvent:
		if sub {
			fmt.Fprintf(r.w, "%s\n", r.theme.paint(r.theme.SubAgentColor,
				fmt.Sprintf("%s [%s] ─┘", r.theme.Labels.SubAgentEnd, shortID(e.CallID))))
		}
	}
}

// OnResult 打印本轮统计。
func (r *TerminalRenderer) OnResult(res *ga.RunResult) {
	if res == nil || !r.showStats {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	parts := make([]string, 0, 5)
	if res.Model != "" {
		parts = append(parts, fmt.Sprintf("模型 %s", res.Model))
	}
	parts = append(parts, fmt.Sprintf("耗时 %dms", res.Timing.TotalDuration))
	parts = append(parts, fmt.Sprintf("tokens %d", res.Usage.TotalTokens))
	parts = append(parts, fmt.Sprintf("迭代 %d", res.Iterations))
	if res.StopReason != ga.StopCompleted {
		parts = append(parts, "结束原因 "+string(res.StopReason))
	}

	fmt.Fprintf(r.w, "\n%s\n", r.theme.paint(r.theme.MetaColor,
		fmt.Sprintf("[ %s ]", strings.Join(parts, " | "))))
}

// OnError 打印本轮错误。
func (r *TerminalRenderer) OnError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.w, "\n%s\n", r.theme.paint(r.theme.ErrorColor,
		fmt.Sprintf("%s: %v", r.theme.Labels.Error, err)))
}

// Info 打印 harness 自身的提示（/clear、中断、未知命令等）。
// 它不属于 Renderer 接口，但对外暴露，业务侧可以复用同一套配色打印自定义信息。
func (r *TerminalRenderer) Info(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.w, "\n%s\n", r.theme.paint(r.theme.MetaColor, msg))
}

// ── 内部渲染 ──────────────────────────────────────────────────

func (r *TerminalRenderer) state(callID string) *streamState {
	st, ok := r.states[callID]
	if !ok {
		st = &streamState{}
		r.states[callID] = st
	}
	return st
}

// reset 开启新一轮迭代，清掉一次性打印标记。
func (r *TerminalRenderer) reset(callID string) {
	r.states[callID] = &streamState{}
}

func (r *TerminalRenderer) writeDelta(callID string, delta *schema.MessageDelta) {
	st := r.state(callID)

	// 子智能体的流以暗色内联输出，不打断主会话的版式。
	if callID != "" {
		if delta.ReasoningContent != "" && r.showReasoning {
			fmt.Fprint(r.w, r.theme.paint(r.theme.ReasoningColor, delta.ReasoningContent))
		}
		if delta.Content != "" {
			fmt.Fprint(r.w, r.theme.paint(r.theme.MetaColor, delta.Content))
		}
		return
	}

	if delta.ReasoningContent != "" && r.showReasoning {
		if !st.reasoning {
			st.reasoning = true
			fmt.Fprintf(r.w, "\n%s\n%s", r.theme.paint(r.theme.MetaColor, r.theme.Labels.Thinking+":"),
				r.theme.paint(r.theme.ReasoningColor, ""))
		}
		fmt.Fprint(r.w, r.theme.paint(r.theme.ReasoningColor, delta.ReasoningContent))
	}
	if delta.Content != "" {
		if !st.content {
			st.content = true
			if st.reasoning {
				// 思考结束，收尾换行，正式正文另起一段。
				fmt.Fprintf(r.w, "%s\n", r.theme.reset())
			}
			fmt.Fprintf(r.w, "\n%s ", r.theme.paint(r.theme.ContentColor, r.theme.Labels.Assistant+":"))
		}
		fmt.Fprint(r.w, r.theme.paint(r.theme.ContentColor, delta.Content))
	}
}

func (r *TerminalRenderer) writeToolCalls(calls []*schema.ToolCall) {
	if len(calls) == 0 {
		return
	}
	fmt.Fprintf(r.w, "\n%s\n", r.theme.paint(r.theme.ToolColor, r.theme.Labels.ToolCall+":"))
	for _, call := range calls {
		if call == nil {
			continue
		}
		line := "  → " + call.Function.Name
		if r.showToolArgs {
			line += "(" + truncate(call.Args(), r.maxArgLen) + ")"
		}
		fmt.Fprintf(r.w, "%s\n", r.theme.paint(r.theme.ToolColor, line))
	}
}

func (r *TerminalRenderer) writeToolResult(data any) {
	call, ok := data.(*schema.ToolCall)
	if !ok || call == nil {
		return
	}
	st := r.state("")
	if !st.result {
		st.result = true
		fmt.Fprintf(r.w, "%s\n", r.theme.paint(r.theme.ToolResultColor, r.theme.Labels.ToolResult+":"))
	}
	body := truncate(call.Result, r.maxResultLen)
	label := call.Function.Name
	if call.Error != nil {
		body = "工具执行失败: " + call.Error.Error()
	}
	fmt.Fprintf(r.w, "%s\n", r.theme.paint(r.theme.ToolResultColor,
		fmt.Sprintf("  [%s] %s", label, body)))
}

// ── NopRenderer ──────────────────────────────────────────────

// NopRenderer 不做任何输出的渲染器，用于静默运行、测试、或把输出完全交给
// 业务自己处理（此时仍可只用 App 的 REPL + Session 管理）。
type NopRenderer struct{}

func (NopRenderer) OnEvent(ga.Event)       {}
func (NopRenderer) OnResult(*ga.RunResult) {}
func (NopRenderer) OnError(error)          {}

// ── 辅助 ──────────────────────────────────────────────────────

// shortID 把 CallID 截短，只用于子智能体标记的可读性。
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// truncate 按长度截断并追加省略标记。
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
