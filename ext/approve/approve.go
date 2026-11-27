package approve

import (
	"context"
	"encoding/json"
	"slices"

	ga "github.com/czasg/go-agent"
)

type Decision struct {
	Approved bool
	Reason   string
}

type Approver interface {
	Approve(ctx context.Context, call *ga.ToolCall) (Decision, error)
}

type Options struct {
	Tools []string
}

func WithTools(names ...string) func(*Options) {
	return func(o *Options) { o.Tools = append([]string(nil), names...) }
}

type Hook struct {
	approver Approver
	opts     Options
}

func New(a Approver, opts ...func(*Options)) *Hook {
	o := Options{}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	return &Hook{approver: a, opts: o}
}

func (h *Hook) OnToolStart(c *ga.Context, call *ga.ToolCall, t *ga.Tool) ga.Decision {
	if h == nil || h.approver == nil || call == nil || !h.match(call.Name) {
		return ga.Next()
	}
	d, err := h.approver.Approve(c, call)
	if err != nil {
		return ga.Fail(err)
	}
	if !d.Approved {
		if d.Reason != "" {
			return ga.SkipText("用户拒绝执行此操作: " + d.Reason)
		}
		return ga.SkipText("用户拒绝执行此操作")
	}
	return ga.Next()
}

func (h *Hook) match(name string) bool {
	return len(h.opts.Tools) == 0 || slices.Contains(h.opts.Tools, name)
}

func Middleware(name string, a Approver) ga.ToolHandlerMiddleware {
	return func(next ga.ToolHandler) ga.ToolHandler {
		return func(c *ga.Context, args json.RawMessage) *ga.ToolResult {
			if a == nil {
				return next(c, args)
			}
			d, err := a.Approve(c, &ga.ToolCall{Name: name, Arguments: args})
			if err != nil {
				return ga.Err(err)
			}
			if !d.Approved {
				if d.Reason != "" {
					return ga.Result("用户拒绝执行此操作: " + d.Reason)
				}
				return ga.Result("用户拒绝执行此操作")
			}
			return next(c, args)
		}
	}
}

func Install(agent *ga.Agent, a Approver, opts ...func(*Options)) *ga.Agent {
	if agent == nil || a == nil {
		return agent
	}
	return agent.BindHooks(New(a, opts...))
}
