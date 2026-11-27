package summary

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	ga "github.com/czasg/go-agent"
)

const (
	DefaultTokenLimit = 100000
	DefaultKeepRounds = 3

	metaMark    = "summary"
	metaCovered = "covered_count"

	EvSummary ga.EventType = "summary"
)

const defaultPrompt = "请将以下对话压缩为简明摘要，保留关键信息、决策和上下文。直接输出摘要，不要加前缀。"

type Event struct {
	Summary  string
	Tokens   int
	Replaced []*ga.Message
	Kept     []*ga.Message
}

type Options struct {
	TokenLimit int
	KeepRounds int
	Model      ga.Model
	Prompt     string
}

type Option func(*Options)

func WithTokenLimit(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.TokenLimit = n
		}
	}
}

func WithKeepRounds(n int) Option {
	return func(o *Options) {
		if n > 0 {
			o.KeepRounds = n
		}
	}
}

func WithModel(m ga.Model) Option {
	return func(o *Options) {
		if m != nil {
			o.Model = m
		}
	}
}

func WithPrompt(p string) Option {
	return func(o *Options) {
		if p = strings.TrimSpace(p); p != "" {
			o.Prompt = p
		}
	}
}

type Hook struct {
	opts Options
}

func New(opts ...Option) *Hook {
	o := Options{TokenLimit: DefaultTokenLimit, KeepRounds: DefaultKeepRounds, Prompt: defaultPrompt}
	for _, fn := range opts {
		if fn != nil {
			fn(&o)
		}
	}
	if o.TokenLimit <= 0 {
		o.TokenLimit = DefaultTokenLimit
	}
	if o.KeepRounds <= 0 {
		o.KeepRounds = DefaultKeepRounds
	}
	if o.Prompt == "" {
		o.Prompt = defaultPrompt
	}
	return &Hook{opts: o}
}

func (h *Hook) OnTurnStart(c *ga.Context) ga.Decision {
	if _, _, err := h.Compact(c); err != nil {
		c.Emit(ga.Event{Type: ga.EvError, Data: ga.ErrorEvent{Stage: "summary", Err: err}})
	}
	return ga.Next()
}

func (h *Hook) Hooks() ga.Hooks {
	return ga.Hooks{}.OnTurnStart(h.OnTurnStart)
}

func (h *Hook) Install(a *ga.Agent) {
	if a == nil {
		return
	}
	a.Hooks = a.Hooks.Use(h)
}

func (h *Hook) ShouldCompact(c *ga.Context) bool {
	if c == nil || c.Agent == nil {
		return false
	}
	return ga.Tokens(c.Messages()) >= h.opts.TokenLimit
}

func (h *Hook) Compact(c *ga.Context) (Event, bool, error) {
	return h.compact(c, false)
}

func (h *Hook) ForceCompact(c *ga.Context) (Event, bool, error) {
	return h.compact(c, true)
}

func (h *Hook) TokenLimit() int { return h.opts.TokenLimit }

func (h *Hook) KeepRounds() int { return h.opts.KeepRounds }

func (h *Hook) compact(c *ga.Context, force bool) (Event, bool, error) {
	if c == nil || c.Agent == nil {
		return Event{}, false, fmt.Errorf("summary: 缺少 context")
	}

	msgs := c.Messages()
	total := ga.Tokens(msgs)
	if !force && total < h.opts.TokenLimit {
		return Event{}, false, nil
	}

	cut := cutIndex(msgs, h.opts.KeepRounds)
	if cut <= 0 {
		return Event{}, false, nil
	}

	head := 0
	for head < cut && msgs[head].Role == ga.RoleSystem {
		head++
	}
	replaced := msgs[head:cut]
	if len(replaced) < 2 {
		return Event{}, false, nil
	}

	text, err := h.summarize(c, replaced)
	if err != nil {
		return Event{}, false, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Event{}, false, fmt.Errorf("summary: 摘要为空")
	}

	checkpoint := &ga.Message{
		ID:        "sum_" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Role:      ga.RoleUser,
		Content:   text,
		CreatedAt: time.Now(),
	}
	checkpoint.SetMeta(metaMark, true)
	checkpoint.SetMeta(metaCovered, len(replaced))

	kept := msgs[cut:]
	for _, m := range kept {
		if m == nil {
			continue
		}
		m.Usage = nil
	}
	next := make([]*ga.Message, 0, head+1+len(kept))
	next = append(next, msgs[:head]...)
	next = append(next, checkpoint)
	next = append(next, kept...)
	c.Edit(func([]*ga.Message) []*ga.Message { return next })

	ev := Event{Summary: text, Tokens: total, Replaced: replaced, Kept: kept}
	c.Emit(ga.Event{Type: EvSummary, Data: ev})
	return ev, true, nil
}

func (h *Hook) Last(c *ga.Context) (string, bool) {
	if c == nil {
		return "", false
	}
	msgs := c.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m != nil && marked(m) {
			return m.Text(), true
		}
	}
	return "", false
}

func (h *Hook) summarize(c *ga.Context, msgs []*ga.Message) (string, error) {
	model := h.opts.Model
	if model == nil {
		model = c.Agent.Model
	}
	if model == nil {
		return "", fmt.Errorf("summary: 没有可用的模型")
	}

	var buf strings.Builder
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if marked(m) {
			fmt.Fprintf(&buf, "[Previous summary]: %s\n", m.Text())
			continue
		}
		switch m.Role {
		case ga.RoleUser:
			fmt.Fprintf(&buf, "User: %s\n", m.Text())
		case ga.RoleAssistant:
			fmt.Fprintf(&buf, "Assistant: %s\n", m.Text())
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&buf, "[Call %s]: %s\n", tc.Name, string(tc.Arguments))
			}
		case ga.RoleTool:
			fmt.Fprintf(&buf, "[Tool %s]: %s\n", m.Name, m.Text())
		}
	}

	prompt := h.opts.Prompt + "\n\n" + buf.String()
	ctx := context.Context(c)
	if c.Context == nil {
		ctx = context.Background()
	}
	resp, err := model.Generate(ctx, &ga.ModelRequest{Messages: []*ga.Message{ga.UserMessage(prompt)}})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Message == nil {
		return "", fmt.Errorf("summary: 模型没有返回内容")
	}
	return resp.Message.Text(), nil
}

func cutIndex(msgs []*ga.Message, keep int) int {
	if keep <= 0 {
		return -1
	}
	rounds := ga.Rounds(msgs)
	usable := make([]ga.Round, 0, len(rounds))
	for _, r := range rounds {
		if len(r.Messages) == 1 && marked(r.Messages[0]) {
			continue
		}
		usable = append(usable, r)
	}
	if len(usable) <= keep {
		return -1
	}
	return usable[len(usable)-keep].Start
}

func marked(m *ga.Message) bool {
	if m == nil {
		return false
	}
	v, ok := m.MetaValue(metaMark)
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}
