// Package approve 提供一个框架级的审批中间件：拦截指定工具的执行，
// 在真正执行前阻塞等待用户审批。
//
// 与 ask 不同，approve 不是模型主动调用的工具，而是 ToolMiddleware——
// 模型调用 delete_file / drop_table 等危险工具时，中间件自动拦截并
// 请求审批，模型完全无感（它只看到正常的工具结果或拒绝结果）。
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithTools(deleteFile, dropTable),
//	    ga.WithToolMiddlewares(
//	        ga.ForTools(approve.New(console), "delete_file", "drop_table"),
//	    ),
//	)
package approve

import (
	"context"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// Decision 是一次审批的结果。
type Decision struct {
	Approved bool   // 是否批准
	Reason   string // 备注/理由（拒绝原因或批准附言），回喂给模型
}

// Approver 决定"怎么把审批请求抛给用户、怎么拿回决定"。
// 业务侧实现它：拿到完整的 *schema.ToolCall，自行组装展示信息。
type Approver interface {
	Approve(ctx context.Context, call *schema.ToolCall) (Decision, error)
}

// New 构造审批中间件。拦截每一次工具调用，阻塞等 Approver 决定：
//   - 放行 → next(c, call)，工具正常执行；
//   - 拒绝 → 直接返回拒绝理由，不执行工具。
//
// 典型配合 ga.ForTools 限定拦截范围：
//
//	ga.ForTools(approve.New(approver), "delete_file", "drop_table")
func New(a Approver) ga.ToolMiddleware {
	return func(next ga.ToolHandler) ga.ToolHandler {
		return func(c *ga.Context, call *schema.ToolCall) (string, error) {
			d, err := a.Approve(c, call)
			if err != nil {
				return "", err
			}
			if !d.Approved {
				if d.Reason != "" {
					return "用户拒绝执行此操作: " + d.Reason, nil
				}
				return "用户拒绝执行此操作", nil
			}
			return next(c, call)
		}
	}
}
