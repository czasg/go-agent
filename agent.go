package ga

import (
	"context"
	"errors"
	"sync"
	"time"
)

type RunResult struct {
	Messages   []*Message
	Final      *Message
	StopTool   *ToolCall
	Turns      int
	Usage      Usage
	StopReason StopReason
}

func (r *RunResult) Text() string {
	if r == nil || r.Final == nil {
		return ""
	}
	return r.Final.Text()
}

func (r *RunResult) StopMessage() *Message {
	if r == nil || r.StopTool == nil {
		return nil
	}
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role == RoleTool && m.ToolCallID == r.StopTool.ID {
			return m
		}
	}
	return nil
}

type Agent struct {
	ID    string
	Name  string
	Model Model

	Description string

	Tools   *ToolBox
	Hooks   Hooks
	State   map[string]any
	Emitter Emitter

	system   string
	messages []*Message

	cfg     Config
	runMu   sync.Mutex
	histMu  sync.RWMutex
	stateMu sync.RWMutex
	usageMu sync.Mutex
	running bool
	seq     *seqCounter
	usage   Usage
}

func New(opts ...Option) *Agent {
	a := &Agent{
		ID:    newID("agent"),
		Tools: NewToolBox(),
		State: map[string]any{},
		cfg:   Config{MaxTurns: 128, MaxParallel: 4},
		seq:   &seqCounter{},
	}
	for _, o := range opts {
		if o != nil {
			o(a)
		}
	}
	if a.Tools == nil {
		a.Tools = NewToolBox()
	}
	if a.State == nil {
		a.State = map[string]any{}
	}
	return a
}

func (a *Agent) Run(ctx context.Context, input ...*Message) (*RunResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	a.runMu.Lock()
	if a.running {
		a.runMu.Unlock()
		return nil, ErrBusy
	}
	a.running = true
	a.runMu.Unlock()
	defer func() {
		a.runMu.Lock()
		a.running = false
		a.runMu.Unlock()
	}()

	if a.Model == nil {
		return nil, &RunError{Stage: "model", Err: errors.New("ga: model not set")}
	}
	if err := ctx.Err(); err != nil {
		return nil, &RunError{Stage: "run", Err: err}
	}
	return a.loop(a.contextFor(ctx), input)
}

func (a *Agent) contextFor(ctx context.Context) *Context {
	if p, ok := ctx.(*Context); ok && p.Agent == a {
		return p
	}
	c := &Context{
		Context: ctx,
		Agent:   a,
		RunID:   newID("run"),
		seq:     a.seq,
		emitter: a.Emitter,
	}
	c.Root = c.RunID
	c.span = c.RunID
	if p, ok := ctx.(*Context); ok {
		c.Parent = p.RunID
		c.Root = p.Root
		c.Depth = p.Depth + 1
		c.seq = p.seq
		c.span = p.span + "/" + c.RunID
		c.parent = p
		c.internal = p.internal
	}
	return c
}

func (a *Agent) emitEvent(c *Context, ev Event) {
	var em Emitter
	if c != nil {
		c.fillEvent(&ev)
		if c.seq != nil {
			ev.Seq = c.seq.next()
		} else {
			ev.Seq = a.seq.next()
		}
		em = c.emitter
	} else {
		ev.Seq = a.seq.next()
		if ev.Owner == "" {
			ev.Owner = a.Name
		}
		em = a.Emitter
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if em == nil {
		return
	}
	defer func() { _ = recover() }()
	em.Emit(ev)
}

func (a *Agent) account(c *Context, u Usage) {
	if u.Zero() {
		return
	}
	a.usageMu.Lock()
	a.usage = a.usage.Add(u)
	a.usageMu.Unlock()
	if c != nil {
		c.mu.Lock()
		c.usage = c.usage.Add(u)
		c.mu.Unlock()
	}
}

func (a *Agent) Config() Config {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	return a.cfg
}

func (a *Agent) maxParallel() int {
	if a.cfg.MaxParallel <= 0 {
		return 1
	}
	return a.cfg.MaxParallel
}

func (a *Agent) BindHooks(hooks ...any) *Agent {
	a.Hooks = a.Hooks.Use(hooks...)
	return a
}

func (a *Agent) Clone() *Agent {
	out := &Agent{
		ID:          newID("agent"),
		Name:        a.Name,
		Description: a.Description,
		Model:       a.Model,
		Tools:       a.Tools.Clone(),
		Hooks:       a.Hooks.Clone(),
		State:       map[string]any{},
		Emitter:     a.Emitter,
		seq:         &seqCounter{},
	}
	a.runMu.Lock()
	out.cfg = a.cfg
	a.runMu.Unlock()

	a.histMu.RLock()
	out.system = a.system
	out.messages = make([]*Message, 0, len(a.messages))
	for _, m := range a.messages {
		out.messages = append(out.messages, m.Clone())
	}
	a.histMu.RUnlock()

	a.stateMu.RLock()
	out.State = cloneState(a.State)
	a.stateMu.RUnlock()

	out.usage = a.Usage()
	return out
}

type Snapshot struct {
	ID       string         `json:"id"`
	System   string         `json:"system"`
	Messages []*Message     `json:"messages"`
	State    map[string]any `json:"state"`
	Usage    Usage          `json:"usage"`
}

func (a *Agent) Snapshot() *Snapshot {
	s := &Snapshot{ID: a.ID, Usage: a.Usage()}

	a.histMu.RLock()
	s.System = a.system
	s.Messages = make([]*Message, 0, len(a.messages))
	for _, m := range a.messages {
		s.Messages = append(s.Messages, m.Clone())
	}
	a.histMu.RUnlock()

	a.stateMu.RLock()
	s.State = cloneState(a.State)
	a.stateMu.RUnlock()
	return s
}

func (a *Agent) Restore(s *Snapshot) error {
	if s == nil {
		return errors.New("ga: nil snapshot")
	}
	a.runMu.Lock()
	if a.running {
		a.runMu.Unlock()
		return ErrBusy
	}
	a.running = true
	a.runMu.Unlock()
	defer func() {
		a.runMu.Lock()
		a.running = false
		a.runMu.Unlock()
	}()

	a.histMu.Lock()
	a.system = s.System
	a.messages = make([]*Message, 0, len(s.Messages))
	for _, m := range s.Messages {
		a.messages = append(a.messages, m.Clone())
	}
	a.histMu.Unlock()

	a.usageMu.Lock()
	a.usage = s.Usage
	a.usageMu.Unlock()

	a.stateMu.Lock()
	a.State = cloneState(s.State)
	a.stateMu.Unlock()

	a.emitEvent(nil, Event{Type: EvMessage, Data: MessageEvent{Op: OpReset, Messages: a.Messages()}})
	return nil
}
