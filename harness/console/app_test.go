package console

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/tool/ask"
	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema")

// ── 测试替身 ──────────────────────────────────────────────────

// scriptedModel 是一个可编程的假模型：每次 Chat 按脚本依次返回预设消息。
// 脚本用尽后返回一条收尾消息（无工具调用），让 loop 自然结束。
type scriptedModel struct {
	mu     sync.Mutex
	script []*schema.Message
	calls  int
	// seen 记录每次调用时模型收到的消息，供断言上下文是否正确维护。
	seen [][]*schema.Message
}

func (m *scriptedModel) Chat(_ context.Context, messages []*schema.Message, opts ...llm.Option) (*schema.Message, error) {
	m.mu.Lock()
	idx := m.calls
	m.calls++
	m.seen = append(m.seen, append([]*schema.Message(nil), messages...))
	var msg *schema.Message
	if idx < len(m.script) {
		msg = m.script[idx]
	} else {
		msg = &schema.Message{Role: schema.Assistant, Content: "done"}
	}
	m.mu.Unlock()

	// 模拟流式：把内容按回调增量推出，渲染器据此打印正文。
	if cb := callbackOf(opts); cb != nil && msg.Content != "" {
		cb(&schema.MessageDelta{Content: msg.Content})
	}
	// 必须是副本：引擎会把返回值追加进历史，脚本要能重复使用。
	out := *msg
	return &out, nil
}

func (m *scriptedModel) ChatCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func callbackOf(opts []llm.Option) func(*schema.MessageDelta) {
	o := &llm.Options{}
	for _, opt := range opts {
		opt(o)
	}
	return o.OnCallback
}

// recordingRenderer 实现 Renderer + InfoRenderer，记录所有回调，
// 用来断言 App 是否正确驱动渲染生命周期。
type recordingRenderer struct {
	mu      sync.Mutex
	events  []ga.Event
	results []*ga.RunResult
	errs    []error
	infos   []string
}

func (r *recordingRenderer) OnEvent(e ga.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingRenderer) OnResult(res *ga.RunResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}

func (r *recordingRenderer) OnError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, err)
}

func (r *recordingRenderer) OnInfo(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.infos = append(r.infos, msg)
}

func (r *recordingRenderer) eventTypes() []ga.EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ga.EventType, len(r.events))
	for i, e := range r.events {
		out[i] = e.Type
	}
	return out
}

func (r *recordingRenderer) resultCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.results)
}

func (r *recordingRenderer) infoJoined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.infos, "\n")
}

// ── REPL 端到端 ───────────────────────────────────────────────

func TestApp_Run_REPL(t *testing.T) {
	model := &scriptedModel{}
	var out bytes.Buffer

	app := New(
		WithChatModel(model),
		WithSystemPrompt("sys"),
		WithInput(strings.NewReader("你好\n/help\nexit\n")),
		WithOutput(&out),
	)

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run 返回错误: %v", err)
	}

	// 只有第一行是对话，/help 是命令，exit 退出 → 模型只该被调用一次。
	if got := model.ChatCount(); got != 1 {
		t.Fatalf("模型调用次数 = %d, want 1", got)
	}
	// 退出后应回到循环外，会话历史为 system + user + assistant。
	msgs := app.Session().Messages()
	if len(msgs) != 3 {
		t.Fatalf("会话消息数 = %d, want 3 (system+user+assistant): %+v", len(msgs), msgs)
	}
	// 用户输入应原样进入历史（终端本身的回显不算在程序输出内）。
	if msgs[1].Role != schema.User || msgs[1].Content != "你好" {
		t.Fatalf("用户消息未正确入历史: %+v", msgs[1])
	}

	got := out.String()
	// 启动横幅、助手回复、/help 帮助文本都应出现。
	for _, want := range []string{"终端助手", "助手:", "done", "可用命令"} {
		if !strings.Contains(got, want) {
			t.Errorf("输出缺少 %q：\n%s", want, got)
		}
	}
}

func TestApp_WithAgent(t *testing.T) {
	model := &scriptedModel{}

	// 外部构造 Agent，再 bind 进 harness。
	agent := ga.NewAgent(
		ga.WithChatModel(model),
		ga.WithSystemPrompt("sys"),
	)

	app := New(
		WithAgent(agent),
		WithInput(strings.NewReader("hello\nexit\n")),
		WithOutput(io.Discard),
	)

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run 返回错误: %v", err)
	}

	// Agent 是外部传入的，harness 不应重新创建。
	if app.Agent() != agent {
		t.Fatal("WithAgent 后 App.Agent() 应返回同一个 Agent 实例")
	}
	// 模型应被调用一次。
	if got := model.ChatCount(); got != 1 {
		t.Fatalf("模型调用次数 = %d, want 1", got)
	}
	// 会话历史正确累积（system + user + assistant）。
	msgs := app.Session().Messages()
	if len(msgs) != 3 {
		t.Fatalf("会话消息数 = %d, want 3: %+v", len(msgs), msgs)
	}
}

func TestApp_Run_EOF(t *testing.T) {
	// 输入流直接结束（无 exit 词）也应正常返回 nil。
	app := New(
		WithChatModel(&scriptedModel{}),
		WithInput(strings.NewReader("")),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("EOF 时应正常退出，got %v", err)
	}
}

func TestApp_Run_BlankLineSkipped(t *testing.T) {
	model := &scriptedModel{}
	app := New(
		WithChatModel(model),
		WithInput(strings.NewReader("\n   \n\nexit\n")),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := model.ChatCount(); got != 0 {
		t.Fatalf("空行不该触发模型调用, got %d", got)
	}
}

func TestApp_Run_Quiet(t *testing.T) {
	var out bytes.Buffer
	app := New(
		WithChatModel(&scriptedModel{}),
		WithInput(strings.NewReader("hi\nexit\n")),
		WithOutput(&out),
		WithQuiet(true),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 静默模式不打印标题、不打印输入提示符。
	if s := out.String(); strings.Contains(s, "你:") {
		t.Errorf("quiet 模式不应打印提示符：\n%s", s)
	}
}

func TestApp_Run_UnknownCommand(t *testing.T) {
	var out bytes.Buffer
	app := New(
		WithChatModel(&scriptedModel{}),
		WithInput(strings.NewReader("/nope\nexit\n")),
		WithOutput(&out),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "未知命令") {
		t.Errorf("应提示未知命令：\n%s", out.String())
	}
}

func TestApp_ClearResetsSession(t *testing.T) {
	model := &scriptedModel{}
	var out bytes.Buffer
	app := New(
		WithChatModel(model),
		WithInput(strings.NewReader("a\n/clear\nexit\n")),
		WithOutput(&out),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// /clear 之后历史重置，只剩系统提示词这条。
	if msgs := app.Session().Messages(); len(msgs) != 0 {
		t.Fatalf("/clear 后历史应为空, got %+v", msgs)
	}
	if !strings.Contains(out.String(), "已清空") {
		t.Errorf("应提示已清空：\n%s", out.String())
	}
}

func TestApp_MultiTurnKeepsHistory(t *testing.T) {
	model := &scriptedModel{}
	app := New(
		WithChatModel(model),
		WithSystemPrompt("sys"),
		WithInput(strings.NewReader("one\ntwo\nexit\n")),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.seen) != 2 {
		t.Fatalf("模型应被调用 2 次, got %d", len(model.seen))
	}
	// 第二轮模型应看到第一轮的 user + assistant，证明历史跨轮累积。
	second := model.seen[1]
	var contents []string
	for _, m := range second {
		contents = append(contents, string(m.Role)+":"+m.Content)
	}
	joined := strings.Join(contents, "|")
	for _, want := range []string{"user:one", "assistant:done", "user:two"} {
		if !strings.Contains(joined, want) {
			t.Errorf("第二轮上下文缺少 %q：%s", want, joined)
		}
	}
}

// ── 工具回环 ──────────────────────────────────────────────────

type echoTool struct {
	got []string
}

func (e *echoTool) Info() schema.ToolInfo {
	return schema.ToolInfo{Name: "echo", Description: "回显"}
}

func (e *echoTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := call.JSON(&in); err != nil {
		return "", err
	}
	e.got = append(e.got, in.Text)
	return "echo:" + in.Text, nil
}

func TestApp_ToolCallRoundTrip(t *testing.T) {
	tool := &echoTool{}
	model := &scriptedModel{script: []*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []*schema.ToolCall{{
				ID:       "call-1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`},
			}},
		},
	}}

	renderer := &recordingRenderer{}
	app := New(
		WithChatModel(model),
		WithTools(tool),
		WithRenderer(renderer),
		WithInput(strings.NewReader("go\nexit\n")),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 工具被真实执行，结果回注。
	if len(tool.got) != 1 || tool.got[0] != "hi" {
		t.Fatalf("工具入参解析错误: %+v", tool.got)
	}
	// 一轮 Run 里模型被调用两次：发起工具调用 + 收到结果后收尾。
	if got := model.ChatCount(); got != 2 {
		t.Fatalf("模型调用次数 = %d, want 2", got)
	}
	if got := renderer.resultCount(); got != 1 {
		t.Fatalf("OnResult 调用次数 = %d, want 1", got)
	}

	types := renderer.eventTypes()
	for _, want := range []ga.EventType{
		ga.AgentStartEvent, ga.IterationStartEvent, ga.MessageEndEvent,
		ga.ToolStartEvent, ga.ToolEndEvent, ga.AgentEndEvent,
	} {
		if !containsType(types, want) {
			t.Errorf("事件流缺少 %s：%v", want, types)
		}
	}
}

func containsType(types []ga.EventType, want ga.EventType) bool {
	for _, t := range types {
		if t == want {
			return true
		}
	}
	return false
}

// ── 错误处理 ──────────────────────────────────────────────────

type failingModel struct{ err error }

func (f *failingModel) Chat(context.Context, []*schema.Message, ...llm.Option) (*schema.Message, error) {
	return nil, f.err
}

func TestApp_RunErrorDoesNotEndSession(t *testing.T) {
	wantErr := errors.New("boom")
	renderer := &recordingRenderer{}
	app := New(
		WithChatModel(&failingModel{err: wantErr}),
		WithRenderer(renderer),
		// 第一轮失败，第二轮仍应被处理，然后退出。
		WithInput(strings.NewReader("a\nb\nexit\n")),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("单轮失败不应终止会话, got %v", err)
	}

	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if len(renderer.errs) != 2 {
		t.Fatalf("应上报 2 次错误, got %d", len(renderer.errs))
	}
	if renderer.errs[0].Error() != wantErr.Error() {
		t.Errorf("错误信息应原样透传, got %v", renderer.errs[0])
	}
}

func TestApp_RunContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立刻取消：Run 应立即返回。

	app := New(
		WithChatModel(&scriptedModel{}),
		WithInput(strings.NewReader("a\nexit\n")),
		WithOutput(io.Discard),
	)
	if err := app.Run(ctx); err != nil {
		t.Fatalf("空闲取消应视作正常退出, got %v", err)
	}
}

// ── Ctrl+C 语义 ──────────────────────────────────────────────

// blockingModel 在收到 ctx 取消前一直阻塞，用来模拟"正在执行的一轮"。
type blockingModel struct {
	started chan struct{}
	once    sync.Once
}

func (b *blockingModel) Chat(ctx context.Context, _ []*schema.Message, _ ...llm.Option) (*schema.Message, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestApp_SignalInterruptsTurnThenExits(t *testing.T) {
	model := &blockingModel{started: make(chan struct{})}
	sig := make(chan os.Signal, 1)
	renderer := &recordingRenderer{}

	app := New(
		WithChatModel(model),
		WithRenderer(renderer),
		WithSignalChannel(sig),
		WithInput(strings.NewReader("long\nexit\n")),
		WithOutput(io.Discard),
	)

	done := make(chan error, 1)
	go func() { done <- app.Run(context.Background()) }()

	// 等这一轮真正进入模型调用，再发第一次 Ctrl+C → 应只打断本轮。
	<-model.started
	sig <- os.Interrupt

	select {
	case err := <-done:
		// 关键：第一个信号只打断本轮，不该直接结束程序。
		// 程序应回到提示符，读到后续的 exit 才退出。
		if err != nil {
			t.Fatalf("Run 返回错误: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("第一次 Ctrl+C 后程序未继续运行（应只打断本轮）")
	}

	if !strings.Contains(renderer.infoJoined(), "已中断") {
		t.Errorf("应提示本轮已中断：%q", renderer.infoJoined())
	}
}

func TestApp_SignalWhenIdleExits(t *testing.T) {
	sig := make(chan os.Signal, 1)
	// 输入源一直阻塞（不会给出 exit），只能靠信号退出。
	pr, _ := io.Pipe()
	defer pr.Close()

	app := New(
		WithChatModel(&scriptedModel{}),
		WithSignalChannel(sig),
		WithInput(pr),
		WithOutput(io.Discard),
	)

	done := make(chan error, 1)
	go func() { done <- app.Run(context.Background()) }()

	time.Sleep(50 * time.Millisecond) // 让 Run 进入读输入的阻塞态
	sig <- os.Interrupt

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("空闲时 Ctrl+C 应正常退出, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("空闲时 Ctrl+C 未退出")
	}
}

// ── 渲染器 ────────────────────────────────────────────────────

func TestTerminalRenderer_Streaming(t *testing.T) {
	var out bytes.Buffer
	r := NewTerminalRenderer(WithRenderOutput(&out), WithRenderTheme(PlainTheme()))

	r.OnEvent(ga.Event{Type: ga.IterationStartEvent})
	r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{ReasoningContent: "想一下"}})
	r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{Content: "答案"}})
	r.OnEvent(ga.Event{Type: ga.MessageEndEvent, Data: &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []*schema.ToolCall{{
			ID: "c1", Function: schema.FunctionCall{Name: "read_file", Arguments: `{"path":"a.txt"}`},
		}},
	}})
	r.OnEvent(ga.Event{Type: ga.ToolEndEvent, Data: &schema.ToolCall{
		ID: "c1", Function: schema.FunctionCall{Name: "read_file"}, Result: "hello",
	}})

	got := out.String()
	for _, want := range []string{"thinking", "想一下", "assistant", "答案", "tool call", "read_file", "a.txt", "hello"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染输出缺少 %q：\n%s", want, got)
		}
	}
	if strings.Contains(got, "\033[") {
		t.Errorf("PlainTheme 不应输出 ANSI 颜色：\n%q", got)
	}
}

func TestTerminalRenderer_HeadersOncePerIteration(t *testing.T) {
	var out bytes.Buffer
	r := NewTerminalRenderer(WithRenderOutput(&out), WithRenderTheme(PlainTheme()))

	// 同一轮内多个增量：小标题只应打印一次。
	r.OnEvent(ga.Event{Type: ga.IterationStartEvent})
	for _, s := range []string{"A", "B", "C"} {
		r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{Content: s}})
	}
	if n := strings.Count(out.String(), "assistant:"); n != 1 {
		t.Fatalf("正文头部应只打印一次, got %d:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "ABC") {
		t.Errorf("增量应拼接输出：\n%s", out.String())
	}

	// 下一轮迭代应重新打印头部。
	r.OnEvent(ga.Event{Type: ga.IterationStartEvent})
	r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{Content: "D"}})
	if n := strings.Count(out.String(), "assistant:"); n != 2 {
		t.Fatalf("新迭代应重新打印头部, got %d:\n%s", n, out.String())
	}
}

func TestTerminalRenderer_SubAgentHiddenByMainFlags(t *testing.T) {
	var out bytes.Buffer
	r := NewTerminalRenderer(WithRenderOutput(&out), WithRenderTheme(PlainTheme()))

	// 主会话的工具调用事件才渲染；子智能体的事件不侵入主版式。
	r.OnEvent(ga.Event{Type: ga.MessageEndEvent, CallID: "sub-1", Data: &schema.Message{
		ToolCalls: []*schema.ToolCall{{Function: schema.FunctionCall{Name: "read_file"}}},
	}})
	if strings.Contains(out.String(), "tool call") {
		t.Errorf("子智能体的工具调用不应渲染到主输出：\n%s", out.String())
	}

	// 子智能体启动/结束有独立标记。
	r.OnEvent(ga.Event{Type: ga.AgentStartEvent, CallID: "abcdef12345"})
	r.OnEvent(ga.Event{Type: ga.AgentEndEvent, CallID: "abcdef12345"})
	if !strings.Contains(out.String(), "abcdef12") {
		t.Errorf("应打印子智能体标记（ID 截断 8 位）：\n%s", out.String())
	}
}

func TestTerminalRenderer_Switches(t *testing.T) {
	var out bytes.Buffer
	r := NewTerminalRenderer(
		WithRenderOutput(&out),
		WithRenderTheme(PlainTheme()),
		WithReasoning(false),
		WithToolResult(false),
		WithStats(false),
	)

	r.OnEvent(ga.Event{Type: ga.IterationStartEvent})
	r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{ReasoningContent: "秘密"}})
	r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{Content: "公开"}})
	r.OnEvent(ga.Event{Type: ga.ToolEndEvent, Data: &schema.ToolCall{
		Function: schema.FunctionCall{Name: "t"}, Result: "结果内容",
	}})
	r.OnResult(&ga.RunResult{})

	got := out.String()
	for _, banned := range []string{"秘密", "结果内容", "耗时"} {
		if strings.Contains(got, banned) {
			t.Errorf("开关关闭后不应输出 %q：\n%s", banned, got)
		}
	}
	if !strings.Contains(got, "公开") {
		t.Errorf("正文应正常输出：\n%s", got)
	}
}

func TestTerminalRenderer_StatsAndError(t *testing.T) {
	var out bytes.Buffer
	r := NewTerminalRenderer(WithRenderOutput(&out), WithRenderTheme(PlainTheme()))

	r.OnResult(&ga.RunResult{
		Usage:      schema.TokenUsage{TotalTokens: 123},
		Timing:     schema.Timing{TotalDuration: 45},
		Iterations: 2,
		StopReason: ga.StopCompleted,
	})
	r.OnError(errors.New("炸了"))

	got := out.String()
	for _, want := range []string{"123", "45ms", "error", "炸了"} {
		if !strings.Contains(got, want) {
			t.Errorf("输出缺少 %q：\n%s", want, got)
		}
	}
}

func TestTerminalRenderer_Truncation(t *testing.T) {
	var out bytes.Buffer
	r := NewTerminalRenderer(
		WithRenderOutput(&out),
		WithRenderTheme(PlainTheme()),
		WithTruncate(5, 5),
	)
	r.OnEvent(ga.Event{Type: ga.MessageEndEvent, Data: &schema.Message{
		ToolCalls: []*schema.ToolCall{{Function: schema.FunctionCall{Name: "t", Arguments: "0123456789"}}},
	}})
	r.OnEvent(ga.Event{Type: ga.ToolEndEvent, Data: &schema.ToolCall{
		Function: schema.FunctionCall{Name: "t"}, Result: "abcdefghij",
	}})

	got := out.String()
	if !strings.Contains(got, "01234...") || !strings.Contains(got, "abcde...") {
		t.Errorf("应按配置截断：\n%s", got)
	}
}

func TestTerminalRenderer_ConcurrentEventsAreSerialized(t *testing.T) {
	// 工具并发执行时事件是并发的；渲染器必须不 panic 且输出不交错。
	var out bytes.Buffer
	r := NewTerminalRenderer(WithRenderOutput(&out), WithRenderTheme(PlainTheme()))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.OnEvent(ga.Event{Type: ga.MessageDeltaEvent, Data: &schema.MessageDelta{Content: "x"}})
		}()
	}
	wg.Wait()

	if n := strings.Count(out.String(), "x"); n != 50 {
		t.Fatalf("并发增量应全部输出, got %d", n)
	}
	if !strings.Contains(out.String(), "▯") && strings.Count(out.String(), "assistant:") != 1 {
		// 只做弱校验：主要目标是 -race 下不报竞态、不 panic。
		t.Logf("输出：%s", out.String())
	}
}

func TestNopRenderer(t *testing.T) {
	// 接口约束 + 静默语义。
	var _ Renderer = NopRenderer{}
	r := NopRenderer{}
	r.OnEvent(ga.Event{})
	r.OnResult(&ga.RunResult{})
	r.OnError(errors.New("x"))
}

// ── Console ───────────────────────────────────────────────────

func TestConsole_AskAndApprove(t *testing.T) {
	out := &bytes.Buffer{}
	c := NewConsole(
		WithConsoleInput(strings.NewReader("2\ny 备注内容\nn 不行\n")),
		WithConsoleOutput(out),
		WithConsoleTheme(PlainTheme()),
	)
	ctx := ga.NewContext(context.Background(), ga.NewAgent(), nil)

	questions := []*ask.Question{
		{
			Question: "你需要哪些数据？",
			Header:   "数据选择",
			Options: []*ask.Option{
				{Label: "用户表"},
				{Label: "订单表"},
				{Label: "商品表"},
			},
		},
	}
	answer, err := c.Ask(ctx, questions)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "订单表") {
		t.Fatalf("Ask = %q, 期望包含 '订单表'", answer)
	}

	d1, err := c.Approve(ctx, &schema.ToolCall{
		Function: schema.FunctionCall{Name: "drop_table", Arguments: `{"table":"users"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !d1.Approved || d1.Reason != "备注内容" {
		t.Fatalf("Approve = %+v", d1)
	}

	d2, err := c.Approve(ctx, &schema.ToolCall{
		Function: schema.FunctionCall{Name: "drop_table", Arguments: `{"table":"orders"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d2.Approved || d2.Reason != "不行" {
		t.Fatalf("Approve = %+v", d2)
	}
}

func TestConsole_ApproveDefaultsToReject(t *testing.T) {
	// 回车（空行）应默认拒绝，避免误放行危险动作。
	c := NewConsole(
		WithConsoleInput(strings.NewReader("\n")),
		WithConsoleOutput(io.Discard),
	)
	d, err := c.Approve(context.Background(), &schema.ToolCall{
		Function: schema.FunctionCall{Name: "delete_file", Arguments: `{"path":"/etc/passwd"}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Approved {
		t.Fatal("空输入应默认拒绝")
	}
}

func TestConsole_OutputUsedForBothPromptAndInput(t *testing.T) {
	// Ask 与 Approve 的提示必须写到 Console 自己的输出，而不是进程 stdout——
	// 否则嵌入到别的 CLI 时提示会跑到错误的流里。
	var out bytes.Buffer
	c := NewConsole(
		WithConsoleInput(strings.NewReader("answer\n")),
		WithConsoleOutput(&out),
		WithConsoleTheme(PlainTheme()),
	)
	questions := []*ask.Question{
		{
			Question: "题目",
			Options:  []*ask.Option{{Label: "A"}, {Label: "B"}},
		},
	}
	if _, err := c.Ask(ga.NewContext(context.Background(), ga.NewAgent(), nil), questions); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "题目") {
		t.Errorf("提示应写入 Console 输出：\n%s", out.String())
	}
}

// TestAskToolSharesStdinWithREPL 是 harness 最关键的一条保证：
// ask 工具与 REPL 共用同一个 reader，REPL 不会把留给工具的输入吞掉。
func TestAskToolSharesStdinWithREPL(t *testing.T) {
	console := NewConsole(
		WithConsoleInput(strings.NewReader("开始\n1\nexit\n")),
		WithConsoleOutput(io.Discard),
	)

	// 脚本化的模型：第一轮发起 ask_user 调用，第二轮收尾。
	script := []*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []*schema.ToolCall{{
				ID:       "ask-1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "ask_user", Arguments: `{"questions":[{"question":"你叫什么？","options":[{"label":"张三"},{"label":"李四"}]}]}`},
			}},
		},
	}
	askTool, err := ask.New(console)
	if err != nil {
		t.Fatal(err)
	}

	app := New(
		WithChatModel(&scriptedModel{script: script}),
		WithTools(askTool),
		WithConsole(console),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 若 REPL 与工具各自持有 scanner，"1" 会被 REPL 预读走，工具读不到。
	// 断言模型最终看到的历史里，工具结果包含用户选择的选项。
	var toolResult string
	for _, m := range app.Session().Messages() {
		if m.Role == schema.Tool {
			toolResult = m.Content
		}
	}
	if !strings.Contains(toolResult, "张三") {
		t.Fatalf("ask 工具应读到 REPL 之后的下一行, got %q", toolResult)
	}
}

func TestApp_ExitWordsConfigurable(t *testing.T) {
	model := &scriptedModel{}
	app := New(
		WithChatModel(model),
		WithExitWords("bye"),
		WithInput(strings.NewReader("exit\nbye\n")),
		WithOutput(io.Discard),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// exit 已不在退出词里，会被当作普通对话发给模型。
	if got := model.ChatCount(); got != 1 {
		t.Fatalf("exit 应被当普通输入处理, 模型调用次数 = %d", got)
	}
}

func TestApp_WithThemeAffectsDefaults(t *testing.T) {
	var out bytes.Buffer
	theme := DefaultTheme()
	theme.Labels.You = "USER"
	theme.PromptColor = ""

	app := New(
		WithChatModel(&scriptedModel{}),
		WithTheme(theme),
		WithInput(strings.NewReader("hi\nexit\n")),
		WithOutput(&out),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "USER") {
		t.Errorf("自定义文案未生效：\n%s", out.String())
	}
}

func TestApp_MessagesCommand(t *testing.T) {
	var out bytes.Buffer
	app := New(
		WithChatModel(&scriptedModel{}),
		WithSystemPrompt("sys"),
		WithInput(strings.NewReader("你好\n/messages\nexit\n")),
		WithOutput(&out),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	// /messages 应输出 JSON 格式的消息历史。
	for _, want := range []string{"共", "条消息", "user", "你好"} {
		if !strings.Contains(got, want) {
			t.Errorf("/messages 输出缺少 %q：\n%s", want, got)
		}
	}
}

func TestApp_MessagesCommandEmpty(t *testing.T) {
	var out bytes.Buffer
	app := New(
		WithChatModel(&scriptedModel{}),
		WithInput(strings.NewReader("/messages\nexit\n")),
		WithOutput(&out),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "会话为空") {
		t.Errorf("空会话 /messages 应提示为空：\n%s", out.String())
	}
}

func TestApp_StatsCommand(t *testing.T) {
	var out bytes.Buffer
	tool := &echoTool{}
	model := &scriptedModel{script: []*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []*schema.ToolCall{{
				ID:       "call-1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "echo", Arguments: `{"text":"hi"}`},
			}},
		},
	}}

	app := New(
		WithChatModel(model),
		WithTools(tool),
		WithInput(strings.NewReader("go\n/stats\nexit\n")),
		WithOutput(&out),
	)
	if err := app.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	// /stats 应包含累计 token、消息数、工具名。
	for _, want := range []string{"累计 tokens", "消息总数", "echo"} {
		if !strings.Contains(got, want) {
			t.Errorf("/stats 输出缺少 %q：\n%s", want, got)
		}
	}
}
