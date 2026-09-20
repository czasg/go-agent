// 多模态对话 —— 发送本地图片给模型识别。
//
// 展示：
//   - schema.ContentPart 构造多模态消息
//   - base64 图片传入方式（本地文件 → base64 → 模型）
//   - 便捷构造函数 TextPart / ImageBase64
//
// 运行：
//
//	go run ./examples/15-multimodal
package main

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"os"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/config"
	"github.com/czasg/go-agent/internal/util"
	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema"
)

//go:embed demo.jpg
var imgData []byte

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	b64 := base64.StdEncoding.EncodeToString(imgData)

	msgs := []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant that can describe images."},
		{
			Role: schema.User,
			MultiParts: []schema.ContentPart{
				schema.TextPart("What is in this image? Describe it briefly use chinese."),
				schema.ImageBase64Part(b64, "image/jpeg"),
			},
		},
	}

	fmt.Printf("=== Local Image (base64) | Model: %s ===\n", cfg.LLM.Model)
	printResponse(chatModel, msgs)
}

func printResponse(chatModel llm.BaseModel, msgs []*schema.Message) {
	reasonPrint := util.NewOncePrint("\n\033[90m💭 Thinking:\033[0m\n")
	contentPrint := util.NewOncePrint("\n\033[36mAssistant:\033[0m ")
	onDelta := func(delta *schema.MessageDelta) {
		if delta.ReasoningContent != "" {
			reasonPrint("\033[90m" + delta.ReasoningContent + "\033[0m")
		}
		if delta.Content != "" {
			contentPrint(delta.Content)
		}
	}

	msg, err := chatModel.Chat(context.Background(), msgs, llm.WithCallback(onDelta))
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n\033[31mError: %v\033[0m\n", err)
		return
	}
	fmt.Println()
	fmt.Printf("\n\033[90mTokens: prompt=%d, completion=%d\033[0m\n",
		msg.Usage.PromptTokens, msg.Usage.CompletionTokens)
}
