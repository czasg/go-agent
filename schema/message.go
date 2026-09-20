package schema

type RoleType string

const (
	Assistant RoleType = "assistant"
	User      RoleType = "user"
	System    RoleType = "system"
	Tool      RoleType = "tool"
)

// Message 对话消息，完整记录，可序列化持久化到数据库。
// 包含对话协议字段（给模型看）和响应元数据（给人看）。
type Message struct {
	Role             RoleType      `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"` // 推理模型思考过程，持久化但不回传
	ToolCalls        []*ToolCall   `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	ToolName         string        `json:"tool_name,omitempty"`
	MultiParts       []ContentPart `json:"multi_parts,omitempty"` // 多模态内容（图片、音频等）

	// ── 响应元数据（Provider 填充，持久化，查看历史时展示）──
	Model        string     `json:"model,omitempty"`         // 使用的模型
	Usage        TokenUsage `json:"usage,omitempty"`         // token 用量
	Timing       Timing     `json:"timing,omitempty"`        // 耗时
	OriginReason string     `json:"origin_reason,omitempty"` // provider 原始结束原因
	Error        string     `json:"error,omitempty"`         // 出错信息（可继续对话）

	// ── 框架级扩展 ──
	Summary   bool           `json:"summary,omitempty"` // 这是一条摘要消息，覆盖之前的消息
	Transient bool           `json:"-"`                 // 瞬态消息，仅当前轮可见，不持久化，下轮自动清除
	Extra     map[string]any `json:"extra,omitempty"`   // 扩展字段（业务层自定义，框架不碰）
}

// ContentPart 多模态内容块。
type ContentPart struct {
	Type     string `json:"type"` // "text" | "image_url" | "audio_url"
	Text     string `json:"text,omitempty"`
	URL      string `json:"url,omitempty"`
	Base64   string `json:"base64,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

// MessageDelta 流式增量。
type MessageDelta struct {
	ReasoningContent string    `json:"reasoning_content,omitempty"`
	Content          string    `json:"content,omitempty"`
	ToolCall         *ToolCall `json:"tool_call,omitempty"`
}

// ── 便捷构造函数 ────────────────────────────────────────────────

// TextPart 构造文本内容块。
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// ImageURLPart 构造图片 URL 内容块。
func ImageURLPart(url string) ContentPart {
	return ContentPart{Type: "image_url", URL: url}
}

// ImageBase64Part 构造 base64 图片内容块。mimeType 如 "image/png"、"image/jpeg"。
func ImageBase64Part(base64Data, mimeType string) ContentPart {
	return ContentPart{Type: "image_url", Base64: base64Data, MIMEType: mimeType}
}

// HasMultiParts 消息是否携带多模态内容。
func (m *Message) HasMultiParts() bool {
	return len(m.MultiParts) > 0
}

// EstimateTokens 粗估本条消息发给 LLM 时的 token 数。
//
// 计算范围：Content + MultiParts + ToolCall 函数名/参数 + 每条 4 token 固定开销。
// 估算方式：UTF-8 字节数 / 4（文本），图片按 85 token 固定估算。
func (m *Message) EstimateTokens() int {
	total := len(m.Content) + 4
	for _, tc := range m.ToolCalls {
		total += len(tc.Function.Name) + len(tc.Function.Arguments)
	}
	for _, mp := range m.MultiParts {
		switch mp.Type {
		case "text":
			total += len(mp.Text)
		case "image_url":
			total += 85 * 4 // 图片粗估 85 token，乘 4 还原成"字节数"再统一 /4
		}
	}
	return total / 4
}
