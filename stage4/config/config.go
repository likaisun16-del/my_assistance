package config

// APIConfig 存储 ReAct 阶段所需的 API 配置
type APIConfig struct {
	// ===== LLM 聊天模型 API（用于多步推理 + 工具选择 + 总结） =====
	LLMAPIUrl   string  // 聊天模型 API 地址
	LLMAPIKey   string  // API Key
	LLMModel    string  // 模型名称，如 ernie-bot-4
	Temperature float64 // 温度参数

	// ===== ReAct 配置 =====
	MaxIterations int // 最大推理迭代次数（防死循环）

	// ===== 通用配置 =====
	ServerPort string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:     "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions",
		LLMAPIKey:     "", // 填入你的 API Key
		LLMModel:      "ernie-bot-4",
		Temperature:   0.3,
		MaxIterations: 5,
		ServerPort:    "8084",
	}
}

func (c *APIConfig) IsRealAPI() bool { return c.LLMAPIKey != "" }
