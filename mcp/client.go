// Package mcp 把一个远程 MCP server 接入 ga 框架。
//
// 设计出发点：ga 对「工具」的唯一认识是 ga.BaseTool（Info + Execute），而
// schema.ToolInfo.Parameters 恰好就是原始 JSON Schema——MCP 的 tool.InputSchema
// 也是 JSON Schema，两者 1:1 对应。于是把每个 MCP 远程工具适配成 ga.BaseTool 后，
// 可零改动流经 ga.WithTools → 引擎 → 模型的全链路，引擎完全不知道它是「远程的」。
//
// 粒度：一个 Client 对应一个 MCP server（单 Client 单 server）。多 server 场景由
// 调用方各建一个 Client，再把它们的 Tools() append 到一起喂给 ga.WithTools。
//
// 用法：
//
//	mc, err := mcp.New(mcp.Config{
//	    Name:      "fs",
//	    Transport: mcp.TransportStreamableHTTP,
//	    Endpoint:  "http://localhost:8080/mcp",
//	    NameMode:  mcp.NameServerPref, // 工具名变成 fs__read / fs__write ...
//	})
//	if err != nil { ... }
//	if err := mc.Init(ctx); err != nil { ... }
//	defer mc.Close()
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithTools(mc.Tools()...), // 多 server 就 append 多个 mc.Tools()
//	)
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// clientInfo 是握手时上报给 server 的客户端身份（仅信息性）。
const (
	clientName    = "ga-mcp"
	clientVersion = "0.1.0"
)

// Client 是单个 MCP server 的连接句柄：建立连接、拉取工具、把工具适配成 ga.BaseTool。
type Client struct {
	cfg   Config
	cli   *mcpclient.Client // 底层 mcp-go 客户端
	tools []ga.BaseTool     // Init 成功后缓存的适配工具
}

// New 按 config 构造底层 mcp-go client（据 Transport 分派 + 注入 Headers），
// 但不发起任何网络请求——连接与握手都在 Init 里做。
func New(cfg Config) (*Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	var (
		cli *mcpclient.Client
		err error
	)
	switch cfg.Transport {
	case TransportSSE:
		var opts []transport.ClientOption
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHeaders(cfg.Headers))
		}
		cli, err = mcpclient.NewSSEMCPClient(cfg.Endpoint, opts...)
	case TransportStreamableHTTP:
		var opts []transport.StreamableHTTPCOption
		if len(cfg.Headers) > 0 {
			opts = append(opts, transport.WithHTTPHeaders(cfg.Headers))
		}
		cli, err = mcpclient.NewStreamableHttpClient(cfg.Endpoint, opts...)
	default:
		// validate 已挡住，这里兜底。
		return nil, fmt.Errorf("mcp: 不支持的 Transport %q", cfg.Transport)
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: 创建 %s 客户端失败: %w", cfg.Transport, err)
	}

	return &Client{cfg: cfg, cli: cli}, nil
}

// Init 一键完成：启动传输 → 握手 Initialize → 拉取工具列表 → 适配为 []ga.BaseTool 缓存。
// ctx 用于整个初始化过程的超时/取消控制。
func (c *Client) Init(ctx context.Context) error {
	if err := c.cli.Start(ctx); err != nil {
		return fmt.Errorf("mcp: 启动传输失败: %w", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: clientName, Version: clientVersion}
	if _, err := c.cli.Initialize(ctx, initReq); err != nil {
		return fmt.Errorf("mcp: 握手失败: %w", err)
	}

	listed, err := c.cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("mcp: 拉取工具列表失败: %w", err)
	}

	tools := make([]ga.BaseTool, 0, len(listed.Tools))
	for i := range listed.Tools {
		t, err := newMCPTool(c.cli, &c.cfg, listed.Tools[i])
		if err != nil {
			return fmt.Errorf("mcp: 适配工具 %q 失败: %w", listed.Tools[i].Name, err)
		}
		tools = append(tools, t)
	}
	c.tools = tools
	return nil
}

// Tools 返回适配好的工具列表（供 ga.WithTools 使用）。Init 之前返回 nil。
func (c *Client) Tools() []ga.BaseTool { return c.tools }

// Close 关闭底层连接。透传 mcp-go 的 Close。
func (c *Client) Close() error {
	if c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// ── mcpTool：把一个 MCP 远程工具适配成 ga.BaseTool ──────────────────
//
// 关键点：对模型暴露的是「对外名」（可能带 server 前缀，避免多 server 撞名），
// 但真正 CallTool 时必须用 rawName（MCP server 认识的原始名）。两者都存在 struct 里。
type mcpTool struct {
	cli     *mcpclient.Client
	rawName string          // MCP 原始工具名，CallTool 时用
	info    schema.ToolInfo // 构造时算好并缓存：Info() 是纯函数、无 error（框架约定）
}

// newMCPTool 从一个 mcp.Tool 构造适配器。InputSchema → JSON Schema map 的转换在这里
// 做一次并缓存进 info，避免每轮 loop 都反射/序列化（ga 每轮都调 Info() 收集工具）。
func newMCPTool(cli *mcpclient.Client, cfg *Config, t mcp.Tool) (ga.BaseTool, error) {
	params, err := inputSchemaToMap(t)
	if err != nil {
		return nil, err
	}
	return &mcpTool{
		cli:     cli,
		rawName: t.Name,
		info: schema.ToolInfo{
			Name:        cfg.toolName(t.Name), // 对外名（可能带前缀）
			Description: t.Description,
			Parameters:  params,
		},
	}, nil
}

func (m *mcpTool) Info() schema.ToolInfo { return m.info }

// Execute 把模型给的原始 args(JSON) 转成 MCP 的 CallTool 请求，执行后把结果文本回吐。
//
//   - 参数通过 call.JSON 反序列化成 map[string]any；空串视作无参（{}）。
//   - Params.Name 用 rawName（不是对外名）——server 只认识原始名。
//   - result.IsError==true 时把文本作为 error 返回：正好落进引擎的「tool error 回喂
//     模型、loop 继续」通道（见 agent.go runTools），不会中断整个 loop。
//   - ctx 就是 *ga.Context，本身实现 context.Context，透传后外部取消能打断远程调用。
func (m *mcpTool) Execute(ctx *ga.Context, call *schema.ToolCall) (string, error) {
	var arguments map[string]any
	if err := call.JSON(&arguments); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = m.rawName
	req.Params.Arguments = arguments

	res, err := m.cli.CallTool(ctx, req)
	if err != nil {
		return "", fmt.Errorf("调用 MCP 工具 %q 失败: %w", m.rawName, err)
	}

	text := contentText(res.Content)
	if res.IsError {
		if text == "" {
			text = "工具执行出错（无详细信息）"
		}
		return "", errors.New(text)
	}
	return text, nil
}

// contentText 把 MCP 返回的多段 Content 里的文本拼成一个字符串。
// 目前只处理 TextContent（覆盖绝大多数工具）；图片/音频/嵌入资源暂不展开，
// 需要时再扩展成结构化返回。
func contentText(contents []mcp.Content) string {
	var b strings.Builder
	for _, ct := range contents {
		if tc, ok := mcp.AsTextContent(ct); ok {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// inputSchemaToMap 把 mcp.Tool 的入参 schema 转成 ga 需要的 map[string]any(JSON Schema)。
// 优先用 RawInputSchema（server 直接给的原始 JSON），否则序列化 InputSchema。
// 无参工具可能得到空 map，补一个最小的 object schema，让各厂商 provider 都能接受。
func inputSchemaToMap(t mcp.Tool) (map[string]any, error) {
	var data []byte
	if len(t.RawInputSchema) > 0 {
		data = t.RawInputSchema
	} else {
		b, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("序列化 InputSchema 失败: %w", err)
		}
		data = b
	}

	out := make(map[string]any)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("解析 InputSchema 失败: %w", err)
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	return out, nil
}
