package config

// APIConfig 存储所有外部 API 配置
// 在真实项目中，这些值应从环境变量或配置文件读取
type APIConfig struct {
	// ===== LLM 聊天模型 API =====
	LLMAPIUrl   string // 大语言模型 API 地址，如 https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions
	LLMAPIKey   string // API Key
	LLMModel    string // 模型名称，如 ernie-bot-4, gpt-4 等
	Temperature float64 // 温度参数 0.0~1.0，越高越随机

	// ===== 通用配置 =====
	ServerPort string // 服务监听端口
}

// DefaultConfig 返回默认配置（使用模拟 LLM）
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:   "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions",
		LLMAPIKey:   "",  // 填入你的 API Key
		LLMModel:    "ernie-bot-4",
		Temperature: 0.7,
		ServerPort:  "8081",
	}
}

// IsRealAPI 是否配置了真实 API
func (c *APIConfig) IsRealAPI() bool {
	return c.LLMAPIKey != ""
}
