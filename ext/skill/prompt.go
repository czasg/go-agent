package skill

import (
	"fmt"
	"strings"
)

const defaultPromptTpl = `# 技能
可用技能（名字 + 用途）：
{{skills}}

任务与某个技能匹配时，先调用 {{tool}}(name="...") 加载正文再按正文执行，不要凭描述猜测做法；技能目录内的脚本与参考资料用 {{tool}}(name="...", file="...") 读取。`

const maxIndexDesc = 160

func renderIndex(names []string, skills map[string]Skill) string {
	var b strings.Builder
	for _, name := range names {
		s := skills[name]
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, shortDesc(s.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortDesc(desc string) string {
	runes := []rune(strings.TrimSpace(desc))
	if len(runes) <= maxIndexDesc {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:maxIndexDesc])) + "…"
}

func renderPrompt(tpl, toolName, index string) string {
	if tpl == "" {
		tpl = defaultPromptTpl
	}
	if strings.Contains(tpl, "{{skills}}") {
		tpl = strings.ReplaceAll(tpl, "{{skills}}", index)
	} else if index != "" {
		tpl = strings.TrimRight(tpl, "\n") + "\n\n可用技能（名字 + 用途）：\n" + index
	}
	tpl = strings.ReplaceAll(tpl, "{{tool}}", toolName)
	return strings.TrimSpace(tpl)
}
