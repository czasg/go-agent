package ask

import (
	"encoding/json"
	"strings"

	ga "github.com/czasg/go-agent"
)

const toolName = "ask"

type Option struct {
	Label       string `json:"label" desc:"选项文本" required:"true"`
	Description string `json:"description,omitempty" desc:"选项的补充说明"`
}

type Question struct {
	Question    string    `json:"question" desc:"问题内容" required:"true"`
	Header      string    `json:"header,omitempty" desc:"问题的简短标题"`
	Options     []*Option `json:"options,omitempty" desc:"选项列表"`
	MultiSelect bool      `json:"multiSelect,omitempty" desc:"是否允许多选"`
}

type Params struct {
	Questions []*Question `json:"questions" desc:"问题列表，1-4 个" required:"true"`
}

const description = `向用户提问并等待回复。支持一次提 1-4 个问题，每个问题包含标题、选项列表，并可设置单选或多选。
注意事项：
- 每个问题提供 2-4 个具体的、有实际意义的选项
- 禁止在选项中包含"其他"、"自定义"、"以上都不是"等兜底选项
- 选项应当互斥且覆盖主要场景`

func New() *ga.Tool {
	return ga.NewTool(toolName, description,
		func(c *ga.Context, in Params) *ga.ToolResult {
			payload, err := json.Marshal(in.Questions)
			if err != nil {
				return ga.Err(err)
			}
			return ga.Result(string(payload)).AsStop()
		})
}

func Parse(res *ga.RunResult) []*Question {
	if res == nil || res.StopTool == nil {
		return nil
	}
	var in Params
	if err := json.Unmarshal(res.StopTool.Arguments, &in); err != nil {
		return nil
	}
	return in.Questions
}

func Reply(a *ga.Agent, res *ga.RunResult, answer string) bool {
	if a == nil || res == nil {
		return false
	}
	if res.StopReason != ga.StopByTool || res.StopTool == nil || res.StopTool.Name != toolName {
		return false
	}
	msg := res.StopMessage()
	if msg == nil {
		return false
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return false
	}
	return a.Update(msg.ID, func(m *ga.Message) {
		m.Content = answer
		m.MultiContent = nil
	})
}
