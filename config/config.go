package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// 项目约定常量。
const (
	ProjectName = "go-agent"
	ProjectAbbr = "ga"
	SettingsFile = "settings.json"
)

// Dir 是 ga 的工作目录（默认 ~/.ga），init 时自动创建。
var Dir string

// SettingsPath 返回 settings.json 的完整路径，由 Dir 动态拼接。
func SettingsPath() string {
	return filepath.Join(Dir, SettingsFile)
}

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("[config] 获取用户主目录失败: %v", err)
	}
	Dir = filepath.Join(home, "."+ProjectAbbr)
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		log.Fatalf("[config] 创建默认工作目录 %s 失败: %v", Dir, err)
	}
}

// Config 应用配置，对应 settings.json 的内容。
type Config struct {
	LLM LLMConfig `json:"llm"`
}

var (
	once sync.Once
	cfg  Config
)

// GetConfig 从 SettingsPath() 读取配置；文件不存在则写入默认配置。
func GetConfig() Config {
	once.Do(func() {
		cfg = load()
	})
	return cfg
}

func load() Config {
	path := SettingsPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return initDefault(path)
		}
		fmt.Printf("[config] 读取配置失败: %v，使用默认值\n", err)
		return defaultConfig()
	}

	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		fmt.Printf("[config] 解析配置失败: %v，使用默认值\n", err)
		return defaultConfig()
	}
	return c
}

func defaultConfig() Config {
	return Config{
		LLM: LLMConfig{
			Provider: "openai",
			BaseUrl:  "https://api.openai.com/v1",
			ApiKey:   "",
			Model:    "gpt-4o",
		},
	}
}

// initDefault 写入默认配置文件，方便用户填写 API Key。
func initDefault(path string) Config {
	c := defaultConfig()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return c
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fmt.Printf("[config] 写入默认配置失败: %v，使用默认值\n", err)
		return c
	}

	fmt.Printf("[config] 已初始化默认配置: %s\n请编辑该文件填入真实的 API Key。\n", path)
	return c
}
