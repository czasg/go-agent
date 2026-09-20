// 纯 LLM 流式对话 —— 不经过 Agent，直接调用模型。
//
// 展示：
//   - llm.BaseModel 的 Chat 方法
//   - WithCallback 流式回调（思考链 + 正文）
//   - 手动管理消息历史
//
// 运行：
//
//	go run ./examples/01-llm-chat
package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/czasg/go-agent/config"
	"os"
	"strings"

	ga "github.com/czasg/go-agent"
	"github.com/czasg/go-agent/internal/util"
	"github.com/czasg/go-agent/llm"
	"github.com/czasg/go-agent/schema"
)

func main() {
	cfg := config.GetConfig()
	chatModel, err := ga.NewChatModel(&cfg.LLM)
	if err != nil {
		panic(err)
	}

	messages := []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant."},
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Chat started. Type 'exit' to quit.")

	for {
		fmt.Print("\n> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.EqualFold(input, "exit") {
			fmt.Println("Bye!")
			break
		}

		messages = append(messages, &schema.Message{Role: schema.User, Content: input})

		// 流式输出：首次打印 header，之后增量追加。
		reasonPrint := util.NewOncePrint("\n\033[90m💭 思考:\033[0m\n")
		contentPrint := util.NewOncePrint("\n\033[36mAssistant:\033[0m ")
		onDelta := func(delta *schema.MessageDelta) {
			if delta.ReasoningContent != "" {
				reasonPrint("\033[90m" + delta.ReasoningContent + "\033[0m")
			}
			if delta.Content != "" {
				contentPrint(delta.Content)
			}
		}

		msg, err := chatModel.Chat(context.Background(), messages, llm.WithCallback(onDelta))
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n\033[31mError: %v\033[0m\n", err)
			continue
		}

		messages = append(messages, msg)
		fmt.Println()
	}
}
