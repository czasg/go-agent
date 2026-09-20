package ga

import (
	"fmt"
	"github.com/czasg/go-agent/schema"
)

// Hook 是所有钩子的基接口。一个实现只需选择性实现下面它关心的接口即可，
// WithHooks 会用类型断言把它挂到对应的生命周期点。
//
// 设计约定（与 tool、middleware 一致的那条铁律）：
//   - Hook 只通过 Context 影响控制流：调用 c.Abort() / c.AbortWithError(err)
//     终止整个 loop。Hook 不返回 error —— “要不要继续”这件事只有一个表达方式。
//   - Hook 全部在主 loop 的单一 goroutine 里串行执行（不像 tool 是并发的），
//     所以在 hook 里读写 Context 上的普通字段是安全的。
//   - 只”观察不改”的需求（渲染、日志、落库、流式输出）请用 onEvent，不要用 hook。
//   - MessageDeltaHook 例外：它在模型流式调用的回调中同步执行，调用 c.Abort()
//     会取消当前流式连接。
type Hook interface {
	Name() string
}

// ── Agent 生命周期 ────────────────────────────────────────────

// AgentStartHook 在 Run 开始时触发一次（进入循环之前）。
// 典型用途：初始化校验、注入工具/上下文（MCP、skill）。
// 需要中止时调用 c.Abort()。
type AgentStartHook interface {
	Hook
	OnAgentStart(c *Context)
}

// AgentEndHook 在 Run 结束时触发一次，无论成败都会执行（defer 语义）。
// 典型用途：会话持久化、资源清理、摘要生成。
type AgentEndHook interface {
	Hook
	OnAgentEnd(c *Context)
}

// ── Message 生命周期 ────────────────────────────────────────────

// IterationStartHook 在每次迭代（调用模型）之前触发。
// 典型用途：上下文修复/压缩、过滤空消息、注入动态上下文（时间、环境）。
// 可修改 c.Messages。需要中止时调用 c.Abort()。
type IterationStartHook interface {
	Hook
	OnIterationStart(c *Context)
}

// MessageEndHook 在模型返回完整响应后触发（此时 c.Message 已就绪）。
// 典型用途：内容安全过滤、工具调用审计。需要中止时调用 c.Abort()。
//
// 注意：这里没有 msg 参数——它就是 c.Message，从 Context 读即可，避免冗余。
type MessageEndHook interface {
	Hook
	OnMessageEnd(c *Context)
}

// MessageDeltaHook 在模型流式输出每个增量时触发。
// 典型用途：重复检测、实时内容过滤、流式监控。
// 注意：此 hook 在模型流式调用内部同步执行，实现应尽量轻量。
// 若调用 c.Abort()，当前模型调用的流式连接会被取消。
type MessageDeltaHook interface {
	Hook
	OnMessageDelta(c *Context, delta *schema.MessageDelta)
}

// ── 迭代控制 ────────────────────────────────────────────

// IterationEndHook 在每轮迭代结束后触发（工具执行完毕、下一轮模型调用之前）。
// 典型用途：上下文压缩、轮次预算控制、质量检查。
type IterationEndHook interface {
	Hook
	OnIterationEnd(c *Context)
}

// ── Hook 执行器（带 panic 恢复） ──────────────────────────────────

func (a *Agent) runAgentStartHooks(c *Context) (*schema.RunResult, bool) {
	for _, h := range a.agentStartHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.AbortWithError(fmt.Errorf("hook %T panicked: %v", h, r))
				}
			}()
			h.OnAgentStart(c)
		}()
		if r, done := a.checkStop(c); done {
			return r, true
		}
	}
	return nil, false
}

func (a *Agent) runIterationStartHooks(c *Context) (*schema.RunResult, bool) {
	for _, h := range a.iterationStartHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.AbortWithError(fmt.Errorf("hook %T panicked: %v", h, r))
				}
			}()
			h.OnIterationStart(c)
		}()
		if r, done := a.checkStop(c); done {
			return r, true
		}
	}
	return nil, false
}

func (a *Agent) runMessageEndHooks(c *Context) (*schema.RunResult, bool) {
	for _, h := range a.messageEndHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.AbortWithError(fmt.Errorf("hook %T panicked: %v", h, r))
				}
			}()
			h.OnMessageEnd(c)
		}()
		if r, done := a.checkStop(c); done {
			return r, true
		}
	}
	return nil, false
}

func (a *Agent) runIterationEndHooks(c *Context) (*schema.RunResult, bool) {
	for _, h := range a.iterationEndHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.AbortWithError(fmt.Errorf("hook %T panicked: %v", h, r))
				}
			}()
			h.OnIterationEnd(c)
		}()
		if r, done := a.checkStop(c); done {
			return r, true
		}
	}
	return nil, false
}

func (a *Agent) runMessageDeltaHooks(c *Context, delta *schema.MessageDelta) {
	for _, h := range a.messageDeltaHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.AbortWithError(fmt.Errorf("hook %T panicked: %v", h, r))
				}
			}()
			h.OnMessageDelta(c, delta)
		}()
	}
}

func (a *Agent) runAgentEndHooks(c *Context) {
	for _, h := range a.agentEndHooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					_ = r // EndHook panic 不影响终止流程
				}
			}()
			h.OnAgentEnd(c)
		}()
	}
}
