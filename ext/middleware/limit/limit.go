// Package limit 提供工具并发限流中间件：用信号量限制同一时刻实际执行的
// 工具调用数。模型一轮 fan-out N 个调用时（引擎默认全并发），保护外部
// 资源不被 N 路同时打挂（rate-limit / DB 连接 / 外呼风暴）。
//
// 信号量在 New 时创建，Agent 生命周期内全局共享（跨 Run、跨并发调用都
// 算数——保护的是外部资源，与哪轮 Run 无关）。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithToolMiddlewares(limit.New(3)), // 最多同时执行 3 个工具
//	)
package limit

import (
	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// New 返回并发上限 n 的工具中间件。n < 1 视为 1。
// 信号量在构造时创建，Agent 生命周期内全局共享。
func New(n int) ga.ToolMiddleware {
	if n < 1 {
		n = 1
	}
	sem := make(chan struct{}, n)
	return func(next ga.ToolHandler) ga.ToolHandler {
		return func(c *ga.Context, call *schema.ToolCall) (string, error) {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				return next(c, call)
			case <-c.Done():
				return "", c.Err()
			}
		}
	}
}
