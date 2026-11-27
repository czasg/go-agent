package ga

import (
	"errors"
	"fmt"
)

type StopReason string

const (
	StopNone     StopReason = ""
	StopNoTool   StopReason = "no_tool_calls"
	StopByHook   StopReason = "hook_stop"
	StopByTool   StopReason = "tool_stop"
	StopMaxTurns StopReason = "max_turns"
	StopError    StopReason = "error"
	StopCanceled StopReason = "canceled"
)

type RunError struct {
	Stage string
	Err   error
	Turn  int
	Span  string
}

func (e *RunError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Span != "" {
		return fmt.Sprintf("ga: %s at %s (turn %d): %v", e.Stage, e.Span, e.Turn, e.Err)
	}
	return fmt.Sprintf("ga: %s (turn %d): %v", e.Stage, e.Turn, e.Err)
}

func (e *RunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

var ErrBusy = errors.New("ga: agent is already running")
