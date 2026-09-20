package ga

import (
	"context"
	"sync"
	"time"

	"github.com/czasg/go-agent/schema"
)

// Context 是每一次 Run 的运行时状态载体，贯穿整个 agent loop。
//
// 设计要点（贯穿整个框架的一条铁律）：
//   - 返回值只表达”结果”，控制流只走 Context。
//   - Context 嵌入 context.Context：外部取消（超时、调用方 cancel）走原生
//     ctx.Done()/ctx.Err()；框架内部的主动终止走 Abort()。
//   - Abort() 同时 cancel 内部 context，使正在进行的模型流式调用立即中断。
//   - 工具是并发执行的（见 agent.go 的 wg），所以 Abort 相关字段用锁保护，
//     任意 goroutine 调用都安全。
type Context struct {
	context.Context // 嵌入：外部取消能力直接复用（Done/Err/Deadline/Value)

	Agent     *Agent          // 本次 Run 使用的 Agent（只读配置）
	Messages  *MessageStore   // 消息管理器，和 Tools 对齐
	Iteration int             // 当前迭代轮次（从 0 开始）
	Message   *schema.Message // 当前轮次模型返回的助手消息
	CallID    string          // 可选标识，由扩展层设置（如 task 子循环设为工具调用 ID），主循环为空串

	startedAt time.Time         // Run 开始时间
	usage     schema.TokenUsage // 本次 Run 累计 token 用量

	mu       sync.Mutex // 保护下面的控制位（工具并发时可能同时写）
	aborted  bool       // 是否已请求终止
	abortErr error      // 终止携带的错误（nil 表示”干净终止”）

	cancel context.CancelFunc // 取消内部 context，Abort 时一并调用

	eventMu sync.Mutex // 保护 onEvent 回调的串行调用

	// store 供 middleware/hook/tool 之间共享任意数据（并发安全）。
	// value 建议用不可变类型（string/int/struct 值），避免引用类型 race。
	store map[string]any

	// Tools 是本次 Run 私有的工具注册表：NewContext 从 Agent 的种子 Copy()
	// 而来，之后主循环从这里取工具、暴露工具列表。hook/tool 直接操作它即可
	// （c.Tools.Register 注入动态工具、c.Tools.Allow 收窄可见范围）——写它不
	// 影响 Agent 的种子，与 Messages 从 history 浅拷贝、loop 内 append 不写回
	// 调用方 slice 完全对称。ToolStore 自带 RWMutex，工具并发执行中操作也安全。
	Tools *ToolStore
}

// Get 从 Context 中取共享数据。并发安全。
func (c *Context) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.store[key]
	return v, ok
}

// Set 往 Context 中写共享数据。并发安全，多次写同一 key 后者覆盖。
func (c *Context) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		c.store = make(map[string]any)
	}
	c.store[key] = value
}

// NewContext 基于一份历史消息创建运行时 Context。
// 通常由 Agent.Run 内部调用；扩展层（如 task 子智能体）需要用不同的
// Agent 创建 Context 时也可直接调用。history 为 nil 时不带历史消息。
func NewContext(ctx context.Context, agent *Agent, history []*schema.Message) *Context {
	store := NewMessageStore(history...)

	// system prompt 始终在最前，且只有一条：若历史里已带 system 则追加到同一条。
	if agent.systemPrompt != "" {
		store.AppendSystemHint(agent.systemPrompt)
	}

	// 创建可取消的子 context：Abort() 时一并 cancel，
	// 使正在进行的模型流式调用（或其他阻塞在 ctx 上的操作）立即中断。
	innerCtx, cancel := context.WithCancel(ctx)

	return &Context{
		Context:   innerCtx,
		Agent:     agent,
		Messages:  store,
		Tools:     agent.toolStore.Copy(), // 从 Agent 种子拷一份私有副本，运行期注入不回写
		startedAt: time.Now(),
		cancel:    cancel,
	}
}

// AddUsage 累加 token 用量（如子循环用量合并到主循环）。
func (c *Context) AddUsage(u schema.TokenUsage) {
	c.usage.Add(u)
}

// Copy 基于当前 Context 创建一个新的独立 Context。
// 拷贝当前完整消息历史（浅拷贝 slice，不共享底层数组），
// 工具集从 Agent 种子重新 Copy，两者互不影响。
// 调用方可根据需要修改 sub.Messages（如裁剪快照）、sub.Tools（如 Deny）。
func (c *Context) Copy() *Context {
	return NewContext(c.Context, c.Agent, c.Messages.Raw())
}

// Abort 请求“干净终止”整个 loop（非异常，例如用户主动喊停、审批拒绝后收尾）。
// 并发安全：多个工具 goroutine 同时调用只有第一次生效。
func (c *Context) Abort() {
	c.abort(nil)
}

// AbortWithError 请求终止整个 loop，并携带错误（会体现在 RunResult.Error）。
func (c *Context) AbortWithError(err error) {
	c.abort(err)
}

func (c *Context) abort(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.aborted {
		return // 已终止，保留第一个原因
	}
	c.aborted = true
	c.abortErr = err
	// 取消内部 context，使正在进行的模型流式调用立即中断。
	if c.cancel != nil {
		c.cancel()
	}
}

// StopState 返回是否已请求终止、以及携带的错误。引擎在各 loop 边界查询。
func (c *Context) StopState() (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted, c.abortErr
}

// Emit 发送观察事件，自动带上当前迭代号与 CallID。
// hook / tool 可主动触发自定义事件（如 approve.request），
// 事件类型建议加命名空间前缀，避免与引擎事件冲突。
func (c *Context) Emit(typ EventType, data any) {
	if c.Agent.onEvent == nil {
		return
	}
	c.eventMu.Lock()
	defer c.eventMu.Unlock()
	c.Agent.onEvent(Event{Type: typ, Iteration: c.Iteration, CallID: c.CallID, Data: data})
}
