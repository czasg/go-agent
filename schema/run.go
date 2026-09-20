package schema

// StopReason loop 终止原因。
type StopReason string

const (
	StopCompleted    StopReason = "completed"      // 模型正常结束（无 tool calls）
	StopMaxIteration StopReason = "max_iterations" // 达到最大迭代次数
	StopAborted      StopReason = "aborted"        // ctx.Abort() 手动终止
	StopError        StopReason = "error"          // 运行出错
	StopCancelled    StopReason = "cancelled"      // context 取消
)

// RunResult 一次 Run 的完整结果。
type RunResult struct {
	Messages   []*Message `json:"messages"`        // 完整对话历史（本次 Run 后）
	Message    *Message   `json:"message"`         // 最后一条消息
	Model      string     `json:"model,omitempty"` // 模型名称
	Usage      TokenUsage `json:"usage"`           // 本次 Run 累计 token 用量（多迭代累加）
	Timing     Timing     `json:"timing"`          // 本次 Run 总耗时
	Iterations int        `json:"iterations"`      // 实际迭代次数
	StopReason StopReason `json:"stop_reason"`     // 终止原因
	Error      string     `json:"error,omitempty"` // 错误信息（StopError 时）
}

// TokenUsage token 用量统计。
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`              // 输入 token 数（模型看到的完整上下文）
	CompletionTokens int `json:"completion_tokens"`          // 输出 token 数（模型生成的内容）
	TotalTokens      int `json:"total_tokens"`               // 输入 + 输出
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"` // 思考 token
	CachedTokens     int `json:"cached_tokens"`              // 命中缓存的输入 token
}

// Add 累加另一份用量。
func (u *TokenUsage) Add(other TokenUsage) {
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.CachedTokens += other.CachedTokens
}

// Timing 耗时统计。
type Timing struct {
	TotalDuration     int64 `json:"total_duration"`
	TimeToFirstToken  int64 `json:"time_to_first_token"`
	ReasoningDuration int64 `json:"reasoning_duration"`
	ContentDuration   int64 `json:"content_duration"`
}
