package plan

import (
	"fmt"
	"strings"
	"time"

	ga "github.com/czasg/go-agent"
)

const (
	statusPending    = "pending"
	statusInProgress = "in_progress"
	statusCompleted  = "completed"

	defaultToolName = "plan"
	stateKey        = "plan"
	maxSteps        = 20
)

type Step struct {
	Title  string `json:"title" desc:"步骤标题，祈使句形式，如 '为 plan 包补齐单测'" required:"true"`
	Status string `json:"status" desc:"步骤状态：pending 待处理、in_progress 进行中、completed 已完成" enum:"pending,in_progress,completed" default:"pending"`
	Note   string `json:"note,omitempty" desc:"补充说明，如产出物或验证方式"`
}

type Plan struct {
	Goal      string    `json:"goal"`
	Steps     []Step    `json:"steps"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (p Plan) Done() bool {
	if len(p.Steps) == 0 {
		return false
	}
	for _, s := range p.Steps {
		if s.Status != statusCompleted {
			return false
		}
	}
	return true
}

func (p Plan) Progress() (int, int) {
	done := 0
	for _, s := range p.Steps {
		if s.Status == statusCompleted {
			done++
		}
	}
	return done, len(p.Steps)
}

func (p Plan) Active() (Step, bool) {
	for _, s := range p.Steps {
		if s.Status == statusInProgress {
			return s, true
		}
	}
	return Step{}, false
}

func (p Plan) String() string {
	var b strings.Builder
	if p.Goal != "" {
		b.WriteString("目标：" + p.Goal + "\n")
	}
	for i, s := range p.Steps {
		b.WriteString(fmt.Sprintf("%d. %s %s", i+1, mark(s.Status), s.Title))
		if s.Note != "" {
			b.WriteString("（" + s.Note + "）")
		}
		b.WriteString("\n")
	}
	if done, total := p.Progress(); total > 0 {
		b.WriteString(fmt.Sprintf("进度：%d/%d 完成", done, total))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p Plan) block() string {
	return "## 当前计划\n" + p.String()
}

func mark(status string) string {
	switch status {
	case statusCompleted:
		return "[x]"
	case statusInProgress:
		return "[~]"
	}
	return "[ ]"
}

type Option func(*Planner)

func WithPrompt(prompt string) Option {
	return func(p *Planner) {
		if strings.TrimSpace(prompt) != "" {
			p.prompt = prompt
		}
	}
}

func WithToolName(name string) Option {
	return func(p *Planner) {
		if name = strings.TrimSpace(name); name != "" {
			p.toolName = name
		}
	}
}

type Planner struct {
	toolName string
	prompt   string
	tool     *ga.Tool
}

func New(opts ...Option) *Planner {
	p := &Planner{toolName: defaultToolName}
	for _, o := range opts {
		if o != nil {
			o(p)
		}
	}
	if p.prompt == "" {
		p.prompt = defaultPrompt(p.toolName)
	}
	p.tool = ga.NewTool(p.toolName, description(), p.execute)
	p.tool.Serial = true
	return p
}

func (p *Planner) Tool() *ga.Tool { return p.tool }

func (p *Planner) Tools() []*ga.Tool { return []*ga.Tool{p.tool} }

func (p *Planner) Options() []ga.Option {
	return []ga.Option{
		ga.WithTools(p.tool),
		ga.WithHooks(p),
	}
}

func (p *Planner) Install(a *ga.Agent) {
	if a == nil {
		return
	}
	a.Tools.Add(p.tool)
	a.BindHooks(p)
}

func (p *Planner) Current(a *ga.Agent) (Plan, bool) {
	if a == nil {
		return Plan{}, false
	}
	return load(&ga.Context{Agent: a})
}

func (p *Planner) Clear(a *ga.Agent) {
	if a == nil {
		return
	}
	(&ga.Context{Agent: a}).SetState(stateKey, nil)
}

func (p *Planner) Apply(a *ga.Agent, goal string, steps []Step) (string, error) {
	if a == nil {
		return "", fmt.Errorf("缺少 agent")
	}
	next, err := build(goal, steps, Plan{})
	if err != nil {
		return "", err
	}
	c := &ga.Context{Agent: a}
	if next.Done() {
		save(c, Plan{})
		return "全部步骤已完成，计划已清除，规划模式结束。目标：" + next.Goal, nil
	}
	save(c, next)
	return next.String(), nil
}

func (p *Planner) OnRunStart(c *ga.Context) {
	if p.prompt != "" {
		c.SetSystemSection("plan", p.prompt)
	}
}

func (p *Planner) OnModelStart(c *ga.Context, req *ga.ModelRequest) ga.Decision {
	if cur, ok := load(c); ok {
		c.Inject(ga.UserMessage(cur.block()), ga.ScopeCall)
	}
	return ga.Next()
}

type args struct {
	Goal  string `json:"goal,omitempty" desc:"计划目标：做成什么样算完成，一句话说清；首次提交必填，之后省略则沿用原目标"`
	Steps []Step `json:"steps" desc:"完整步骤列表，每次全量覆盖，1-20 个"`
}

func (p *Planner) execute(c *ga.Context, in args) *ga.ToolResult {
	prev, had := load(c)
	next, err := build(in.Goal, in.Steps, prev)
	if err != nil {
		return ga.Err(err)
	}

	if next.Done() {
		save(c, Plan{})
		return ga.Result("全部步骤已完成，计划已清除，规划模式结束。目标：" + next.Goal)
	}

	if had && same(prev, next) {
		done, total := next.Progress()
		out := fmt.Sprintf("计划没有变化，已在执行中：%d/%d 完成", done, total)
		if active, ok := next.Active(); ok {
			out += "，当前：" + active.Title
		}
		return ga.Result(out + "。继续推进，不要重复提交计划。")
	}

	save(c, next)

	if !had {
		return ga.Result("计划已提交，等待用户确认。\n\n" + next.String()).AsStop()
	}

	done, total := next.Progress()
	out := fmt.Sprintf("计划已更新：%d/%d 完成", done, total)
	if active, ok := next.Active(); ok {
		out += "，当前：" + active.Title
	}
	return ga.Result(out)
}

func same(a, b Plan) bool {
	if a.Goal != b.Goal || len(a.Steps) != len(b.Steps) {
		return false
	}
	for i := range a.Steps {
		if a.Steps[i] != b.Steps[i] {
			return false
		}
	}
	return true
}

func build(goal string, steps []Step, prev Plan) (Plan, error) {
	normalized, err := normalizeSteps(steps)
	if err != nil {
		return Plan{}, err
	}
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = prev.Goal
	}
	if goal == "" {
		return Plan{}, fmt.Errorf("goal 不能为空：提交计划需要一句话说明目标")
	}
	return Plan{Goal: goal, Steps: normalized, UpdatedAt: time.Now()}, nil
}

func normalizeSteps(in []Step) ([]Step, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("steps 不能为空：至少给出 1 个步骤")
	}
	if len(in) > maxSteps {
		return nil, fmt.Errorf("steps 最多 %d 个，收到 %d 个", maxSteps, len(in))
	}
	out := make([]Step, 0, len(in))
	hasActive := false
	for i, s := range in {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			return nil, fmt.Errorf("第 %d 个步骤缺少 title", i+1)
		}
		status := strings.ToLower(strings.TrimSpace(s.Status))
		if status == "" {
			status = statusPending
		}
		switch status {
		case statusPending, statusInProgress, statusCompleted:
		default:
			return nil, fmt.Errorf("第 %d 个步骤状态 %q 非法，只能是 pending / in_progress / completed", i+1, s.Status)
		}
		if status == statusInProgress {
			if hasActive {
				status = statusPending
			}
			hasActive = true
		}
		out = append(out, Step{Title: title, Status: status, Note: strings.TrimSpace(s.Note)})
	}
	return out, nil
}

func save(c *ga.Context, p Plan) {
	c.SetState(stateKey, p)
}

func load(c *ga.Context) (Plan, bool) {
	v, ok := c.GetState(stateKey)
	if !ok {
		return Plan{}, false
	}
	p, ok := v.(Plan)
	if !ok || len(p.Steps) == 0 {
		return Plan{}, false
	}
	return p, true
}

func description() string {
	return "维护当前任务的结构化计划，用于规划模式。" +
		"首次提交计划时本轮会暂停，把计划交给用户确认；之后再次提交直接生效（中途根据新情况整体重规划也走这条），不打断执行。" +
		"steps 每次全量覆盖，1-20 个，status 取 pending / in_progress / completed，同一时间只保留一个 in_progress；" +
		"全部 completed 后计划自动清除。" +
		"三步以上、或用户给出多个需求时使用，简单任务直接执行。"
}

func defaultPrompt(name string) string {
	return fmt.Sprintf(`# 规划模式
三步以上的复杂任务，先用 %s 提交计划，首次提交会停下等用户确认，确认后再动手。
- 提交计划：%s(goal="...", steps=[...])，只有第一次会停，之后提交直接生效
- 推进进度：%s(steps=[...]) 全量覆盖，不会打断
- 中途根据新情况需要整体重规划时，直接再提交一次即可
- 同一时间只保留一步 in_progress；全部步骤 completed 后计划自动清除
- 一两步的小事直接做，不建计划`, name, name, name)
}
