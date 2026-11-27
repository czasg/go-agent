package ga

import (
	"time"
)

func (a *Agent) Messages() []*Message {
	a.histMu.RLock()
	defer a.histMu.RUnlock()
	return append([]*Message(nil), a.messages...)
}

func (a *Agent) Len() int {
	a.histMu.RLock()
	defer a.histMu.RUnlock()
	return len(a.messages)
}

func (a *Agent) Last() *Message {
	a.histMu.RLock()
	defer a.histMu.RUnlock()
	if len(a.messages) == 0 {
		return nil
	}
	return a.messages[len(a.messages)-1]
}

func (a *Agent) Find(fn func(*Message) bool) *Message {
	if fn == nil {
		return nil
	}
	for _, m := range a.Messages() {
		if fn(m) {
			return m
		}
	}
	return nil
}

func (a *Agent) FindLast(fn func(*Message) bool) *Message {
	if fn == nil {
		return nil
	}
	msgs := a.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if fn(msgs[i]) {
			return msgs[i]
		}
	}
	return nil
}

func (a *Agent) IndexOf(id string) int {
	a.histMu.RLock()
	defer a.histMu.RUnlock()
	for i, m := range a.messages {
		if m.ID == id {
			return i
		}
	}
	return -1
}

func (a *Agent) Usage() Usage {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	return a.usage
}

func (a *Agent) System() string {
	a.histMu.RLock()
	defer a.histMu.RUnlock()
	return a.system
}

func (a *Agent) SetSystem(s string) {
	a.histMu.Lock()
	a.system = s
	a.histMu.Unlock()
}

func (a *Agent) Clear(keepSystem bool) {
	a.histMu.Lock()
	a.messages = nil
	if !keepSystem {
		a.system = ""
	}
	a.histMu.Unlock()
	a.emitEvent(nil, Event{Type: EvMessage, Data: MessageEvent{Op: OpReset, Messages: nil}})
}

func (a *Agent) Append(msgs ...*Message) {
	a.appendFrom(nil, msgs...)
}

func (a *Agent) appendFrom(c *Context, msgs ...*Message) {
	if len(msgs) == 0 {
		return
	}
	type added struct {
		idx int
		m   *Message
	}
	var list []added
	a.histMu.Lock()
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.ID == "" {
			m.ID = newID("msg")
		}
		if m.CreatedAt.IsZero() {
			m.CreatedAt = time.Now()
		}
		a.messages = append(a.messages, m)
		list = append(list, added{idx: len(a.messages) - 1, m: m})
	}
	a.histMu.Unlock()
	for _, it := range list {
		a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpAdd, Index: it.idx, Message: it.m}})
	}
}

func (a *Agent) Insert(i int, m *Message) {
	a.insertFrom(nil, i, m)
}

func (a *Agent) insertFrom(c *Context, i int, m *Message) {
	if m == nil {
		return
	}
	a.histMu.Lock()
	if i < 0 {
		i = 0
	}
	if i > len(a.messages) {
		i = len(a.messages)
	}
	if m.ID == "" {
		m.ID = newID("msg")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	a.messages = append(a.messages, nil)
	copy(a.messages[i+1:], a.messages[i:])
	a.messages[i] = m
	a.histMu.Unlock()
	a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpAdd, Index: i, Message: m}})
}

func (a *Agent) InsertBefore(id string, m *Message) bool {
	i := a.IndexOf(id)
	if i < 0 {
		return false
	}
	a.insertFrom(nil, i, m)
	return true
}

func (a *Agent) Set(i int, m *Message) bool {
	return a.setFrom(nil, i, m)
}

func (a *Agent) setFrom(c *Context, i int, m *Message) bool {
	if m == nil {
		return false
	}
	a.histMu.Lock()
	if i < 0 || i >= len(a.messages) {
		a.histMu.Unlock()
		return false
	}
	if m.ID == "" {
		m.ID = newID("msg")
	}
	a.messages[i] = m
	a.histMu.Unlock()
	a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpReplace, Index: i, Message: m}})
	return true
}

func (a *Agent) Update(id string, fn func(*Message)) bool {
	return a.updateFrom(nil, id, fn)
}

func (a *Agent) updateFrom(c *Context, id string, fn func(*Message)) bool {
	if fn == nil {
		return false
	}
	a.histMu.RLock()
	idx := -1
	for i, cur := range a.messages {
		if cur.ID == id {
			idx = i
			break
		}
	}
	var target *Message
	if idx >= 0 {
		target = a.messages[idx].Clone()
	}
	a.histMu.RUnlock()
	if idx < 0 {
		return false
	}

	fn(target)

	a.histMu.Lock()
	at := -1
	for i, cur := range a.messages {
		if cur.ID == id {
			at = i
			break
		}
	}
	if at >= 0 {
		a.messages[at] = target
	}
	a.histMu.Unlock()
	if at < 0 {
		return false
	}
	a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpReplace, Index: at, Message: target}})
	return true
}

func (a *Agent) Remove(i int) bool {
	return a.removeFrom(nil, i)
}

func (a *Agent) removeFrom(c *Context, i int) bool {
	a.histMu.Lock()
	if i < 0 || i >= len(a.messages) {
		a.histMu.Unlock()
		return false
	}
	m := a.messages[i]
	a.messages = append(a.messages[:i], a.messages[i+1:]...)
	a.histMu.Unlock()
	a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpRemove, Index: i, Message: m}})
	return true
}

func (a *Agent) RemoveWhere(fn func(*Message) bool) int {
	return a.removeWhereFrom(nil, fn)
}

func (a *Agent) removeWhereFrom(c *Context, fn func(*Message) bool) int {
	if fn == nil {
		return 0
	}
	cur := a.Messages()

	var removed []*Message
	idx := map[*Message]int{}
	drop := map[*Message]struct{}{}
	for i, m := range cur {
		if fn(m) {
			removed = append(removed, m)
			idx[m] = i
			drop[m] = struct{}{}
		}
	}
	if len(removed) == 0 {
		return 0
	}

	a.histMu.Lock()
	kept := make([]*Message, 0, len(a.messages))
	for _, m := range a.messages {
		if _, ok := drop[m]; ok {
			continue
		}
		kept = append(kept, m)
	}
	a.messages = kept
	a.histMu.Unlock()

	for _, m := range removed {
		a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpRemove, Index: idx[m], Message: m}})
	}
	return len(removed)
}

func (a *Agent) Edit(fn func([]*Message) []*Message) {
	a.editFrom(nil, fn)
}

func (a *Agent) editFrom(c *Context, fn func([]*Message) []*Message) {
	if fn == nil {
		return
	}
	next := fn(a.Messages())

	clean := make([]*Message, 0, len(next))
	for _, m := range next {
		if m != nil {
			clean = append(clean, m)
		}
	}

	a.histMu.Lock()
	a.messages = clean
	snapshot := append([]*Message(nil), clean...)
	a.histMu.Unlock()
	a.emitEvent(c, Event{Type: EvMessage, Data: MessageEvent{Op: OpReset, Messages: snapshot}})
}

type Round struct {
	Start    int
	End      int
	Messages []*Message
}

func (a *Agent) Rounds() []Round {
	return Rounds(a.Messages())
}

func Rounds(msgs []*Message) []Round {
	var rounds []Round
	start := -1
	for i, m := range msgs {
		if m == nil || m.Role != RoleUser {
			continue
		}
		if start >= 0 {
			rounds = append(rounds, Round{Start: start, End: i, Messages: msgs[start:i]})
		}
		start = i
	}
	if start >= 0 {
		rounds = append(rounds, Round{Start: start, End: len(msgs), Messages: msgs[start:]})
	}
	return rounds
}

func (r Round) Tokens() int { return Tokens(r.Messages) }

func (a *Agent) ContextTokens() int { return Tokens(a.Messages()) }

func (c *Context) ContextTokens() int { return c.Agent.ContextTokens() }

func (c *Context) Messages() []*Message { return c.Agent.Messages() }

func (c *Context) Len() int { return c.Agent.Len() }

func (c *Context) Last() *Message { return c.Agent.Last() }

func (c *Context) Find(fn func(*Message) bool) *Message { return c.Agent.Find(fn) }

func (c *Context) FindLast(fn func(*Message) bool) *Message { return c.Agent.FindLast(fn) }

func (c *Context) Append(msgs ...*Message) { c.Agent.appendFrom(c, msgs...) }

func (c *Context) Insert(i int, m *Message) { c.Agent.insertFrom(c, i, m) }

func (c *Context) InsertBefore(id string, m *Message) bool {
	return c.Agent.InsertBefore(id, m)
}

func (c *Context) Set(i int, m *Message) bool { return c.Agent.setFrom(c, i, m) }

func (c *Context) Update(id string, fn func(*Message)) bool { return c.Agent.updateFrom(c, id, fn) }

func (c *Context) Remove(i int) bool { return c.Agent.removeFrom(c, i) }

func (c *Context) RemoveWhere(fn func(*Message) bool) int { return c.Agent.removeWhereFrom(c, fn) }

func (c *Context) Edit(fn func([]*Message) []*Message) { c.Agent.editFrom(c, fn) }

func (c *Context) SetSystem(s string) { c.Agent.SetSystem(s) }

func (c *Context) Clear(keepSystem bool) { c.Agent.Clear(keepSystem) }

func (c *Context) Usage() Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}
