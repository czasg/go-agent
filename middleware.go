package ga

import (
	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema"
)

// 本文件是框架的“洋葱”层。中间件用装饰器模型包住一次操作，
// 相比 hook 更强：能决定内层要不要跑、跑几次、怎么跑（重试、超时、缓存、
// defer 清理），而 hook 只能在固定时间点观察/叫停。
//
// 关键点：装饰器天然自带“中断”能力——不调用 next 就等于中断（这正是
// gin 的 c.Abort() 的本质）。所以这里不需要任何特殊的“中断 error”。
//
// 两套中间件，各管一个粒度，命名带前缀以示区分：
//   - ToolMiddleware  包一次工具执行（tool.Execute）
//   - ModelMiddleware 包一次模型调用（model.Chat）

// ── Tool 中间件 ──────────────────────────────────────────────

// ToolHandler 是”处理一次工具调用”的函数。中间件包的就是它。
// middleware 和 tool 看到统一的签名：都拿到完整的 *schema.ToolCall。
type ToolHandler func(c *Context, call *schema.ToolCall) (string, error)

// ToolMiddleware 拿到内层 handler，返回一个包装后的 handler。经典洋葱。
type ToolMiddleware func(next ToolHandler) ToolHandler

// chainTool 按注册顺序把中间件层层包在 handler 外面。
// mws[0] 在最外层（最先看到请求、最后看到结果）。
func chainTool(h ToolHandler, mws ...ToolMiddleware) ToolHandler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ForTools 把一个中间件的作用域限定到指定工具名，其余工具直接放行。
// 用于“只给危险工具加审批”这类需求，避免给每个工具单独包一层。
//
//	WithToolMiddlewares(
//		LogMW,                                  // 所有工具
//		ForTools(ApprovalMW(app), "delete_file"), // 只 delete_file
//	)
func ForTools(mw ToolMiddleware, names ...string) ToolMiddleware {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return func(next ToolHandler) ToolHandler {
		wrapped := mw(next) // 命中时走的链
		return func(c *Context, call *schema.ToolCall) (string, error) {
			if set[call.Function.Name] {
				return wrapped(c, call)
			}
			return next(c, call) // 未命中：跳过该中间件
		}
	}
}

// ── Model 中间件 ─────────────────────────────────────────────

// ModelHandler 是“处理一次模型调用”的函数。
type ModelHandler func(c *Context, messages []*schema.Message, opts ...llm.Option) (*schema.Message, error)

// ModelMiddleware 包一次模型调用（重试、限流、token 计费、缓存、日志）。
type ModelMiddleware func(next ModelHandler) ModelHandler

// modelAsHandler 把一个 llm.BaseModel 适配成最内层的 ModelHandler。
func modelAsHandler(m llm.BaseModel) ModelHandler {
	return func(c *Context, messages []*schema.Message, opts ...llm.Option) (*schema.Message, error) {
		return m.Chat(c, messages, opts...)
	}
}

// chainModel 与 chainTool 同构：mws[0] 在最外层。
func chainModel(h ModelHandler, mws ...ModelMiddleware) ModelHandler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
