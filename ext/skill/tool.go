package skill

import (
	"fmt"
	"path/filepath"
	"strings"

	ga "github.com/czasg/go-agent"
)

type args struct {
	Name string `json:"name,omitempty" desc:"技能名；省略则列出全部技能"`
	File string `json:"file,omitempty" desc:"技能目录内的相对路径；省略则加载技能正文，传目录则列出其中文件"`
}

func (r *Registry) buildTool() *ga.Tool {
	return ga.NewTool(r.toolName, toolDescription(r.toolName),
		func(c *ga.Context, in args) *ga.ToolResult {
			return r.execute(in)
		})
}

func toolDescription(name string) string {
	return "按需加载技能：技能是按名字组织的任务说明书，只在任务与描述匹配时加载，不要凭名字猜测内容。" +
		name + "() 列出全部技能（名字 + 描述）；" +
		name + "(name=\"pdf-processing\") 加载技能正文，并返回技能目录内的资源清单与读取方式；" +
		name + "(name=\"pdf-processing\", file=\"references/TABLES.md\") 读取技能目录内的资源文件，传目录则列出其中文件。" +
		"技能目录内的脚本用终端执行其绝对路径，正文会给出目录。"
}

func (r *Registry) execute(in args) *ga.ToolResult {
	name := strings.TrimSpace(in.Name)
	file := strings.TrimSpace(in.File)

	if name == "" {
		if file != "" {
			return ga.Result("file 需要配合 name 使用：先指定技能名，再读取该技能目录内的资源").AsErr()
		}
		return ga.Result(r.listText())
	}

	s, ok := r.Get(name)
	if !ok {
		return ga.Result(fmt.Sprintf("技能不存在：%s\n可用技能：%s", name, r.available())).AsErr()
	}
	if file == "" {
		return r.loadText(s)
	}
	return r.readResource(s, file)
}

func (r *Registry) available() string {
	names := r.Names()
	if len(names) == 0 {
		return "（无）"
	}
	return strings.Join(names, "、")
}

func (r *Registry) listText() string {
	skills := r.List()
	if len(skills) == 0 {
		return "暂无可用技能。技能根目录下放置 <name>/SKILL.md 后即可被发现。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个技能：\n", len(skills))
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	fmt.Fprintf(&b, "\n加载正文：%s(name=\"<技能名>\")", r.toolName)
	return b.String()
}

func (r *Registry) loadText(s Skill) *ga.ToolResult {
	var b strings.Builder
	fmt.Fprintf(&b, "Loaded skill: %s\n", s.Name)
	if s.BaseDirectory == "" {
		b.WriteString("Base directory for this skill: （无目录，编程式注入）\n")
	} else {
		fmt.Fprintf(&b, "Base directory for this skill: %s\n", s.BaseDirectory)
	}
	if s.License != "" {
		fmt.Fprintf(&b, "License: %s\n", s.License)
	}
	if s.Compatibility != "" {
		fmt.Fprintf(&b, "Compatibility: %s\n", s.Compatibility)
	}
	if len(s.AllowedTools) > 0 {
		fmt.Fprintf(&b, "Allowed tools: %s\n", strings.Join(s.AllowedTools, ", "))
	}
	b.WriteString("\n")
	b.WriteString(s.Content)

	if s.Truncated {
		fmt.Fprintf(&b, "\n\n[正文超过 %d 字符上限已截断", r.maxBody)
		if s.BaseDirectory != "" {
			fmt.Fprintf(&b, "，全文见 %s，可用 %s(name=%q, file=%q) 读取", filepath.Join(s.BaseDirectory, skillFileName), r.toolName, s.Name, skillFileName)
		}
		b.WriteString("]")
	}

	if s.BaseDirectory == "" {
		return ga.Result(b.String()).WithMeta("skill", s.Name)
	}
	if listing := resourceList(s.BaseDirectory); listing != "" {
		b.WriteString("\n\n## 技能目录资源\n")
		b.WriteString(listing)
		fmt.Fprintf(&b, "\n\n读取资源：%s(name=%q, file=\"<相对路径>\")", r.toolName, s.Name)
	}
	return ga.Result(b.String()).WithMeta("skill", s.Name)
}
