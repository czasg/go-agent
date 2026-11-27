package ga

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
)

type EventType string

const (
	EvRunStart      EventType = "run_start"
	EvTurnStart     EventType = "turn_start"
	EvModelStart    EventType = "model_start"
	EvTextDelta     EventType = "text_delta"
	EvThinkingDelta EventType = "thinking_delta"
	EvToolDelta     EventType = "tool_delta"
	EvModelEnd      EventType = "model_end"
	EvMessage       EventType = "message"
	EvToolStart     EventType = "tool_start"
	EvToolEnd       EventType = "tool_end"
	EvTurnEnd       EventType = "turn_end"
	EvHook          EventType = "hook"
	EvError         EventType = "error"
	EvRunEnd        EventType = "run_end"
)

const (
	OpAdd     = "add"
	OpReplace = "replace"
	OpRemove  = "remove"
	OpReset   = "reset"
)

type Event struct {
	Seq  int64
	Time time.Time
	Dur  time.Duration
	Type EventType
	Data any

	TraceID string

	RootID   string
	RunID    string
	ParentID string
	Depth    int
	Span     string
	Turn     int
	Owner    string

	Internal bool
}

type RunStartEvent struct {
	Input []*Message
}

type RunEndEvent struct {
	Reason StopReason
	Result *RunResult
}

type TurnEvent struct {
	Turn  int
	Usage Usage
}

type ModelEvent struct {
	MsgCount  int
	ToolCount int
	System    string
	Message   *Message
	Usage     Usage
}

type DeltaEvent struct {
	Text string
}

type ToolDeltaEvent struct {
	CallID    string
	Name      string
	ArgsChunk string
}

type MessageEvent struct {
	Op       string
	Index    int
	Message  *Message
	Messages []*Message
}

type ToolEvent struct {
	CallID   string
	Name     string
	Args     json.RawMessage
	Result   *ToolResult
	Err      error
	Duration time.Duration
}

type HookEvent struct {
	Name   string
	Kind   string
	Reason string
}

type ErrorEvent struct {
	Stage string
	Err   error
}

type Emitter interface {
	Emit(Event)
}

type EmitterFunc func(Event)

func (f EmitterFunc) Emit(e Event) {
	if f != nil {
		f(e)
	}
}

func Redacting(inner Emitter, fn func(*Event)) Emitter {
	return EmitterFunc(func(e Event) {
		if fn != nil {
			fn(&e)
		}
		if inner != nil {
			inner.Emit(e)
		}
	})
}

type seqCounter struct {
	n atomic.Int64
}

func (s *seqCounter) next() int64 {
	return s.n.Add(1)
}

type traceKey struct{}

func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey{}, id)
}

func TraceIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}

func kindName(k Kind) string {
	switch k {
	case KindSkip:
		return "skip"
	case KindStop:
		return "stop"
	case KindRedo:
		return "redo"
	case KindFail:
		return "fail"
	default:
		return "next"
	}
}
