package task

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	ga "github.com/czasg/go-agent"
)

const toolName = "task"

type spec struct {
	agent *ga.Agent
}

type Options struct {
	specs  []spec
	events ga.EventsMode
	global bool
}

type Option func(*Options)

func WithAgent(a *ga.Agent) Option {
	if a == nil {
		panic("task.WithAgent: agent 不能为 nil")
	}
	if a.Name == "" {
		panic("task.WithAgent: agent 名称不能为空，请用 ga.WithName 设置")
	}
	if a.Description == "" {
		panic("task.WithAgent: agent 描述不能为空，请用 ga.WithDescription 设置")
	}
	return func(o *Options) {
		o.specs = append(o.specs, spec{agent: a})
	}
}

func WithEvents(mode ga.EventsMode) Option {
	return func(o *Options) { o.events = mode }
}

type Task struct {
	opts Options
	tool *ga.Tool
}

func New(opts ...Option) *Task {
	o := Options{events: ga.EventsTransparent}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}

	sort.Slice(o.specs, func(i, j int) bool {
		return o.specs[i].agent.Name < o.specs[j].agent.Name
	})

	t := &Task{opts: o}
	t.tool = ga.NewToolRaw(toolName, t.description(), t.params(), t.execute)
	return t
}

func (t *Task) Tool() *ga.Tool { return t.tool }

func (t *Task) Tools() []*ga.Tool { return []*ga.Tool{t.tool} }

func (t *Task) Names() []string {
	names := make([]string, 0, len(t.opts.specs))
	for _, s := range t.opts.specs {
		names = append(names, s.agent.Name)
	}
	sort.Strings(names)
	return names
}

func (t *Task) description() string {
	d := "在隔离上下文中执行子任务：克隆一个分身独立跑完整循环，只把最终结果带回主对话。" +
		"分身看不到本次工具调用之后的历史，也看不到本轮对话之外的语境，所以 task 描述必须自包含（目标 + 输入 + 验收标准）。" +
		"适合需要多步工具调用、过程细节无需回流的子问题。"
	if len(t.opts.specs) > 0 {
		d += "agent 参数决定用哪个分身：留空则克隆当前智能体（复用其 system 与工具，并继承当前对话历史）；" +
			"指定名称则用一个全新上下文的专用智能体（只有它自己的 system 与工具，不继承当前对话，适用时更需自包含）。" +
			"可用智能体：" + strings.Join(t.Names(), "、") + "。"
	}
	return d
}

func (t *Task) params() map[string]any {
	props := map[string]any{
		"task": map[string]any{
			"type":        "string",
			"description": "子任务的完整描述，须自包含（分身看不到本轮对话之外的语境），包含目标与验收标准",
		},
	}
	if len(t.opts.specs) > 0 {
		props["agent"] = map[string]any{
			"type":        "string",
			"description": "目标智能体名称；留空克隆当前智能体并继承对话历史，指定名称则用全新上下文的专用智能体（不继承对话）",
			"enum":        t.Names(),
		}
	}
	return map[string]any{
		"type":                 "object",
		"properties":           props,
		"required":             []string{"task"},
		"additionalProperties": false,
	}
}

type args struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

func (t *Task) execute(c *ga.Context, raw json.RawMessage) *ga.ToolResult {
	var in args
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return ga.Result("参数解析失败: " + err.Error()).AsErr()
		}
	}
	if strings.TrimSpace(in.Task) == "" {
		return ga.Result("task 不能为空").AsErr()
	}

	sub, err := t.prepare(c, in.Agent)
	if err != nil {
		return ga.Err(err)
	}

	child := c.Child(sub, t.opts.events)
	defer child.Close()

	res, err := sub.Run(child, ga.UserMessage(in.Task))
	if err != nil {
		return ga.Err(err)
	}
	return ga.Result(extract(res))
}

func (t *Task) prepare(c *ga.Context, name string) (*ga.Agent, error) {
	if c == nil || c.Agent == nil {
		return nil, errors.New("task 缺少父 agent")
	}
	if name != "" {
		target := t.find(name)
		if target == nil {
			return nil, errors.New("智能体不存在: " + name)
		}
		sub := target.Clone()
		sub.Tools.Remove(toolName)
		resetContext(sub)
		return sub, nil
	}
	sub := c.Agent.Clone()
	sub.Tools.Remove(toolName)
	trimIncomplete(sub)
	return sub, nil
}

func (t *Task) find(name string) *ga.Agent {
	for _, s := range t.opts.specs {
		if s.agent.Name == name {
			return s.agent
		}
	}
	return nil
}

func trimIncomplete(a *ga.Agent) {
	quiet(a, func() {
		a.Edit(func(h []*ga.Message) []*ga.Message {
			end := len(h)
			for i := len(h) - 1; i >= 0; i-- {
				if h[i].Role == ga.RoleTool {
					continue
				}
				if h[i].Role == ga.RoleAssistant && len(h[i].ToolCalls) > 0 {
					end = i
				}
				break
			}
			return h[:end]
		})
	})
}

func resetContext(a *ga.Agent) {
	quiet(a, func() { a.Clear(true) })
}

func quiet(a *ga.Agent, fn func()) {
	em := a.Emitter
	a.Emitter = nil
	fn()
	a.Emitter = em
}

func extract(r *ga.RunResult) string {
	if r == nil || r.Final == nil || r.Final.Text() == "" {
		return "(子任务完成但没有输出)"
	}
	ans := r.Final.Text()
	switch r.StopReason {
	case ga.StopMaxTurns:
		return ans + "\n\n[子任务达到轮次上限，结果可能不完整]"
	case ga.StopCanceled:
		return ans + "\n\n[子任务被取消]"
	case ga.StopError:
		return ans + "\n\n[子任务异常终止]"
	}
	return ans
}
