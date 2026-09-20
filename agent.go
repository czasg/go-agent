package ga

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema"
)

// Agent 是不可变的配置载体：model、tools、hooks、中间件、systemPrompt。
// 一旦构造完成，Run 期间不会修改它，因此同一个 Agent 可以被多个会话、
// 多个 goroutine 并发复用。所有"本次运行的可变状态"都在 Context 里。
type Agent struct {
	name          string // 智能体名称（call_agent 工具发现用）
	description   string // 智能体描述（暴露给主模型看）
	chatModel     llm.BaseModel
	maxIterations int
	systemPrompt  string
	toolStore     *ToolStore

	// 两套中间件（洋葱），gin.Use 式全局注册。
	toolMiddlewares  []ToolMiddleware
	modelMiddlewares []ModelMiddleware

	// 六个生命周期 hook（切面：拦截/改状态，控制流走 c.Abort）。
	agentStartHooks     []AgentStartHook
	agentEndHooks       []AgentEndHook
	iterationStartHooks []IterationStartHook
	messageDeltaHooks   []MessageDeltaHook
	messageEndHooks     []MessageEndHook
	iterationEndHooks   []IterationEndHook

	// onEvent 观察回调（落库、渲染、日志、流式输出）。与 hook 平级，只观察不改状态。
	onEvent OnEvent

	// getSteeringMessages 在每轮迭代结束后被调用，返回需要注入的用户消息。
	// 返回 nil/空表示没有插队消息，loop 正常继续。
	// 这是纯配置（函数指针），不破坏 Agent 的不可变性——队列由调用方持有。
	getSteeringMessages func() []*schema.Message
}

func NewAgent(opts ...Option) *Agent {
	a := &Agent{
		maxIterations: 64,
		toolStore:     NewToolStore(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Name 返回智能体名称。
func (a *Agent) Name() string { return a.name }

// Description 返回智能体描述。
func (a *Agent) Description() string { return a.description }

// ChatModel 返回 Agent 使用的聊天模型。
func (a *Agent) ChatModel() llm.BaseModel { return a.chatModel }

// ── Option 配置 ──────────────────────────────────────────────

type Option func(*Agent)

// WithChatModel 设置 Agent 使用的聊天模型。
func WithChatModel(m llm.BaseModel) Option {
	return func(a *Agent) { a.chatModel = m }
}

// WithName 设置智能体名称（call_agent 工具发现用）。
func WithName(name string) Option {
	return func(a *Agent) { a.name = name }
}

// WithDescription 设置智能体描述（暴露给主模型看）。
func WithDescription(desc string) Option {
	return func(a *Agent) { a.description = desc }
}

// WithMaxIterations 设置最大迭代次数（默认 64）。
// 每轮迭代包含一次模型调用 + 若干工具执行，达到上限后 loop 以 StopMaxIteration 终止。
func WithMaxIterations(n int) Option {
	return func(a *Agent) { a.maxIterations = n }
}

// WithSystemPrompt 设置系统提示词（Run 时若历史中无 system 消息则注入到最前）。
func WithSystemPrompt(prompt string) Option {
	return func(a *Agent) { a.systemPrompt = prompt }
}

// WithTools 批量注册工具（构造期）。
func WithTools(tools ...BaseTool) Option {
	return func(a *Agent) {
		for _, t := range tools {
			a.toolStore.Register(t)
		}
	}
}

// WithToolMiddlewares 注册工具中间件（对每一次工具调用统一生效）。
func WithToolMiddlewares(mws ...ToolMiddleware) Option {
	return func(a *Agent) { a.toolMiddlewares = append(a.toolMiddlewares, mws...) }
}

// WithModelMiddlewares 注册模型中间件（对每一次模型调用统一生效）。
func WithModelMiddlewares(mws ...ModelMiddleware) Option {
	return func(a *Agent) { a.modelMiddlewares = append(a.modelMiddlewares, mws...) }
}

// WithOnEvent 设置观察回调（落库、渲染、日志）。
// 与 hook 平级：hook 拦截/改状态，onEvent 只观察执行，不应修改状态。
func WithOnEvent(fn OnEvent) Option {
	return func(a *Agent) { a.onEvent = fn }
}

// SetOnEvent 在构造后设置观察回调。供 harness 绑定渲染器时使用。
func (a *Agent) SetOnEvent(fn OnEvent) { a.onEvent = fn }

// WithSteeringMessages 设置消息插队回调。
//
// 每轮迭代结束（工具执行完毕）后，引擎调用此回调。若返回非空消息列表，
// 这些消息会注入到对话历史中，模型在下一次响应时即可看到。
//
// fn 的典型实现：harness 持有一个 channel 收集用户在 turn 期间输入的消息，
// fn 负责非阻塞 drain 该 channel 并返回 []*schema.Message。
// 这样队列是 harness 的内部状态，Agent 只持有一个函数指针（纯配置）。
func WithSteeringMessages(fn func() []*schema.Message) Option {
	return func(a *Agent) { a.getSteeringMessages = fn }
}

// WithHooks 批量注册钩子，每个钩子只需实现它关心的接口即可。
func WithHooks(hooks ...Hook) Option {
	return func(a *Agent) {
		for _, h := range hooks {
			if h == nil {
				continue
			}
			if v, ok := h.(AgentStartHook); ok {
				a.agentStartHooks = append(a.agentStartHooks, v)
			}
			if v, ok := h.(AgentEndHook); ok {
				a.agentEndHooks = append(a.agentEndHooks, v)
			}
			if v, ok := h.(IterationStartHook); ok {
				a.iterationStartHooks = append(a.iterationStartHooks, v)
			}
			if v, ok := h.(MessageDeltaHook); ok {
				a.messageDeltaHooks = append(a.messageDeltaHooks, v)
			}
			if v, ok := h.(MessageEndHook); ok {
				a.messageEndHooks = append(a.messageEndHooks, v)
			}
			if v, ok := h.(IterationEndHook); ok {
				a.iterationEndHooks = append(a.iterationEndHooks, v)
			}
		}
	}
}

// ── Run：无状态执行 ──────────────────────────────────────────
//
// Run 是整个框架的核心契约：进去一份 history，出来一份完整的 RunResult，
// 中间不碰任何 Agent 上的可变状态。正因为无状态，它天然支持：
//   - 并发/复用（同一 Agent 多处同时 Run 互不干扰）；
//   - 多轮对话（调用方保管历史，下一轮把新历史传进来）；
//   - 挂起-恢复（Run 返回后落库、进程可退出，之后带更多历史再 Run）。
//
// 控制流只有一个来源：Context。外部取消走 ctx.Done()；框架内部主动终止走
// hook/tool 调用的 c.Abort()。返回值里的 error 只表达"Run 本身失败"。
func (a *Agent) Run(ctx context.Context, history []*schema.Message) (*schema.RunResult, error) {
	c := NewContext(ctx, a, history)
	return RunLoop(c)
}

// RunLoop 以已构建的 Context 跑一轮完整的 agent loop（model ↔ tool 循环）。
// 大多数场景用 Agent.Run 即可；需要精细控制 Context 时（如 task 子循环）用这个。
func RunLoop(c *Context) (*schema.RunResult, error) {
	a := c.Agent

	c.Emit(AgentStartEvent, nil)
	if r, done := a.runAgentStartHooks(c); done {
		return r, nil
	}

	modelHandler := chainModel(modelAsHandler(a.chatModel), a.modelMiddlewares...)

	for ; c.Iteration < a.maxIterations; c.Iteration++ {
		// 清除上一轮的瞬态消息，保证模型不会看到过期的临时状态。
		c.Messages.ClearTransient()
		// 修补 tool_call/tool 结果不匹配（补缺、删孤）。
		c.Messages.Fix()

		// 边界：外部取消？
		if err := c.Err(); err != nil {
			return a.buildRunResult(c, schema.StopCancelled, err), err
		}

		c.Emit(IterationStartEvent, c.Iteration)
		if r, done := a.runIterationStartHooks(c); done {
			return r, nil
		}

		// 构建 llm options：工具列表 + 流式回调（喂给 onEvent 观察 + delta hook 控制）。
		var opts []llm.Option
		if infos := c.Tools.ListToolInfos(); len(infos) > 0 {
			opts = append(opts, llm.WithTools(infos))
		}
		if a.onEvent != nil || len(a.messageDeltaHooks) > 0 {
			opts = append(opts, llm.WithCallback(func(delta *schema.MessageDelta) {
				c.Emit(MessageDeltaEvent, delta)
				a.runMessageDeltaHooks(c, delta)
			}))
		}

		// 调模型（过 model 中间件链）。
		msg, err := modelHandler(c, c.Messages.Messages(), opts...)
		if err != nil {
			c.AbortWithError(err)
			break
		}
		c.usage.Add(msg.Usage)

		c.Message = msg
		c.Messages.AddMessage(msg)
		c.Emit(MessageEndEvent, msg)

		if r, done := a.runMessageEndHooks(c); done {
			return r, nil
		}

		// 消息携带错误（如 max_tokens 截断）→ 提前终止，不执行工具。
		if msg.Error != "" {
			c.AbortWithError(fmt.Errorf("%s", msg.Error))
			break
		}

		// 模型没有发起工具调用 → 对话自然结束。
		if len(msg.ToolCalls) == 0 {
			break
		}

		// ── 工具调用阶段（并发执行）──
		a.runTools(c, msg.ToolCalls)

		// 工具可能在执行中调用了 c.Abort()（并发安全，已加锁）。
		// 已产出的结果先回注历史，再统一判定终止——不丢任何工具结果。
		for _, call := range msg.ToolCalls {
			if call.Result == "" {
				continue
			}
			c.Messages.AddMessage(&schema.Message{
				Role:       schema.Tool,
				Content:    call.Result,
				ToolCallID: call.ID,
				ToolName:   call.Function.Name,
			})
		}
		if r, done := a.checkStop(c); done {
			return r, nil
		}

		if r, done := a.runIterationEndHooks(c); done {
			return r, nil
		}
		c.Emit(IterationEndEvent, c.Iteration)

		// 插队：回调返回的用户消息追加到历史，下一次模型调用即可看到。
		if a.getSteeringMessages != nil {
			for _, m := range a.getSteeringMessages() {
				c.Messages.AddMessage(m)
			}
		}
	}

	stopReason, runErr := a.resolveStopReason(c)

	c.Emit(AgentEndEvent, stopReason)
	a.runAgentEndHooks(c)

	return a.buildRunResult(c, stopReason, runErr), nil
}

// runTools 并发执行本轮所有工具调用，各自把结果写回自己的 call。
// 每个调用都过一遍 tool 中间件链（审批、日志、重试等）。
func (a *Agent) runTools(c *Context, calls []*schema.ToolCall) {
	var wg sync.WaitGroup
	for _, call := range calls {
		call := call
		wg.Add(1)
		go func() {
			c.Emit(ToolStartEvent, call)
			defer func() {
				if r := recover(); r != nil {
					call.Error = fmt.Errorf("tool panic: %v", r)
					call.Result = fmt.Sprintf("tool panic: %v", r)
				}
				c.Emit(ToolEndEvent, call)
				wg.Done()
			}()

			tool, ok := c.Tools.Get(call.Function.Name)
			if !ok {
				// 修掉旧实现里"静默吞掉"的 bug：告诉模型这个工具不存在。
				call.Result = "tool not found: " + call.Function.Name
				return
			}

			handler := chainTool(tool.Execute, a.toolMiddlewares...)
			res, err := handler(c, call)
			if err != nil {
				// error 只是结果：格式化后回喂模型，loop 不因此终止。
				call.Error = err
				call.Result = fmt.Sprintf("tool error: %v", err)
				return
			}
			call.Result = res
			if call.Result == "" {
				call.Result = FixPlaceholder
			}
		}()
	}
	wg.Wait()
}

// resolveStopReason 根据 Context 状态确定 loop 终止原因。
// 优先级：abortWithError > abort > maxIterations > completed。
func (a *Agent) resolveStopReason(c *Context) (schema.StopReason, error) {
	aborted, abortErr := c.StopState()
	switch {
	case abortErr != nil:
		return schema.StopError, abortErr
	case aborted:
		return schema.StopAborted, nil
	case c.Iteration >= a.maxIterations:
		return schema.StopMaxIteration, nil
	default:
		return schema.StopCompleted, nil
	}
}

// checkStop 查询 Context 的终止请求：若已 Abort，构建 RunResult 并返回 done=true。
// 干净终止（Abort()）记为 StopAborted；带错终止（AbortWithError）记为 StopError。
func (a *Agent) checkStop(c *Context) (*schema.RunResult, bool) {
	aborted, err := c.StopState()
	if !aborted {
		return nil, false
	}
	reason := schema.StopAborted
	if err != nil {
		reason = schema.StopError
	}
	// 终止路径也执行 AgentEnd（defer 语义：无论成败都收尾）。
	c.Emit(AgentEndEvent, reason)
	a.runAgentEndHooks(c)
	return a.buildRunResult(c, reason, err), true
}

// buildRunResult 从 Context 构建 RunResult。
func (a *Agent) buildRunResult(c *Context, reason schema.StopReason, runErr error) *schema.RunResult {
	r := &schema.RunResult{
		Messages:   c.Messages.Raw(),
		Message:    c.Message,
		Usage:      c.usage,
		Iterations: c.Iteration,
		StopReason: reason,
		Timing: schema.Timing{
			TotalDuration: time.Since(c.startedAt).Milliseconds(),
		},
	}
	if mn, ok := a.chatModel.(interface{ ModelName() string }); ok {
		r.Model = mn.ModelName()
	}
	if runErr != nil {
		r.Error = runErr.Error()
	}
	return r
}
