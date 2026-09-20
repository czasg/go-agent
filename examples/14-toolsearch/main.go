// 工具搜索（toolsearch）—— 展示按需发现工具能力。
//
// 注册了 5 个工具，但只有 calculator 直接对模型可见。
// 其余工具（天气、股票、汇率、翻译）需要模型通过 tool_search 发现后才能使用。
//
// 运行：
//
//	go run ./examples/14-toolsearch
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"time"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/ext/hook/toolsearch"
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
		ga.WithSystemPrompt("你是一个多功能助手。部分工具需要先用 tool_search 搜索才能使用。"),
		ga.WithTools(
			&calcTool{},
			&weatherTool{},
			&stockTool{},
			&currencyTool{},
			&translateTool{},
		),
		// 只有 calculator 直接可见，其余 4 个需要 tool_search 发现
		ga.WithHooks(toolsearch.New("calculator")),
	)

	app := console.New(console.WithAgent(agent))
	if err := app.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// ── 工具定义 ──────────────────────────────────────────────────

type calcTool struct{}

func (*calcTool) Info() schema.ToolInfo {
	return schema.ToolInfo{
		Name:        "calculator",
		Description: "数学计算器，支持加减乘除、幂运算、开方",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation": map[string]any{"type": "string", "enum": []string{"add", "sub", "mul", "div", "pow", "sqrt"}},
				"a":         map[string]any{"type": "number"},
				"b":         map[string]any{"type": "number"},
			},
			"required": []string{"operation", "a"},
		},
	}
}

func (*calcTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Op string  `json:"operation"`
		A  float64 `json:"a"`
		B  float64 `json:"b"`
	}
	if err := call.JSON(&in); err != nil {
		return "", err
	}
	switch in.Op {
	case "add":
		return fmt.Sprintf("%g", in.A+in.B), nil
	case "sub":
		return fmt.Sprintf("%g", in.A-in.B), nil
	case "mul":
		return fmt.Sprintf("%g", in.A*in.B), nil
	case "div":
		if in.B == 0 {
			return "", fmt.Errorf("division by zero")
		}
		return fmt.Sprintf("%g", in.A/in.B), nil
	case "pow":
		return fmt.Sprintf("%g", math.Pow(in.A, in.B)), nil
	case "sqrt":
		return fmt.Sprintf("%g", math.Sqrt(in.A)), nil
	default:
		return "", fmt.Errorf("unknown op: %s", in.Op)
	}
}

type weatherTool struct{}

func (*weatherTool) Info() schema.ToolInfo {
	return schema.ToolInfo{
		Name:        "get_weather",
		Description: "查询指定城市的当前天气信息",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "城市名称"},
			},
			"required": []string{"city"},
		},
	}
}

func (*weatherTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct{ City string `json:"city"` }
	if err := call.JSON(&in); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s 当前天气：晴，25°C，湿度 60%%，东南风 3 级", in.City), nil
}

type stockTool struct{}

func (*stockTool) Info() schema.ToolInfo {
	return schema.ToolInfo{
		Name:        "query_stock",
		Description: "查询股票实时行情",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "股票代码，如 AAPL、600519"},
			},
			"required": []string{"symbol"},
		},
	}
}

func (*stockTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct{ Symbol string `json:"symbol"` }
	if err := call.JSON(&in); err != nil {
		return "", err
	}
	return fmt.Sprintf("[%s] 最新价: $182.50, 涨跌: +1.2%%, 成交量: 5200万, 更新时间: %s",
		in.Symbol, time.Now().Format("15:04")), nil
}

type currencyTool struct{}

func (*currencyTool) Info() schema.ToolInfo {
	return schema.ToolInfo{
		Name:        "currency_exchange",
		Description: "汇率查询与货币换算",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"from":   map[string]any{"type": "string", "description": "源货币代码，如 USD"},
				"to":     map[string]any{"type": "string", "description": "目标货币代码，如 CNY"},
				"amount": map[string]any{"type": "number", "description": "金额"},
			},
			"required": []string{"from", "to", "amount"},
		},
	}
}

func (*currencyTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		From   string  `json:"from"`
		To     string  `json:"to"`
		Amount float64 `json:"amount"`
	}
	if err := call.JSON(&in); err != nil {
		return "", err
	}
	rate := 7.24 // USD/CNY 模拟汇率
	return fmt.Sprintf("%.2f %s = %.2f %s (汇率: %.4f)", in.Amount, in.From, in.Amount*rate, in.To, rate), nil
}

type translateTool struct{}

func (*translateTool) Info() schema.ToolInfo {
	return schema.ToolInfo{
		Name:        "translate",
		Description: "多语言翻译工具",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":   map[string]any{"type": "string", "description": "待翻译文本"},
				"target": map[string]any{"type": "string", "description": "目标语言，如 en、ja、ko"},
			},
			"required": []string{"text", "target"},
		},
	}
}

func (*translateTool) Execute(_ *ga.Context, call *schema.ToolCall) (string, error) {
	var in struct {
		Text   string `json:"text"`
		Target string `json:"target"`
	}
	if err := call.JSON(&in); err != nil {
		return "", err
	}
	return fmt.Sprintf("[翻译→%s] %s → [模拟翻译结果]", in.Target, in.Text), nil
}