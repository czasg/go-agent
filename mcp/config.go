package mcp

import (
	"errors"
	"fmt"
)

// Transport 是 MCP server 的传输方式。本包只支持两种远程形态——
// SSE 与 Streamable HTTP，不支持 stdio（子进程）。
type Transport string

const (
	// TransportSSE 走 Server-Sent Events（长连接 + 事件流）。
	TransportSSE Transport = "sse"
	// TransportStreamableHTTP 走 MCP 的 Streamable HTTP（较新的默认远程传输）。
	TransportStreamableHTTP Transport = "streamable_http"
)

// NameMode 决定 MCP 工具暴露给模型时的名字策略。
//
// 多个 MCP server 很容易出现同名工具（都叫 search），也可能与框架内置工具撞名。
// 由于 ga 的 ToolStore 以工具名为 key，后注册的会覆盖先注册的——撞名等于静默丢工具。
// 用一个枚举把三种命名策略收口在 Config.toolName 一处，调用方只填枚举即可。
type NameMode int

const (
	// NameRaw 保持 MCP 原始工具名。最简单，单 server 场景够用；多 server 有撞名风险。
	NameRaw NameMode = iota
	// NameServerPref 用 Config.Name 作前缀：<Name>__<tool>。多 server 时来源清晰、天然隔离。
	NameServerPref
	// NameCustomPref 用 Config.Prefix 作前缀：<Prefix>__<tool>。前缀与 server 逻辑名解耦。
	NameCustomPref
)

// prefixSep 是前缀与原始工具名之间的分隔符。
// 选 "__"：OpenAI/Anthropic 的函数名规则（^[a-zA-Z0-9_-]+$）都接受下划线，且双下划线
// 在原始工具名里极少出现，反解析歧义小、肉眼也好读。
const prefixSep = "__"

// Config 是单个 MCP server 的连接配置（本包的粒度是「单 Client 单 server」）。
type Config struct {
	// Name 是 server 的逻辑名，用于日志、以及 NameServerPref 命名策略的前缀。
	Name string
	// Transport 传输方式：TransportSSE 或 TransportStreamableHTTP。
	Transport Transport
	// Endpoint 是 server 地址，如 https://host/mcp（HTTP）或 https://host/sse（SSE）。
	Endpoint string
	// Headers 是每次请求附带的自定义 HTTP 头（如 Authorization 鉴权）。可为空。
	Headers map[string]string
	// NameMode 工具命名策略，默认 NameRaw。
	NameMode NameMode
	// Prefix 仅在 NameMode == NameCustomPref 时使用。
	Prefix string
}

// toolName 按命名策略把 MCP 原始工具名 raw 转成对模型暴露的名字。
// 注意：这是「对外名」，真正 CallTool 时仍要用 raw（见 client.go 的 mcpTool）。
func (c *Config) toolName(raw string) string {
	switch c.NameMode {
	case NameServerPref:
		return c.Name + prefixSep + raw
	case NameCustomPref:
		return c.Prefix + prefixSep + raw
	default: // NameRaw
		return raw
	}
}

// validate 在建立连接前校验配置，尽早把配置错误暴露在构造期。
func (c *Config) validate() error {
	if c.Endpoint == "" {
		return errors.New("mcp: Config.Endpoint 不能为空")
	}
	switch c.Transport {
	case TransportSSE, TransportStreamableHTTP:
	case "":
		return errors.New("mcp: Config.Transport 不能为空（sse 或 streamable_http）")
	default:
		return fmt.Errorf("mcp: 不支持的 Transport %q（仅支持 sse / streamable_http）", c.Transport)
	}
	if c.NameMode == NameCustomPref && c.Prefix == "" {
		return errors.New("mcp: NameMode=NameCustomPref 时 Config.Prefix 不能为空")
	}
	if c.NameMode == NameServerPref && c.Name == "" {
		return errors.New("mcp: NameMode=NameServerPref 时 Config.Name 不能为空")
	}
	return nil
}
