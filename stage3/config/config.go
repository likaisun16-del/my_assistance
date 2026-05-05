package config

// APIConfig 存储 Tool Agent 阶段所需的 API 配置
type APIConfig struct {
	// ===== LLM 聊天模型 API（用于理解意图 + 选择工具 + 生成回答） =====
	LLMAPIUrl   string  // 聊天模型 API 地址
	LLMAPIKey   string  // API Key
	LLMModel    string  // 模型名称，如 ernie-bot-4
	Temperature float64 // 温度参数

	// ===== 通用配置 =====
	ServerPort string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:   "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions",
		LLMAPIKey:   "", // 填入你的 API Key
		LLMModel:    "ernie-bot-4",
		Temperature: 0.3, // 工具选择场景建议低温度
		ServerPort:  "8083",
	}
}

func (c *APIConfig) IsRealAPI() bool { return c.LLMAPIKey != "" }
