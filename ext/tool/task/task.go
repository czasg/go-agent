// Package task 提供一个智能体调用工具 task：
//   - 不传 agent 参数：克隆当前智能体，在隔离上下文中跑子任务（task 模式）
//   - 传 agent 参数：委托给指定的子智能体执行（sub-agent 模式）
//
// schema 根据 WithAgent 注册的子智能体动态生成：没有注册任何子智能体时，
// 模型看到的就是一个普通的 task 工具，不会出现 agent 参数。
//
// 子循环与主循环共享同一个 Agent 的 hooks/middlewares/model（task 模式），
// 或使用目标 Agent 的完整配置（sub-agent 模式）。
// 消息历史独立，事件带 CallID 供消费方区分归属。
package task

import (
	"errors"
	"sort"
	"strings"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// Options 配置 task 工具。
type Options struct {
	agents []*ga.Agent
}

type Option func(*Options)

// WithAgent 注册一个子智能体，可多次调用注册多个。
// 智能体的 Name() 会作为 schema 中的枚举值。
// 有注册时 schema 会包含可选的 agent 参数。
//
//	task.New(
//	    task.WithAgent(reviewerAgent),
//	    task.WithAgent(translatorAgent),
//	)
func WithAgent(a *ga.Agent) Option {
	if a.Name() == "" {
		panic("task.WithAgent: agent name 不能为空，请用 ga.WithName 设置")
	}
	if a.Description() == "" {
		panic("task.WithAgent: agent description 不能为空，请用 ga.WithDescription 设置")
	}
	return func(o *Options) {
		o.agents = append(o.agents, a)
	}
}

// New 构造 task 工具。
//
// 不传 WithAgent 时 schema 中不含 agent 参数，
// 模型看到的就是一个纯 task 工具（克隆自己跑子任务）。
//
//	taskTool, _ := task.New()
//	taskTool, _ := task.New(task.WithAgent("reviewer", reviewerAgent))
func New(opts ...Option) (ga.BaseTool, error) {
	o := Options{}
	for _, fn := range opts {
		fn(&o)
	}
	info := buildInfo(o.agents)
	return &taskTool{info: info, agents: o.agents}, nil
}

func buildInfo(agents []*ga.Agent) schema.ToolInfo {
	desc := "在隔离上下文中求解子任务：以当前模型与上下文独立运行一轮完整循环，只把最终结果带回主对话。" +
		"适合需要多步工具调用、过程细节无需回流的子问题。任务描述须自包含。"

	props := map[string]any{
		"task": map[string]any{
			"type":        "string",
			"description": "子任务的完整描述，须自包含（分身看不到本轮对话之外的语境），包含目标与验收标准",
		},
	}
	required := []string{"task"}

	if len(agents) > 0 {
		names := make([]string, len(agents))
		for i, a := range agents {
			names[i] = a.Name()
		}
		desc += "可通过 agent 参数指定子智能体（留空则克隆当前智能体）。可用智能体：" + strings.Join(names, "、") + "。"
		props["agent"] = map[string]any{
			"type":        "string",
			"description": "目标智能体名称，留空则克隆当前智能体",
			"enum":        names,
		}
	}

	return schema.ToolInfo{
		Name:        "task",
		Description: desc,
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		},
	}
}

type taskTool struct {
	info   schema.ToolInfo
	agents []*ga.Agent
}

func (t *taskTool) Info() schema.ToolInfo { return t.info }

func (t *taskTool) Execute(c *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := call.JSON(&in); err != nil {
		return "", errors.New("参数解析失败: " + err.Error())
	}
	if in.Task == "" {
		return "", errors.New("task 不能为空")
	}

	var sub *ga.Context

	if in.Agent != "" {
		// ── sub-agent 模式：委托给指定智能体 ──
		target := t.findAgent(in.Agent)
		if target == nil {
			return "", errors.New("智能体不存在: " + in.Agent)
		}
		sub = ga.NewContext(c, target, nil)
	} else {
		// ── task 模式：克隆当前智能体 ──
		sub = c.Copy()
		sub.Messages.Set(snapshotMessages(c.Messages.Raw()))
		sub.Tools.Deny("task")
	}

	sub.CallID = call.ID
	sub.Messages.AddMessage(&ga.Message{Role: ga.User, Content: in.Task})

	result, err := ga.RunLoop(sub)
	if err != nil {
		return "", err
	}

	c.AddUsage(result.Usage)
	return extractAnswer(result), nil
}

func (t *taskTool) findAgent(name string) *ga.Agent {
	for _, a := range t.agents {
		if a.Name() == name {
			return a
		}
	}
	return nil
}

func snapshotMessages(msgs []*ga.Message) []*ga.Message {
	if len(msgs) == 0 {
		return nil
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != ga.Tool {
			if msgs[i].Role == ga.Assistant {
				return msgs[:i]
			}
			break
		}
	}
	return msgs
}

func extractAnswer(r *ga.RunResult) string {
	if r.Message != nil && r.Message.Content != "" {
		ans := r.Message.Content
		switch r.StopReason {
		case ga.StopMaxIteration:
			return ans + "\n\n[subtask reached max iterations, result may be incomplete]"
		case ga.StopAborted, ga.StopCancelled:
			return ans + "\n\n[subtask stopped: " + string(r.StopReason) + "]"
		default:
			return ans
		}
	}
	return "(subtask completed with no output)"
}

// Names 返回所有已注册的子智能体名称（按字母排序）。
func (t *taskTool) Names() []string {
	names := make([]string, len(t.agents))
	for i, a := range t.agents {
		names[i] = a.Name()
	}
	sort.Strings(names)
	return names
}
