// Package toolsearch 提供工具搜索 Hook：在 Agent 启动时注入 tool_search 元工具，
// 让模型通过正则匹配按需发现并加载工具，避免一次性暴露过多工具撑爆上下文。
//
// 原理：
//  1. OnAgentStart：注入 tool_search，设置白名单（preAllowed + tool_search）
//  2. 模型调用 tool_search(regex) → 搜索全量工具，匹配到的立刻加白并返回摘要
//  3. 下一轮模型调用时就能看到并使用这些工具
//
// 用法：
//
//	agent := ga.NewAgent(
//	    ga.WithChatModel(model),
//	    ga.WithTools(allTools...),                      // 业务侧注册全部工具
//	    ga.WithHooks(toolsearch.New("tool_a", "tool_b")),  // 这些工具直接加白
//	)
package toolsearch

import (
	"encoding/json"
	"fmt"
	"regexp"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

// New 创建 toolsearch Hook。
// preAllowed 是一开始就对模型可见的工具名称，其余工具需要通过 tool_search 发现后才可用。
func New(preAllowed ...string) *Hook {
	return &Hook{
		preAllowed: preAllowed,
		allowed:    make(map[string]bool),
	}
}

// Hook 实现 AgentStartHook + IterationStartHook。
type Hook struct {
	preAllowed []string
	allowed    map[string]bool // 通过 tool_search 发现并已加白的工具名
}

func (h *Hook) Name() string { return "tool_search" }

// OnAgentStart 注入 tool_search 元工具，设置初始白名单：preAllowed + tool_search。
func (h *Hook) OnAgentStart(c *ga.Context) {
	c.Tools.Register(&toolSearchTool{})

	initial := make([]string, 0, len(h.preAllowed)+1)
	initial = append(initial, toolSearchToolName)
	initial = append(initial, h.preAllowed...)
	c.Tools.Allow(initial...)

	h.allowed = make(map[string]bool)
}

// OnIterationStart 扫描历史消息中 tool_search 的返回结果，把匹配到的工具加白。
func (h *Hook) OnIterationStart(c *ga.Context) {
	for _, name := range extractSelectedTools(c.Messages.Raw()) {
		if h.allowed[name] {
			continue
		}
		h.allowed[name] = true
		c.Tools.Allow(name)
	}
}

// ── tool_search 元工具 ────────────────────────────────────────────

const toolSearchToolName = "tool_search"

var toolSearchToolInfo = schema.ToolInfo{
	Name: toolSearchToolName,
	Description: "搜索可用工具。传入正则表达式，匹配工具名称和描述，返回匹配到的工具摘要。" +
		"当你需要某个功能但当前没有对应工具时使用。" +
		"匹配到的工具会在下一轮自动可用。",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "正则表达式，同时匹配工具名称和描述（不区分大小写）",
			},
		},
		"required": []string{"pattern"},
	},
}

type toolSearchTool struct{}

func (t *toolSearchTool) Info() schema.ToolInfo { return toolSearchToolInfo }

func (t *toolSearchTool) Execute(c *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Pattern string `json:"pattern"`
	}
	if err := call.JSON(&in); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if in.Pattern == "" {
		return "", fmt.Errorf("pattern 不能为空")
	}

	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return "", fmt.Errorf("无效的正则表达式: %w", err)
	}

	var matched []ToolMatch
	for _, name := range c.Tools.Names() {
		if name == toolSearchToolName {
			continue
		}
		tool, _ := c.Tools.Get(name)
		info := tool.Info()
		if re.MatchString(info.Name) || re.MatchString(info.Description) {
			matched = append(matched, ToolMatch{Name: info.Name, Description: info.Description})
			c.Tools.Allow(name) // 匹配到立刻加白
		}
	}

	data, _ := json.Marshal(SearchResult{MatchedTools: matched, Total: len(matched)})
	return string(data), nil
}

// ── 返回值结构体 ────────────────────────────────────────────────

// SearchResult tool_search 的返回结果。
type SearchResult struct {
	MatchedTools []ToolMatch `json:"matchedTools"`
	Total        int         `json:"total"`
}

// ToolMatch 单个匹配到的工具摘要。只返回名称和描述，不返回完整 schema。
type ToolMatch struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ── 辅助函数 ──────────────────────────────────────────────────

// extractSelectedTools 从历史消息中提取所有 tool_search 返回过的工具名。
func extractSelectedTools(messages []*schema.Message) []string {
	var names []string
	for _, msg := range messages {
		if msg.Role != schema.Tool || msg.ToolName != toolSearchToolName {
			continue
		}
		var result SearchResult
		if err := json.Unmarshal([]byte(msg.Content), &result); err != nil {
			continue
		}
		for _, m := range result.MatchedTools {
			names = append(names, m.Name)
		}
	}
	return names
}
