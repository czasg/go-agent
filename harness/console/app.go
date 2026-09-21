package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema"
)

// 内置命令（以 / 开头）。
const (
	cmdHelp     = "/help"
	cmdClear    = "/clear"
	cmdExit     = "/exit"
	cmdQuit     = "/quit"
	cmdMessages = "/messages"
	cmdStats    = "/stats"
)

// defaultExitWords 是不带 / 前缀也能退出的词，保留 examples 里 "exit" 的习惯。
var defaultExitWords = []string{"exit", "quit", ":q"}

// App 是终端会话运行时：把一个 ga.Agent 包装成"能直接跑起来"的交互程序。
//
// 职责边界：App 掌管进程内的输入输出与生命周期，不碰业务数据。会话历史默认
// 放在内存里的 ga.Session——需要落库/多轮恢复时，用 app.Agent() 拿到底层
// Agent 自己管历史（框架里没有一行 DB 代码）。
type App struct {
	// ── 组装 Agent 的选项 ──
	agentOpts []ga.Option
	agent     *ga.Agent

	// ── 终端装配 ──
	console  *Console
	renderer Renderer
	theme    Theme

	in  io.Reader
	out io.Writer

	outExplicit   bool
	themeExplicit bool

	// ── 行为 ──
	quiet     bool
	exitWords []string
	toolNames []string // 注册的工具名（WithTools 时记录，/stats 用）

	// ── 运行时状态 ──
	session     *ga.Session
	totalUsage  schema.TokenUsage // 累计 token 用量
	turnCount   int               // 累计对话轮次

	sigCh chan os.Signal

	mu              sync.Mutex
	cancelTurn      context.CancelFunc
	turnActive      bool
	turnInterrupted bool
}

// Option 配置 App。
type Option func(*App)

// ── 组装 Agent（转发到 ga） ───────────────────────────────────

// WithAgent 绑定一个已构造好的 Agent。与 WithChatModel / WithTools 等转发选项互斥：
// 传了 WithAgent 就不要再传 ga 选项，否则 prepare 会 panic 或行为未定义。
// 典型用法：先独立组装 Agent，再 bind 进 harness。
func WithAgent(agent *ga.Agent) Option {
	return func(a *App) { a.agent = agent }
}

// WithChatModel 设置模型。
func WithChatModel(m llm.BaseModel) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithChatModel(m))
	}
}

// WithSystemPrompt 设置系统提示词。
func WithSystemPrompt(prompt string) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithSystemPrompt(prompt))
	}
}

// WithTools 注册工具。
func WithTools(tools ...ga.BaseTool) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithTools(tools...))
		for _, t := range tools {
			a.toolNames = append(a.toolNames, t.Info().Name)
		}
	}
}

// WithHooks 注册生命周期 hook（filesystem、skill、todos、summary...）。
func WithHooks(hooks ...ga.Hook) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithHooks(hooks...))
	}
}

// WithToolMiddlewares 注册工具中间件（日志、审批、限流...）。
func WithToolMiddlewares(mws ...ga.ToolMiddleware) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithToolMiddlewares(mws...))
	}
}

// WithModelMiddlewares 注册模型中间件（重试、计费...）。
func WithModelMiddlewares(mws ...ga.ModelMiddleware) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithModelMiddlewares(mws...))
	}
}

// WithMaxIterations 设置最大迭代次数。
func WithMaxIterations(n int) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithMaxIterations(n))
	}
}

// WithName / WithDescription 设置 Agent 身份（作为子智能体被 task 调用时用）。
func WithName(name string) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithName(name))
	}
}

// WithDescription 设置 Agent 描述。
func WithDescription(desc string) Option {
	return func(a *App) {
		a.agentOpts = append(a.agentOpts, ga.WithDescription(desc))
	}
}

// WithAgentOptions 是逃生舱：传入任意 ga.Option（harness 尚未包装的能力）。
// 注意：onEvent 由 harness 接管（用于驱动 Renderer），不要在这里重复设置。
func WithAgentOptions(opts ...ga.Option) Option {
	return func(a *App) { a.agentOpts = append(a.agentOpts, opts...) }
}

// ── 终端装配 ─────────────────────────────────────────────────

// WithRenderer 指定渲染器（默认 NewTerminalRenderer）。传 NopRenderer{} 可静默。
func WithRenderer(r Renderer) Option {
	return func(a *App) { a.renderer = r }
}

// WithConsole 指定终端交互入口（默认 NewConsole）。ask/approve 工具应与它共用
// 同一个 Console，否则又会出现多份 stdin reader 相互抢占。
func WithConsole(c *Console) Option {
	return func(a *App) { a.console = c }
}

// WithTheme 设置文案与配色。会影响默认构造的 Console 与 Renderer；
// 若显式传入了 Console/Renderer，以它们自身配置为准。
func WithTheme(t Theme) Option {
	return func(a *App) {
		a.theme = t
		a.themeExplicit = true
	}
}

// WithInput 指定输入流（默认 os.Stdin）。测试或从管道/文件喂输入时很有用。
func WithInput(r io.Reader) Option {
	return func(a *App) { a.in = r }
}

// WithOutput 指定输出流（默认 os.Stdout）。同时作用于默认 Console 与 Renderer。
func WithOutput(w io.Writer) Option {
	return func(a *App) {
		a.out = w
		a.outExplicit = true
	}
}

// WithQuiet 为 true 时不打印横幅与输入提示符，只输出渲染内容。
// 适合把程序接到管道、或作为更大 CLI 的子命令运行。
func WithQuiet(quiet bool) Option {
	return func(a *App) { a.quiet = quiet }
}

// WithExitWords 覆盖"不带 / 前缀也能退出"的词（默认 exit / quit / :q）。
// 传空列表表示只认 /exit 与 /quit。
func WithExitWords(words ...string) Option {
	return func(a *App) { a.exitWords = words }
}

// WithSignalChannel 注入信号通道（默认监听 os.Interrupt）。
// 主要用于测试：手动往通道里塞信号即可验证 Ctrl+C 语义。
func WithSignalChannel(ch chan os.Signal) Option {
	return func(a *App) { a.sigCh = ch }
}

// New 组装一个终端 App。未显式指定时，Console 读 os.Stdin、Renderer 写
// os.Stdout；Agent 用传入的 ga 选项构造，并把 Renderer 接到事件流上。
func New(opts ...Option) *App {
	a := &App{}
	for _, opt := range opts {
		opt(a)
	}
	a.prepare()
	return a
}

// prepare 在选项全部应用后做一次性的默认值解析。
func (a *App) prepare() {
	if !a.themeExplicit && a.console != nil {
		a.theme = a.console.Theme()
	}
	if !a.themeExplicit && a.theme.Labels.You == "" {
		a.theme = DefaultTheme()
	}
	a.theme = a.theme.withDefaults()

	if a.console == nil {
		consoleOpts := []ConsoleOption{WithConsoleTheme(a.theme)}
		if a.in != nil {
			consoleOpts = append(consoleOpts, WithConsoleInput(a.in))
		}
		if a.out != nil {
			consoleOpts = append(consoleOpts, WithConsoleOutput(a.out))
		}
		a.console = NewConsole(consoleOpts...)
	} else {
		// 调用方自带的 Console：把 App 级主题下发（除非它自己指定过主题），
		// 使 REPL 提示符、ask/approve 提问、渲染输出三者样式一致。
		a.console.applyTheme(a.theme)
	}
	if a.out == nil {
		a.out = a.console.Writer()
	}
	if a.renderer == nil {
		a.renderer = NewTerminalRenderer(WithRenderOutput(a.out), WithRenderTheme(a.theme))
	}
	if a.exitWords == nil {
		a.exitWords = append([]string{}, defaultExitWords...)
	}
	if a.agent == nil {
		agentOpts := make([]ga.Option, 0, len(a.agentOpts)+1)
		agentOpts = append(agentOpts, a.agentOpts...)
		agentOpts = append(agentOpts, ga.WithOnEvent(a.renderer.OnEvent))
		a.agent = ga.NewAgent(agentOpts...)
	} else {
		a.agent.SetOnEvent(a.renderer.OnEvent)
	}
	if a.session == nil {
		a.session = a.agent.NewSession()
	}
}

// ── 访问器 ───────────────────────────────────────────────────

// Agent 返回底层 Agent。需要自定义历史管理（落库、恢复、多租户）时从这里取。
func (a *App) Agent() *ga.Agent { return a.agent }

// Console 返回终端交互入口，可直接用于构造 ask / approve 工具。
func (a *App) Console() *Console { return a.console }

// Renderer 返回渲染器。
func (a *App) Renderer() Renderer { return a.renderer }

// Session 返回当前会话（保管跨轮历史）。
func (a *App) Session() *ga.Session { return a.session }

// Out 返回 App 的输出目标。
func (a *App) Out() io.Writer { return a.out }

// Theme 返回生效的主题。
func (a *App) Theme() Theme { return a.theme }

// ── 运行 ─────────────────────────────────────────────────────

// Run 进入交互循环，直到 /exit、退出词或输入流结束（EOF）。
// ctx 取消（如外部超时）同样结束循环，并返回 ctx.Err()。
func (a *App) Run(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	stop := a.watchSignals(cancelRun)
	defer stop()

	a.info(a.theme.Labels.Title)
	if mn, ok := a.agent.ChatModel().(interface{ ModelName() string }); ok && mn.ModelName() != "" {
		a.info(fmt.Sprintf("模型: %s", mn.ModelName()))
	}
	if !a.quiet && a.theme.Labels.Intro != "" {
		a.info(a.theme.Labels.Intro)
	}

	for {
		if err := runCtx.Err(); err != nil {
			// 空闲时 Ctrl+C：视作正常退出，不报错。
			if errors.Is(err, context.Canceled) {
				a.bye()
				return nil
			}
			return err
		}

		a.prompt()
		line, err := a.console.ReadLine(runCtx)
		switch {
		case errors.Is(err, ErrClosed):
			a.bye()
			return nil
		case errors.Is(err, context.Canceled):
			// 读输入时被 Ctrl+C 打断 → 退出。
			a.bye()
			return nil
		case err != nil:
			return err
		}

		if line == "" {
			continue
		}
		if !a.dispatch(runCtx, line) {
			a.bye()
			return nil
		}
	}
}

// Send 以编程方式跑一轮对话（不经过 REPL），返回本轮结果。
// 嵌入到更大的 CLI、或写端到端测试时用。
func (a *App) Send(ctx context.Context, input string) (*ga.RunResult, error) {
	a.session.AddUserMessage(input)
	return a.runTurn(ctx)
}

// dispatch 处理一行输入，返回 false 表示要退出循环。
func (a *App) dispatch(ctx context.Context, line string) bool {
	if strings.HasPrefix(line, "/") {
		switch strings.ToLower(strings.TrimSpace(line)) {
		case cmdExit, cmdQuit:
			return false
		case cmdClear:
			a.session = a.agent.NewSession()
			a.info(a.theme.Labels.Cleared)
			return true
		case cmdMessages:
			a.printMessages()
			return true
		case cmdStats:
			a.printStats()
			return true
		case cmdHelp:
			a.printHelp()
			return true
		default:
			a.info(fmt.Sprintf("%s: %s（输入 /help 查看可用命令）",
				a.theme.Labels.UnknownCommand, line))
			return true
		}
	}

	for _, w := range a.exitWords {
		if strings.EqualFold(line, w) {
			return false
		}
	}

	a.session.AddUserMessage(line)
	if _, err := a.runTurn(ctx); err != nil {
		// 单轮失败不应终止整个会话，渲染器已把错误展示出去。
		return true
	}
	return true
}

// runTurn 跑一轮 agent loop，并按结果调用渲染器的回调。
func (a *App) runTurn(ctx context.Context) (*ga.RunResult, error) {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.beginTurn(cancel)

	res, err := a.session.Run(turnCtx)

	interrupted := a.endTurn()
	switch {
	case interrupted:
		// 用户主动打断：还原颜色并给一句轻提示，不当作错误。
		fmt.Fprint(a.out, a.theme.reset())
		a.info(a.theme.Labels.Interrupted)
		return res, nil
	case err != nil:
		a.renderer.OnError(err)
		return res, err
	default:
		// RunLoop 把循环内部错误编码在 RunResult.Error 里（Go 返回值为 nil），
		// 这里统一检查并转为 OnError，让渲染器能感知模型报错等异常。
		if res != nil && res.Error != "" {
			runErr := fmt.Errorf("%s", res.Error)
			a.renderer.OnError(runErr)
			return res, runErr
		}
		a.renderer.OnResult(res)
		// 累计统计。
		if res != nil {
			a.totalUsage.Add(res.Usage)
			a.turnCount++
		}
		return res, nil
	}
}

// ── Ctrl+C 语义 ──────────────────────────────────────────────
//
// 设计目标（终端用户的肌肉记忆）：
//   - 一轮执行中按 Ctrl+C → 打断本轮，回到提示符，历史保留；
//   - 同一轮里再按一次     → 直接退出程序；
//   - 空闲（停在提示符）时按 → 退出程序。

func (a *App) watchSignals(cancelRun context.CancelFunc) func() {
	sigCh := a.sigCh
	stopSignal := func() {}
	if sigCh == nil {
		sigCh = make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt)
		stopSignal = func() { signal.Stop(sigCh) }
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sigCh:
				a.mu.Lock()
				active, already, cancel := a.turnActive, a.turnInterrupted, a.cancelTurn
				if active && !already {
					a.turnInterrupted = true
				}
				a.mu.Unlock()

				if active && !already && cancel != nil {
					cancel() // 首次：只打断当前轮
					continue
				}
				cancelRun() // 空闲或再次按下：退出
				return
			}
		}
	}()

	return func() {
		close(done)
		stopSignal()
	}
}

func (a *App) beginTurn(cancel context.CancelFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.turnActive = true
	a.turnInterrupted = false
	a.cancelTurn = cancel
}

// endTurn 返回本轮是否被 Ctrl+C 打断。
func (a *App) endTurn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	interrupted := a.turnInterrupted
	// 空闲态 Ctrl+C 会让本轮的 cancel 在下一次进入循环时才生效，
	// 这里统一复位，下一轮重新开始。
	a.turnActive = false
	a.turnInterrupted = false
	a.cancelTurn = nil
	return interrupted
}

// ── 输出 ─────────────────────────────────────────────────────

// info 打印 harness 自身提示。渲染器若实现了 InfoRenderer 就交给它，
// 否则回退到 App 自己的输出——这样自定义渲染器不必被迫实现提示功能。
func (a *App) info(msg string) {
	if msg == "" {
		return
	}
	if ir, ok := a.renderer.(InfoRenderer); ok {
		ir.OnInfo(msg)
		return
	}
	fmt.Fprintf(a.out, "\n%s\n", a.theme.paint(a.theme.MetaColor, msg))
}

func (a *App) prompt() {
	if a.quiet {
		return
	}
	fmt.Fprintf(a.out, "\n%s ", a.theme.paint(a.theme.PromptColor, a.theme.Labels.You+":"))
}

func (a *App) bye() {
	if a.theme.Labels.Bye != "" {
		a.info(a.theme.Labels.Bye)
	}
}

func (a *App) printHelp() {
	lines := []string{
		"可用命令：",
		"  /help      显示本帮助",
		"  /messages  查看当前会话的全部消息",
		"  /stats     查看累计 token 用量与会话信息",
		"  /clear     清空当前会话语境（历史不保留）",
		"  /exit      退出（/quit 同义）",
	}
	if len(a.exitWords) > 0 {
		lines = append(lines, "", "直接输入以下词也可退出："+strings.Join(a.exitWords, " / "))
	}
	lines = append(lines, "", "Ctrl+C：执行中按下打断本轮，空闲时按下退出。")
	a.info(strings.Join(lines, "\n"))
}

// printMessages 打印当前会话的全部消息（JSON 格式，便于调试）。
func (a *App) printMessages() {
	msgs := a.session.Messages()
	if len(msgs) == 0 {
		a.info("（会话为空）")
		return
	}
	data, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		a.info(fmt.Sprintf("序列化失败: %v", err))
		return
	}
	a.info(fmt.Sprintf("── 共 %d 条消息 ──\n%s", len(msgs), string(data)))
}

// printStats 打印累计统计信息。
func (a *App) printStats() {
	msgs := a.session.Messages()
	parts := []string{}
	if mn, ok := a.agent.ChatModel().(interface{ ModelName() string }); ok {
		parts = append(parts, fmt.Sprintf("模型: %s", mn.ModelName()))
	}
	parts = append(parts,
		fmt.Sprintf("对话轮次: %d", a.turnCount),
		fmt.Sprintf("消息总数: %d", len(msgs)),
		fmt.Sprintf("累计 tokens: %d (输入 %d / 输出 %d / 缓存命中 %d)",
			a.totalUsage.TotalTokens, a.totalUsage.PromptTokens,
			a.totalUsage.CompletionTokens, a.totalUsage.CachedTokens),
	)
	if len(a.toolNames) > 0 {
		parts = append(parts, fmt.Sprintf("注册工具: %s", strings.Join(a.toolNames, ", ")))
	}
	a.info(strings.Join(parts, "\n"))
}
