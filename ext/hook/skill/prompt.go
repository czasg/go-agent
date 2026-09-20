package skill

import "fmt"

const (
	// defaultSystemPromptTpl 注入到 Agent system prompt，告诉模型 skill 能力存在。
	// %s 为 skills 目录路径，避免模型盲目猜测。
	defaultSystemPromptTpl = `# skills
When users ask you to perform tasks, check if any available skills can help. Call 'skill' tool to load skill instructions.
if user ask your power(能力)/skill(技能), you should check you tool description.
Skills directory: %s`

	// toolDescriptionBase 是 skill 工具描述的静态前缀。
	toolDescriptionBase = `Load and execute a skill by name.`
)

// buildSystemPrompt 动态构建 system prompt，注入 skills 目录路径。
func buildSystemPrompt(dir string) string {
	return fmt.Sprintf(defaultSystemPromptTpl, dir)
}
