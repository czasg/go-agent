package ga

import (
	"fmt"
	"strings"

	"github.com/czasg/go-agent/llm"
)

func NewChatModel(config *llm.Config) (llm.BaseModel, error) {
	if config == nil {
		return nil, fmt.Errorf("agent: config 为空")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("agent: mode 为空")
	}

	switch strings.ToLower(strings.TrimSpace(config.Provider)) {
	case "openai":
		return llm.NewOpenAIChatModel(config), nil
	case "anthropic":
		return llm.NewAnthropicChatModel(config), nil
	default:
		return llm.NewOpenAIChatModel(config), nil
	}
}
