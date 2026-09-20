package config

import (
	"github.com/czasg/go-agent/internal/util"
	"net/http"
)

const (
	defaultTemperature = 0.7
	defaultMaxTokens   = int64(40960)
	defaultMaxRetries  = 2
)

type LLMConfig struct {
	Provider string `json:"provider"`
	BaseUrl  string `json:"base_url"`
	ApiKey   string `json:"api_key"`
	Model    string `json:"model"`

	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   *int64   `json:"max_tokens,omitempty"`
	MaxRetries  int64    `json:"max_retries"`

	Headers    map[string]string `json:"headers,omitempty"`
	HTTPClient *http.Client      `json:"-"`
}

func (c *LLMConfig) ApplyDefaults() {
	if c.Temperature == nil {
		c.Temperature = util.PtrOf(defaultTemperature)
	}
	if c.MaxTokens == nil {
		c.MaxTokens = util.PtrOf(defaultMaxTokens)
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = defaultMaxRetries
	}
}
