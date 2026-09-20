package schema

import "encoding/json"

// ToolCall 模型返回的工具调用。
type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`

	// 内部字段
	Result string `json:"-"`
	Error  error  `json:"-"`
}

// JSON 把工具参数反序列化到 v，等价于 json.Unmarshal(call.Function.Arguments, v)。
// 参数为空串时跳过解析（v 保持零值），返回 nil。
func (c *ToolCall) JSON(v any) error {
	if c.Function.Arguments == "" {
		return nil
	}
	return json.Unmarshal([]byte(c.Function.Arguments), v)
}

// Args 返回原始参数 JSON。空串时返回 "{}"，方便直接传给日志或 json.Valid。
func (c *ToolCall) Args() string {
	if c.Function.Arguments == "" {
		return "{}"
	}
	return c.Function.Arguments
}

// FunctionCall 函数调用详情。
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolInfo 通用工具描述，Parameters 直接是 JSON Schema。
// 各厂商 provider 直接透传，无需二次转换。
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}
