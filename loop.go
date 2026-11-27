package ga

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

func (a *Agent) loop(c *Context, input []*Message) (*RunResult, error) {
	if len(input) > 0 {
		a.appendFrom(c, input...)
	}
	a.emitEvent(c, Event{Type: EvRunStart, Data: RunStartEvent{Input: input}})
	c.fireRunStart()

	var reason StopReason
	var runErr error

	cfg := a.Config()
	budget := cfg.MaxTurns

	for {
		reason, runErr = a.turns(c, budget)
		if reason != StopNone {
			break
		}
		d := c.fireMaxTurns()
		if d.Kind == KindRedo {
			budget = cfg.MaxTurns
			continue
		}
		reason, runErr = a.exhausted(c, d)
		break
	}

	c.fireRunEnd(reason)
	res := c.result(reason)
	a.emitEvent(c, Event{Type: EvRunEnd, Data: RunEndEvent{Reason: reason, Result: res}})
	if runErr != nil {
		return res, &RunError{Stage: "run", Err: runErr, Turn: c.Turn, Span: c.span}
	}
	return res, nil
}

func (a *Agent) exhausted(c *Context, d Decision) (StopReason, error) {
	switch d.Kind {
	case KindStop:
		if d.Msg != nil {
			a.appendFrom(c, d.Msg)
		}
		return StopByHook, nil
	case KindFail:
		return StopError, d.Err
	}
	return StopMaxTurns, nil
}

func (a *Agent) turns(c *Context, budget int) (StopReason, error) {
	for i := 0; budget <= 0 || i < budget; i++ {
		c.Turn++
		c.beginTurn()
		a.emitEvent(c, Event{Type: EvTurnStart, Data: TurnEvent{Turn: c.Turn}})
		reason, err := a.safeTurn(c)
		reason, err = a.endTurn(c, reason, err)
		a.emitEvent(c, Event{Type: EvTurnEnd, Data: TurnEvent{Turn: c.Turn}})
		if reason != StopNone {
			return reason, err
		}
	}
	return StopNone, nil
}

func (a *Agent) endTurn(c *Context, reason StopReason, err error) (StopReason, error) {
	d := c.fireTurnEnd()
	if reason == StopCanceled {
		return reason, err
	}
	if d.Kind == KindFail {
		return StopError, d.Err
	}
	if reason != StopNone {
		return reason, err
	}
	if h := a.settle(c, d); h != nil {
		return h.reason, h.err
	}
	return StopNone, nil
}

func (a *Agent) safeTurn(c *Context) (reason StopReason, err error) {
	defer func() {
		if r := recover(); r != nil {
			reason = StopError
			err = fmt.Errorf("ga: turn %d panicked: %v", c.Turn, r)
		}
	}()
	return a.turn(c)
}

func (a *Agent) turn(c *Context) (StopReason, error) {
	resp, reason, err := a.respond(c)
	if reason != StopNone {
		return reason, err
	}
	if resp == nil {
		return StopNone, nil
	}

	if len(resp.Message.ToolCalls) == 0 {
		if h := a.settle(c, c.fireShouldStop()); h != nil {
			return h.reason, h.err
		}
		return StopNoTool, nil
	}

	outcomes, stopCall, failErr := a.execTools(c, resp.Message.ToolCalls)
	for _, o := range outcomes {
		if o.msg != nil {
			a.appendFrom(c, o.msg)
		}
	}
	if c.Err() != nil {
		return StopCanceled, nil
	}
	if failErr != nil {
		return StopError, failErr
	}
	if stopCall != nil {
		c.StopTool = stopCall
		return StopByTool, nil
	}
	return StopNone, nil
}

func (a *Agent) respond(c *Context) (*ModelResponse, StopReason, error) {
	resp, reason, err := a.answer(c)
	if reason != StopNone {
		return nil, reason, err
	}
	if resp == nil {
		return nil, StopNone, nil
	}

	resp = normalizeResponse(resp)

	if d := c.fireModelEnd(resp); d.Kind == KindSkip {
		resp.Message = droppedMessage(d.Msg)
	} else if d.Kind != KindNext {
		a.keep(c, resp.Message)
		if h := a.settle(c, d); h != nil {
			return nil, h.reason, h.err
		}
	}

	a.keep(c, resp.Message)
	return resp, StopNone, nil
}

func (a *Agent) answer(c *Context) (*ModelResponse, StopReason, error) {
	if d := c.fireTurnStart(); d.Kind == KindSkip {
		return &ModelResponse{Message: d.Msg}, StopNone, nil
	} else if h := a.settle(c, d); h != nil {
		return nil, h.reason, h.err
	}
	return a.invoke(c)
}

func (a *Agent) invoke(c *Context) (*ModelResponse, StopReason, error) {
	req := a.buildRequest(c)

	if d := c.fireModelStart(req); d.Kind == KindSkip {
		return &ModelResponse{Message: d.Msg}, StopNone, nil
	} else if h := a.settle(c, d); h != nil {
		return nil, h.reason, h.err
	}

	if extra := c.takeCallMsgs(); len(extra) > 0 {
		req.Messages = append(req.Messages, extra...)
	}
	resp, err := a.callModel(c, req)
	if err == nil {
		return resp, StopNone, nil
	}
	if c.Err() != nil {
		return nil, StopCanceled, nil
	}

	ed := c.fireError(err)
	switch ed.Kind {
	case KindRedo:
		return nil, StopNone, nil
	case KindStop:
		if ed.Msg != nil {
			a.appendFrom(c, ed.Msg)
		}
		return nil, StopError, nil
	case KindFail:
		if ed.Err != nil {
			return nil, StopError, ed.Err
		}
	}
	return nil, StopError, err
}

type halt struct {
	reason StopReason
	err    error
}

func (a *Agent) settle(c *Context, d Decision) *halt {
	switch d.Kind {
	case KindFail:
		return &halt{reason: StopError, err: d.Err}
	case KindStop:
		if d.Msg != nil {
			a.appendFrom(c, d.Msg)
		}
		return &halt{reason: StopByHook}
	case KindRedo:
		return &halt{reason: StopNone}
	}
	return nil
}

func (a *Agent) keep(c *Context, m *Message) {
	if m == nil || m.Empty() {
		return
	}
	if m.Role == "" {
		m.Role = RoleAssistant
	}
	a.appendFrom(c, m)
}

func normalizeResponse(resp *ModelResponse) *ModelResponse {
	if resp == nil {
		resp = &ModelResponse{}
	}
	if resp.Message == nil {
		resp.Message = &Message{}
	}
	if resp.Message.Role == "" {
		resp.Message.Role = RoleAssistant
	}
	resp.Message.Usage = &resp.Usage
	return resp
}

func droppedMessage(m *Message) *Message {
	if m == nil {
		return &Message{Role: RoleAssistant}
	}
	if m.Role == "" {
		m.Role = RoleAssistant
	}
	return m
}

func (a *Agent) buildRequest(c *Context) *ModelRequest {
	pending := c.pending()

	a.histMu.RLock()
	msgs := make([]*Message, 0, len(a.messages)+len(pending))
	msgs = append(msgs, a.messages...)
	msgs = append(msgs, pending...)
	sys := a.system
	a.histMu.RUnlock()

	if sections := c.SystemSections(); len(sections) > 0 {
		parts := make([]string, 0, len(sections)+1)
		if sys != "" {
			parts = append(parts, sys)
		}
		parts = append(parts, sections...)
		sys = strings.Join(parts, "\n\n")
	}

	return &ModelRequest{
		System:   sys,
		Messages: msgs,
		Tools:    a.Tools.Specs(),
		Params:   map[string]any{},
		Extra:    map[string]any{},
	}
}

func (a *Agent) callModel(c *Context, req *ModelRequest) (*ModelResponse, error) {
	a.emitEvent(c, Event{Type: EvModelStart, Data: ModelEvent{MsgCount: len(req.Messages), ToolCount: len(req.Tools), System: req.System}})

	if sm, ok := a.Model.(StreamModel); ok {
		return a.callStream(c, sm, req)
	}
	resp, err := a.Model.Generate(c, req)
	if err != nil {
		a.emitEvent(c, Event{Type: EvError, Data: ErrorEvent{Stage: "model", Err: err}})
		return nil, err
	}
	if resp == nil {
		resp = &ModelResponse{}
	}
	a.account(c, resp.Usage)
	a.emitEvent(c, Event{Type: EvModelEnd, Data: ModelEvent{System: req.System, Message: resp.Message, Usage: resp.Usage}})
	return resp, nil
}

func (a *Agent) callStream(c *Context, sm StreamModel, req *ModelRequest) (*ModelResponse, error) {
	stream, err := sm.Stream(c, req)
	if err != nil {
		a.emitEvent(c, Event{Type: EvError, Data: ErrorEvent{Stage: "model", Err: err}})
		return nil, err
	}
	defer stream.Close()

	var text strings.Builder
	var thinking strings.Builder
	calls := map[int]*ToolCall{}
	var order []int
	finish := ""
	var usage Usage

	for {
		chunk, rerr := stream.Recv()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			a.emitEvent(c, Event{Type: EvError, Data: ErrorEvent{Stage: "model", Err: rerr}})
			return nil, rerr
		}
		if chunk == nil {
			continue
		}
		if chunk.TextDelta != "" {
			text.WriteString(chunk.TextDelta)
			a.emitEvent(c, Event{Type: EvTextDelta, Data: DeltaEvent{Text: chunk.TextDelta}})
		}
		if chunk.ThinkingDelta != "" {
			thinking.WriteString(chunk.ThinkingDelta)
			a.emitEvent(c, Event{Type: EvThinkingDelta, Data: DeltaEvent{Text: chunk.ThinkingDelta}})
		}
		for _, td := range chunk.ToolCalls {
			slot, ok := calls[td.Index]
			if !ok {
				slot = &ToolCall{}
				calls[td.Index] = slot
				order = append(order, td.Index)
			}
			if td.ID != "" {
				slot.ID = td.ID
			}
			if td.Name != "" {
				if slot.Name == "" {
					slot.Name = td.Name
				}
			}
			if td.Arguments != "" {
				slot.Arguments = append(slot.Arguments, td.Arguments...)
			}
			a.emitEvent(c, Event{Type: EvToolDelta, Data: ToolDeltaEvent{CallID: slot.ID, Name: slot.Name, ArgsChunk: td.Arguments}})
		}
		if chunk.FinishReason != "" {
			finish = chunk.FinishReason
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
	}

	msg := &Message{Role: RoleAssistant, Content: text.String()}
	if thinking.Len() > 0 {
		msg.MultiContent = append(msg.MultiContent, Part{Type: "thinking", Text: thinking.String()})
	}
	sort.Ints(order)
	for _, i := range order {
		tc := calls[i]
		if tc == nil || tc.Name == "" {
			continue
		}
		if tc.ID == "" {
			tc.ID = newID("call")
		}
		if len(tc.Arguments) == 0 {
			tc.Arguments = json.RawMessage("{}")
		}
		msg.ToolCalls = append(msg.ToolCalls, *tc)
	}

	a.account(c, usage)
	a.emitEvent(c, Event{Type: EvModelEnd, Data: ModelEvent{System: req.System, Message: msg, Usage: usage}})
	return &ModelResponse{Message: msg, Usage: usage, FinishReason: finish}, nil
}

type toolOutcome struct {
	call *ToolCall
	msg  *Message
	stop bool
	fail error
}

func (a *Agent) execTools(c *Context, calls []ToolCall) ([]toolOutcome, *ToolCall, error) {
	out := make([]toolOutcome, len(calls))
	sem := make(chan struct{}, a.maxParallel())
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-c.Done():
			out[i] = toolOutcome{call: &calls[i], msg: toolMsg(&calls[i], &ToolResult{Content: "(canceled before start)", IsError: true})}
			wg.Done()
			continue
		}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			if t, ok := a.Tools.Get(calls[idx].Name); ok && t.Serial {
				mu := t.mutex()
				mu.Lock()
				defer mu.Unlock()
			}
			out[idx] = a.execOne(c, &calls[idx])
		}(i)
	}
	wg.Wait()

	var stopCall *ToolCall
	var failErr error
	for i := range out {
		if out[i].call == nil {
			continue
		}
		if out[i].msg == nil {
			out[i].msg = toolMsg(out[i].call, &ToolResult{Content: "(no output)"})
		}
		if out[i].fail != nil && failErr == nil {
			failErr = out[i].fail
		}
		if out[i].stop && stopCall == nil {
			stopCall = out[i].call
		}
	}
	return out, stopCall, failErr
}

func (a *Agent) execOne(c *Context, call *ToolCall) toolOutcome {
	out := toolOutcome{call: call}
	if call.ID == "" {
		call.ID = newID("call")
	}

	tool, ok := a.Tools.Get(call.Name)
	if !ok {
		res := &ToolResult{
			Content: fmt.Sprintf("tool %q not found; available tools: %s", call.Name, strings.Join(a.Tools.Names(), ", ")),
			IsError: true,
		}
		out.msg = toolMsg(call, res)
		a.emitEvent(c, Event{Type: EvToolEnd, Data: ToolEvent{CallID: call.ID, Name: call.Name, Result: res}, Owner: call.Name})
		return out
	}

	if c.aborted.Load() {
		res := &ToolResult{Content: "(aborted)", IsError: true}
		out.msg = toolMsg(call, res)
		return out
	}

	start := time.Now()
	ev := Event{Type: EvToolStart, Data: ToolEvent{CallID: call.ID, Name: call.Name, Args: call.Arguments}}
	ev.Owner = call.Name
	a.emitEvent(c, ev)

	if d := c.fireToolStart(call, tool); d.Kind != KindNext {
		switch d.Kind {
		case KindFail:
			out.fail = d.Err
			return out
		case KindStop:
			res := &ToolResult{Content: textOr(d.Msg, "(stopped)"), Stop: true}
			out.msg = toolMsg(call, res)
			out.stop = true
			a.emitEvent(c, Event{Type: EvToolEnd, Data: ToolEvent{CallID: call.ID, Name: call.Name, Result: res, Duration: time.Since(start)}, Owner: call.Name})
			return out
		case KindSkip:
			res := &ToolResult{Content: textOr(d.Msg, "(skipped)")}
			out.msg = toolMsg(call, res)
			a.emitEvent(c, Event{Type: EvToolEnd, Data: ToolEvent{CallID: call.ID, Name: call.Name, Result: res, Duration: time.Since(start)}, Owner: call.Name})
			return out
		}
	}

	args := call.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	res, err := safeCall(tool, c, args)
	if err != nil {
		p := err.(*ToolPanic)
		res = &ToolResult{Content: p.Error(), IsError: true}
		out.msg = toolMsg(call, res)
		out.fail = p
		a.emitEvent(c, Event{Type: EvToolEnd, Data: ToolEvent{CallID: call.ID, Name: call.Name, Result: res, Duration: time.Since(start)}, Owner: call.Name})
		return out
	}
	if res == nil {
		res = &ToolResult{}
	}
	if res.Content == "" && len(res.MultiContent) == 0 {
		res.Content = "(no output)"
	}

	if d := c.fireToolEnd(call, res); d.Kind != KindNext {
		switch d.Kind {
		case KindFail:
			out.fail = d.Err
			return out
		case KindSkip:
			res = &ToolResult{Content: "(skipped)", IsError: true}
		case KindStop:
			res.Stop = true
		}
	}

	out.msg = toolMsg(call, res)
	out.stop = res.Stop
	a.emitEvent(c, Event{Type: EvToolEnd, Data: ToolEvent{CallID: call.ID, Name: call.Name, Result: res, Duration: time.Since(start)}, Owner: call.Name})
	return out
}

func toolMsg(call *ToolCall, res *ToolResult) *Message {
	if res == nil {
		res = &ToolResult{}
	}
	m := &Message{
		Role:         RoleTool,
		ToolCallID:   call.ID,
		Name:         call.Name,
		Content:      res.Content,
		MultiContent: res.MultiContent,
	}
	if res.IsError || res.Stop || len(res.Meta) > 0 {
		m.Meta = map[string]any{}
		if res.IsError {
			m.Meta[metaIsError] = true
		}
		if res.Stop {
			m.Meta[metaStop] = true
		}
		for k, v := range res.Meta {
			m.Meta[k] = v
		}
	}
	return m
}

func textOr(m *Message, def string) string {
	if m == nil {
		return def
	}
	if s := m.Text(); s != "" {
		return s
	}
	return def
}
