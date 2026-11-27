package ga

import (
	"fmt"
	"reflect"
)

type Kind int

const (
	KindNext Kind = iota
	KindSkip
	KindStop
	KindRedo
	KindFail
)

type Decision struct {
	Kind   Kind
	Msg    *Message
	Err    error
	Reason string
}

func (d Decision) stops() bool {
	return d.Kind == KindStop || d.Kind == KindFail
}

func Next() Decision {
	return Decision{Kind: KindNext}
}

func SkipWith(m *Message) Decision {
	return Decision{Kind: KindSkip, Msg: m}
}

func SkipText(s string) Decision {
	return Decision{Kind: KindSkip, Msg: UserMessage(s)}
}

func Stop(msg *Message) Decision {
	return Decision{Kind: KindStop, Msg: msg}
}

func Redo() Decision {
	return Decision{Kind: KindRedo}
}

func Fail(err error) Decision {
	return Decision{Kind: KindFail, Err: err}
}

type RunStartHook func(*Context)

type TurnStartHook func(*Context) Decision

type ModelStartHook func(*Context, *ModelRequest) Decision

type ModelEndHook func(*Context, *ModelResponse) Decision

type ToolStartHook func(*Context, *ToolCall, *Tool) Decision

type ToolEndHook func(*Context, *ToolCall, *ToolResult) Decision

type TurnEndHook func(*Context) Decision

type ShouldStopHook func(*Context) Decision

type MaxTurnsHook func(*Context) Decision

type ErrorHook func(*Context, error) Decision

type RunEndHook func(*Context, StopReason)

type Hooks struct {
	RunStart   []RunStartHook
	TurnStart  []TurnStartHook
	ModelStart []ModelStartHook
	ModelEnd   []ModelEndHook
	ToolStart  []ToolStartHook
	ToolEnd    []ToolEndHook
	TurnEnd    []TurnEndHook
	ShouldStop []ShouldStopHook
	MaxTurns   []MaxTurnsHook
	Error      []ErrorHook
	RunEnd     []RunEndHook
}

func grow[T any](s []T, add ...T) []T {
	return append(s[:len(s):len(s)], add...)
}

func NewHooks(hooks ...any) Hooks {
	return Hooks{}.Use(hooks...)
}

func (h Hooks) Use(plugins ...any) Hooks {
	for _, p := range plugins {
		if p == nil {
			continue
		}
		rv := reflect.ValueOf(p)
		switch rv.Kind() {
		case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
			if rv.IsNil() {
				continue
			}
		}
		h = h.useOne(p)
	}
	return h
}

func (h Hooks) useOne(p any) Hooks {
	switch x := p.(type) {
	case Hooks:
		return h.merge(x)
	case *Hooks:
		return h.merge(*x)
	case RunStartHook:
		h.RunStart = grow(h.RunStart, x)
		return h
	case TurnStartHook:
		h.TurnStart = grow(h.TurnStart, x)
		return h
	case ModelStartHook:
		h.ModelStart = grow(h.ModelStart, x)
		return h
	case ModelEndHook:
		h.ModelEnd = grow(h.ModelEnd, x)
		return h
	case ToolStartHook:
		h.ToolStart = grow(h.ToolStart, x)
		return h
	case ToolEndHook:
		h.ToolEnd = grow(h.ToolEnd, x)
		return h
	case TurnEndHook:
		h.TurnEnd = grow(h.TurnEnd, x)
		return h
	case ShouldStopHook:
		h.ShouldStop = grow(h.ShouldStop, x)
		return h
	case MaxTurnsHook:
		h.MaxTurns = grow(h.MaxTurns, x)
		return h
	case ErrorHook:
		h.Error = grow(h.Error, x)
		return h
	case RunEndHook:
		h.RunEnd = grow(h.RunEnd, x)
		return h
	}

	matched := false
	if x, ok := p.(interface{ OnRunStart(*Context) }); ok {
		h.RunStart = grow(h.RunStart, x.OnRunStart)
		matched = true
	}
	if x, ok := p.(interface {
		OnTurnStart(*Context) Decision
	}); ok {
		h.TurnStart = grow(h.TurnStart, x.OnTurnStart)
		matched = true
	}
	if x, ok := p.(interface {
		OnModelStart(*Context, *ModelRequest) Decision
	}); ok {
		h.ModelStart = grow(h.ModelStart, x.OnModelStart)
		matched = true
	}
	if x, ok := p.(interface {
		OnModelEnd(*Context, *ModelResponse) Decision
	}); ok {
		h.ModelEnd = grow(h.ModelEnd, x.OnModelEnd)
		matched = true
	}
	if x, ok := p.(interface {
		OnToolStart(*Context, *ToolCall, *Tool) Decision
	}); ok {
		h.ToolStart = grow(h.ToolStart, x.OnToolStart)
		matched = true
	}
	if x, ok := p.(interface {
		OnToolEnd(*Context, *ToolCall, *ToolResult) Decision
	}); ok {
		h.ToolEnd = grow(h.ToolEnd, x.OnToolEnd)
		matched = true
	}
	if x, ok := p.(interface{ OnTurnEnd(*Context) Decision }); ok {
		h.TurnEnd = grow(h.TurnEnd, x.OnTurnEnd)
		matched = true
	}
	if x, ok := p.(interface{ OnShouldStop(*Context) Decision }); ok {
		h.ShouldStop = grow(h.ShouldStop, x.OnShouldStop)
		matched = true
	}
	if x, ok := p.(interface{ OnMaxTurns(*Context) Decision }); ok {
		h.MaxTurns = grow(h.MaxTurns, x.OnMaxTurns)
		matched = true
	}
	if x, ok := p.(interface {
		OnError(*Context, error) Decision
	}); ok {
		h.Error = grow(h.Error, x.OnError)
		matched = true
	}
	if x, ok := p.(interface{ OnRunEnd(*Context, StopReason) }); ok {
		h.RunEnd = grow(h.RunEnd, x.OnRunEnd)
		matched = true
	}
	if !matched {
		panic(fmt.Sprintf("ga: %T 没有实现任何 On 方法；单阶段函数请写成 ga.ModelStartHook(fn) 或 ga.Hooks{}.OnModelStart(fn)", p))
	}
	return h
}

func (h Hooks) merge(other Hooks) Hooks {
	h.RunStart = grow(h.RunStart, other.RunStart...)
	h.TurnStart = grow(h.TurnStart, other.TurnStart...)
	h.ModelStart = grow(h.ModelStart, other.ModelStart...)
	h.ModelEnd = grow(h.ModelEnd, other.ModelEnd...)
	h.ToolStart = grow(h.ToolStart, other.ToolStart...)
	h.ToolEnd = grow(h.ToolEnd, other.ToolEnd...)
	h.TurnEnd = grow(h.TurnEnd, other.TurnEnd...)
	h.ShouldStop = grow(h.ShouldStop, other.ShouldStop...)
	h.MaxTurns = grow(h.MaxTurns, other.MaxTurns...)
	h.Error = grow(h.Error, other.Error...)
	h.RunEnd = grow(h.RunEnd, other.RunEnd...)
	return h
}

func (h Hooks) OnRunStart(fns ...RunStartHook) Hooks {
	h.RunStart = grow(h.RunStart, fns...)
	return h
}

func (h Hooks) OnTurnStart(fns ...TurnStartHook) Hooks {
	h.TurnStart = grow(h.TurnStart, fns...)
	return h
}

func (h Hooks) OnModelStart(fns ...ModelStartHook) Hooks {
	h.ModelStart = grow(h.ModelStart, fns...)
	return h
}

func (h Hooks) OnModelEnd(fns ...ModelEndHook) Hooks {
	h.ModelEnd = grow(h.ModelEnd, fns...)
	return h
}

func (h Hooks) OnToolStart(fns ...ToolStartHook) Hooks {
	h.ToolStart = grow(h.ToolStart, fns...)
	return h
}

func (h Hooks) OnToolEnd(fns ...ToolEndHook) Hooks {
	h.ToolEnd = grow(h.ToolEnd, fns...)
	return h
}

func (h Hooks) OnTurnEnd(fns ...TurnEndHook) Hooks {
	h.TurnEnd = grow(h.TurnEnd, fns...)
	return h
}

func (h Hooks) OnShouldStop(fns ...ShouldStopHook) Hooks {
	h.ShouldStop = grow(h.ShouldStop, fns...)
	return h
}

func (h Hooks) OnMaxTurns(fns ...MaxTurnsHook) Hooks {
	h.MaxTurns = grow(h.MaxTurns, fns...)
	return h
}

func (h Hooks) OnError(fns ...ErrorHook) Hooks {
	h.Error = grow(h.Error, fns...)
	return h
}

func (h Hooks) OnRunEnd(fns ...RunEndHook) Hooks {
	h.RunEnd = grow(h.RunEnd, fns...)
	return h
}

func (h Hooks) Clone() Hooks {
	out := Hooks{}
	out.RunStart = append([]RunStartHook(nil), h.RunStart...)
	out.TurnStart = append([]TurnStartHook(nil), h.TurnStart...)
	out.ModelStart = append([]ModelStartHook(nil), h.ModelStart...)
	out.ModelEnd = append([]ModelEndHook(nil), h.ModelEnd...)
	out.ToolStart = append([]ToolStartHook(nil), h.ToolStart...)
	out.ToolEnd = append([]ToolEndHook(nil), h.ToolEnd...)
	out.TurnEnd = append([]TurnEndHook(nil), h.TurnEnd...)
	out.ShouldStop = append([]ShouldStopHook(nil), h.ShouldStop...)
	out.MaxTurns = append([]MaxTurnsHook(nil), h.MaxTurns...)
	out.Error = append([]ErrorHook(nil), h.Error...)
	out.RunEnd = append([]RunEndHook(nil), h.RunEnd...)
	return out
}

func (h Hooks) Count() int {
	return len(h.RunStart) + len(h.TurnStart) + len(h.ModelStart) + len(h.ModelEnd) +
		len(h.ToolStart) + len(h.ToolEnd) + len(h.TurnEnd) + len(h.ShouldStop) +
		len(h.MaxTurns) + len(h.Error) + len(h.RunEnd)
}
