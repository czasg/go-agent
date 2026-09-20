package skill

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/schema"
)

const skillToolName = "skill"

// skillTool 实现 BaseTool，作为模型可调用的 skill 加载工具。
// 工具描述动态生成：列出所有可用 skill 的 name + description（轻量 meta）。
// 执行时按需加载完整 skill 指令。
type skillTool struct {
	b   Backend
	ctx context.Context
}

// Info 动态生成工具描述：前缀 + 所有 skill 的 name/description 列表。
func (t *skillTool) Info() schema.ToolInfo {
	var desc string
	if t.b != nil {
		skills, err := t.b.List(t.ctx)
		if err == nil && len(skills) > 0 {
			var buf bytes.Buffer
			buf.WriteString(toolDescriptionBase)
			for _, s := range skills {
				fmt.Fprintf(&buf, "- %s: %s\n", s.Name, s.Description)
			}
			desc = buf.String()
		}
	}
	if desc == "" {
		desc = toolDescriptionBase
	}

	return schema.ToolInfo{
		Name:        skillToolName,
		Description: desc,
		Parameters:  schema.MustParameters(skillInput{}),
	}
}

// Execute 处理模型的 skill 调用：解析参数，按名称加载完整 skill 内容返回。
func (t *skillTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in skillInput
	if err := call.JSON(&in); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if in.Skill == "" {
		return "", fmt.Errorf("skill 参数不能为空")
	}

	skill, err := t.b.Get(t.ctx, in.Skill)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "Launching skill: %s\n", skill.Name)
	fmt.Fprintf(&buf, "Base directory for this skill: %s\n\n", skill.BaseDirectory)
	buf.WriteString(skill.Content)
	return buf.String(), nil
}

// skillInput 是 skill 工具的入参。
type skillInput struct {
	Skill string `json:"skill"         jsonschema:"description=The skill name to load"`
}
