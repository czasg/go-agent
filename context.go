package ga

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Scope int

const (
	ScopePersist Scope = iota
	ScopeTurn
	ScopeCall
)

type EventsMode int

const (
	EventsSilent EventsMode = iota
	EventsTransparent
	EventsOwn
)

type Context struct {
	context.Context

	Agent    *Agent
	RunID    string
	Parent   string
	Root     string
	Depth    int
	Turn     int
	StopTool *ToolCall

	mu       sync.Mutex
	emitter  Emitter
	seq      *seqCounter
	span     string
	parent   *Context
	mode     EventsMode
	internal bool

	turnMsgs []*Message
	callMsgs []*Message
	sysSec   map[string]string
	usage    Usage

	aborted atomic.Bool
	closed  atomic.Bool
}

func (c *Context) Tools() *ToolBox {
	if c == nil || c.Agent == nil {
		return nil
	}
	return c.Agent.Tools
}

func (c *Context) Inject(m *Message, s Scope) {
	if c == nil || m == nil {
		return
	}
	if m.Meta == nil {
		m.Meta = map[string]any{}
	}
	switch s {
	case ScopeTurn:
		m.Meta[metaEphemeral] = "turn"
		c.mu.Lock()
		c.turnMsgs = append(c.turnMsgs, m)
		c.mu.Unlock()
	case ScopeCall:
		m.Meta[metaEphemeral] = "call"
		c.mu.Lock()
		c.callMsgs = append(c.callMsgs, m)
		c.mu.Unlock()
	default:
		c.Agent.appendFrom(c, m)
	}
}

func (c *Context) InjectText(role Role, text string, s Scope) {
	c.Inject(&Message{Role: role, Content: text}, s)
}

func (c *Context) Emit(ev Event) {
	if c == nil || c.Agent == nil {
		return
	}
	c.Agent.emitEvent(c, ev)
}

func (c *Context) SetSystemSection(key, text string) {
	if c == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(text) == "" {
		delete(c.sysSec, key)
		return
	}
	if c.sysSec == nil {
		c.sysSec = map[string]string{}
	}
	c.sysSec[key] = text
}

func (c *Context) SystemSection(key string) string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sysSec[strings.TrimSpace(key)]
}

func (c *Context) SystemSections() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sysSec) == 0 {
		return nil
	}
	keys := make([]string, 0, len(c.sysSec))
	for k := range c.sysSec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.sysSec[k])
	}
	return out
}

func (c *Context) SetState(k string, v any) {
	if c == nil || c.Agent == nil {
		return
	}
	a := c.Agent
	a.stateMu.Lock()
	if a.State == nil {
		a.State = map[string]any{}
	}
	if v == nil {
		delete(a.State, k)
	} else {
		a.State[k] = v
	}
	a.stateMu.Unlock()
}

func (c *Context) GetState(k string) (any, bool) {
	if c == nil || c.Agent == nil {
		return nil, false
	}
	c.Agent.stateMu.RLock()
	defer c.Agent.stateMu.RUnlock()
	v, ok := c.Agent.State[k]
	return v, ok
}

func (c *Context) State(key string) any {
	v, _ := c.GetState(key)
	return v
}

func (c *Context) AbortTool() {
	if c == nil {
		return
	}
	c.aborted.Store(true)
}

func (c *Context) beginTurn() {
	c.mu.Lock()
	c.turnMsgs = nil
	c.mu.Unlock()
}

func (c *Context) pending() []*Message {
	c.mu.Lock()
	out := make([]*Message, 0, len(c.turnMsgs)+len(c.callMsgs))
	out = append(out, c.turnMsgs...)
	out = append(out, c.callMsgs...)
	c.callMsgs = nil
	c.mu.Unlock()
	return out
}

func (c *Context) takeCallMsgs() []*Message {
	c.mu.Lock()
	out := c.callMsgs
	c.callMsgs = nil
	c.mu.Unlock()
	return out
}

func (c *Context) Child(a *Agent, mode EventsMode) *Context {
	child := &Context{
		Context:  c,
		Agent:    a,
		RunID:    newID("run"),
		Parent:   c.RunID,
		Root:     c.Root,
		Depth:    c.Depth + 1,
		seq:      c.seq,
		parent:   c,
		mode:     mode,
		internal: mode != EventsOwn,
	}
	child.span = c.span + "/" + child.RunID
	switch mode {
	case EventsSilent:
		child.emitter = nil
	case EventsOwn:
		child.emitter = a.Emitter
	default:
		child.emitter = c.emitter
	}
	return child
}

func (c *Context) Close() {
	if c == nil || c.parent == nil {
		return
	}
	if !c.closed.CompareAndSwap(false, true) {
		return
	}
	c.mu.Lock()
	u := c.usage
	c.usage = Usage{}
	c.mu.Unlock()
	if u.Zero() {
		return
	}
	p := c.parent
	if p.Agent != nil {
		p.Agent.account(p, u)
		return
	}
	p.mu.Lock()
	p.usage = p.usage.Add(u)
	p.mu.Unlock()
}

func (c *Context) Deadline() (time.Time, bool) {
	if c == nil || c.Context == nil {
		return time.Time{}, false
	}
	return c.Context.Deadline()
}

func (c *Context) Done() <-chan struct{} {
	if c == nil || c.Context == nil {
		return nil
	}
	return c.Context.Done()
}

func (c *Context) Err() error {
	if c == nil || c.Context == nil {
		return nil
	}
	return c.Context.Err()
}

func (c *Context) Value(key any) any {
	if c == nil || c.Context == nil {
		return nil
	}
	return c.Context.Value(key)
}

func (c *Context) fillEvent(ev *Event) {
	ev.RootID = c.Root
	ev.RunID = c.RunID
	ev.ParentID = c.Parent
	ev.Depth = c.Depth
	ev.Span = c.span
	ev.Turn = c.Turn
	if ev.Owner == "" {
		ev.Owner = c.Agent.Name
	}
	ev.Internal = c.internal
	ev.TraceID = TraceIDFrom(c.Context)
}

func (c *Context) runHook(name string, fn func() Decision) (d Decision) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("hook %s panicked: %v", name, r)
			c.Agent.emitEvent(c, Event{Type: EvError, Data: ErrorEvent{Stage: "hook", Err: err}})
			d = Fail(err)
		}
	}()
	d = fn()
	if d.Kind != KindNext {
		c.Agent.emitEvent(c, Event{Type: EvHook, Data: HookEvent{Name: name, Kind: kindName(d.Kind), Reason: d.Reason}})
	}
	return d
}

func (c *Context) fireRunStart() {
	for _, h := range c.Agent.Hooks.RunStart {
		if h == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					err := fmt.Errorf("hook %T.OnRunStart panicked: %v", h, r)
					c.Agent.emitEvent(c, Event{Type: EvError, Data: ErrorEvent{Stage: "hook", Err: err}})
				}
			}()
			h(c)
		}()
	}
}

func (c *Context) fireTurnStart() Decision {
	for _, h := range c.Agent.Hooks.TurnStart {
		if h == nil {
			continue
		}
		if d := c.runHook("OnTurnStart", func() Decision { return h(c) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireModelStart(req *ModelRequest) Decision {
	for _, h := range c.Agent.Hooks.ModelStart {
		if h == nil {
			continue
		}
		if d := c.runHook("OnModelStart", func() Decision { return h(c, req) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireModelEnd(resp *ModelResponse) Decision {
	for _, h := range c.Agent.Hooks.ModelEnd {
		if h == nil {
			continue
		}
		if d := c.runHook("OnModelEnd", func() Decision { return h(c, resp) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireToolStart(call *ToolCall, t *Tool) Decision {
	for _, h := range c.Agent.Hooks.ToolStart {
		if h == nil {
			continue
		}
		if d := c.runHook("OnToolStart", func() Decision { return h(c, call, t) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireToolEnd(call *ToolCall, res *ToolResult) Decision {
	for _, h := range c.Agent.Hooks.ToolEnd {
		if h == nil {
			continue
		}
		if d := c.runHook("OnToolEnd", func() Decision { return h(c, call, res) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireTurnEnd() Decision {
	for _, h := range c.Agent.Hooks.TurnEnd {
		if h == nil {
			continue
		}
		if d := c.runHook("OnTurnEnd", func() Decision { return h(c) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireShouldStop() Decision {
	for _, h := range c.Agent.Hooks.ShouldStop {
		if h == nil {
			continue
		}
		if d := c.runHook("OnShouldStop", func() Decision { return h(c) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireMaxTurns() Decision {
	for _, h := range c.Agent.Hooks.MaxTurns {
		if h == nil {
			continue
		}
		if d := c.runHook("OnMaxTurns", func() Decision { return h(c) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireError(err error) Decision {
	for _, h := range c.Agent.Hooks.Error {
		if h == nil {
			continue
		}
		if d := c.runHook("OnError", func() Decision { return h(c, err) }); d.Kind != KindNext {
			return d
		}
	}
	return Next()
}

func (c *Context) fireRunEnd(reason StopReason) {
	for _, h := range c.Agent.Hooks.RunEnd {
		if h == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					err := fmt.Errorf("hook %T.OnRunEnd panicked: %v", h, r)
					c.Agent.emitEvent(c, Event{Type: EvError, Data: ErrorEvent{Stage: "hook", Err: err}})
				}
			}()
			h(c, reason)
		}()
	}
}

func (c *Context) result(reason StopReason) *RunResult {
	msgs := c.Agent.Messages()
	return &RunResult{
		Messages:   msgs,
		Final:      lastAssistant(msgs),
		StopTool:   c.StopTool,
		Turns:      c.Turn,
		Usage:      c.usage,
		StopReason: reason,
	}
}

func lastAssistant(msgs []*Message) *Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == RoleAssistant {
			return msgs[i]
		}
	}
	if len(msgs) > 0 {
		return msgs[len(msgs)-1]
	}
	return nil
}
