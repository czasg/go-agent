// 手写 BaseTool 接口 —— 展示工具的最底层定义。
//
// 展示：
//   - 实现 ga.BaseTool 接口（Info + Execute）
//   - 手写 JSON Schema、手动解析参数
//   - WithTools 注册工具，Agent loop 自动调用
//
// 对比 04-typed-tool：那里用 NewTool[T] 泛型省掉了这些样板。
//
// 运行：
//
//	go run ./examples/03-custom-tool
package main

import (
	"context"
	"fmt"
	"log"
	"math"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/harness/console"
	"github.com/czasg/go-agent/config"
	"github.com/czasg/go-agent/schema"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt("你是一个数学助手，可以使用 calculator 工具进行计算。"),
		ga.WithTools(&calcTool{}),
	)

	app := console.New(console.WithAgent(agent))
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// ── 手写 BaseTool ───────────────────────────────────────────────

// calcTool 实现 ga.BaseTool，手动定义 schema 和参数解析。
type calcTool struct{}

func (*calcTool) Info() schema.ToolInfo {
	return schema.ToolInfo{
		Name:        "calculator",
		Description: "执行数学运算：加减乘除、幂运算、开方",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{
					"type":        "string",
					"description": "运算类型",
					"enum":        []string{"add", "sub", "mul", "div", "pow", "sqrt"},
				},
				"a": map[string]any{
					"type":        "number",
					"description": "第一个操作数",
				},
				"b": map[string]any{
					"type":        "number",
					"description": "第二个操作数（sqrt 时忽略）",
				},
			},
			"required":             []string{"operation", "a"},
			"additionalProperties": false,
		},
	}
}

func (*calcTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Operation string  `json:"operation"`
		A         float64 `json:"a"`
		B         float64 `json:"b"`
	}
	if err := call.JSON(&in); err != nil {
		return "", err
	}

	var result float64
	switch in.Operation {
	case "add":
		result = in.A + in.B
	case "sub":
		result = in.A - in.B
	case "mul":
		result = in.A * in.B
	case "div":
		if in.B == 0 {
			return "", fmt.Errorf("division by zero")
		}
		result = in.A / in.B
	case "pow":
		result = math.Pow(in.A, in.B)
	case "sqrt":
		if in.A < 0 {
			return "", fmt.Errorf("sqrt of negative number")
		}
		result = math.Sqrt(in.A)
	default:
		return "", fmt.Errorf("unknown operation: %s", in.Operation)
	}

	return fmt.Sprintf("%g", result), nil
}
