// Package ask 提供一个"向用户提问并阻塞等待回答"的工具。
//
// 支持结构化问题：每个问题包含标题、选项列表，并可设置单选或多选。
// 一次调用可同时提多个问题（1-4 个），适合"让用户从多个数据中选择"这类场景。
//
// 依赖倒置：工具本体只负责"阻塞等一个答案"，至于"怎么问人"（渲染选项 /
// 弹窗 / 走 WebSocket）和"怎么格式化回答"由 Prompter 接口决定——
// 框架不假设任何一种交互方式，也不限定返回格式。
//
// 业务侧可通过 c.Abort() 主动中断 agent loop（如用户取消），
// 中断后 tool result 为 "[tool result missing]"，loop 终止，
// 下次 session 启动时 messageStore.Fix() 会修补缺失，model 可重新发起。
package ask

import (
	"errors"

	ga "github.com/czasg/go-agent"
)

// Option 单个选项。
type Option struct {
	Label       string `json:"label" jsonschema:"description=选项显示文本"`
	Description string `json:"description,omitempty" jsonschema:"description=选项的补充说明"`
}

// Question 单个问题。
type Question struct {
	Question    string    `json:"question" jsonschema:"description=问题内容"`
	Header      string    `json:"header,omitempty" jsonschema:"description=问题的简短标题，用于前端分组展示"`
	Options     []*Option `json:"options,omitempty" jsonschema:"description=选项列表，至少提供 2 个选项"`
	MultiSelect bool      `json:"multiSelect,omitempty" jsonschema:"description=是否允许多选，false 为单选，true 为多选"`
}

// Prompter 决定"怎么把问题抛给用户、怎么拿回答案"。业务侧实现它。
//
// c 是本次 agent loop 的 Context，业务侧可调用 c.Abort() 主动终止 loop：
//   - 正常回答：return "用户的选择结果", nil
//   - 用户取消：c.Abort(); return "", nil —— loop 终止，不报错
type Prompter interface {
	Ask(c *ga.Context, questions []*Question) (string, error)
}

// Params 是 ask_user 工具的入参。
type Params struct {
	Questions []*Question `json:"questions" jsonschema:"description=问题列表，支持同时提多个问题(1-4个)"`
}

// New 用给定的 Prompter 构造一个 ask_user 工具。
func New(p Prompter) (ga.BaseTool, error) {
	return ga.NewTool("ask_user",
		`向用户提问并等待回复。支持一次提 1-4 个问题，每个问题包含标题、选项列表，并可设置单选或多选。
注意事项：
- 每个问题提供 2-4 个具体的、有实际意义的选项
- 禁止在选项中包含"其他"、"自定义"、"以上都不是"等兜底选项
- 选项应当互斥且覆盖主要场景`,
		func(c *ga.Context, in Params) (string, error) {
			if len(in.Questions) == 0 {
				return "", errors.New("questions 不能为空")
			}
			for _, q := range in.Questions {
				if q.Question == "" {
					return "", errors.New("question 不能为空")
				}
				if len(q.Options) < 2 {
					return "", errors.New("options 至少提供 2 个选项")
				}
			}
			return p.Ask(c, in.Questions)
		})
}