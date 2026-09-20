package ga

import "github.com/czasg/go-agent/schema"

// Re-export commonly used schema types so ext packages only need to import ga.

type RunResult = schema.RunResult
type Message = schema.Message
type TokenUsage = schema.TokenUsage
type StopReason = schema.StopReason
type RoleType = schema.RoleType

const (
	StopCompleted    = schema.StopCompleted
	StopMaxIteration = schema.StopMaxIteration
	StopAborted      = schema.StopAborted
	StopError        = schema.StopError
	StopCancelled    = schema.StopCancelled

	Assistant = schema.Assistant
	User      = schema.User
	System    = schema.System
	Tool      = schema.Tool
)