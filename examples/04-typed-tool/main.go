// NewTool[T] 泛型工具 —— 对比 03-custom-tool 的手写方式。
//
// 展示：
//   - ga.NewTool[T] 从结构体 tag 自动生成 JSON Schema
//   - 参数自动反序列化为类型化结构体，无需手动 json.Unmarshal
//   - ToolFunc[T] 纯函数签名，只关心业务逻辑
//
// 运行：
//
//	go run ./examples/04-typed-tool
package main

import (
	"context"
	"fmt"
	"log"
	"math"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/harness/console"
	"github.com/czasg/go-agent/config"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	// 用 NewTool[T] 构造：schema 从 CalcParams 的 tag 反射生成。
	calcTool, err := ga.NewTool("calculator",
		"执行数学运算：加减乘除、幂运算、开方",
		calculate,
	)
	if err != nil {
		panic(err)
	}

	agent := ga.NewAgent(
		ga.WithChatModel(chatModel),
		ga.WithSystemPrompt("你是一个数学助手，可以使用 calculator 工具进行计算。"),
		ga.WithTools(calcTool),
	)

	app := console.New(console.WithAgent(agent))
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// ── 类型化工具 ──────────────────────────────────────────────────

// CalcParams 的 tag 自动生成 JSON Schema：
//   - json tag 定义字段名
//   - jsonschema tag 定义 description
//   - omitempty 控制 required
type CalcParams struct {
	Operation string  `json:"operation" jsonschema:"description=运算类型,enum=add,enum=sub,enum=mul,enum=div,enum=pow,enum=sqrt"`
	A         float64 `json:"a"          jsonschema:"description=第一个操作数"`
	B         float64 `json:"b,omitempty" jsonschema:"description=第二个操作数（sqrt 时忽略）"`
}

// calculate 是纯业务函数：拿到类型化入参，返回结果。
// 没有 Info() 样板，没有 json.Unmarshal，没有 schema 手写。
func calculate(_ *ga.Context, in CalcParams) (string, error) {
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
