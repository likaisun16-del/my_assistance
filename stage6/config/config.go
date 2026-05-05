package config

// APIConfig 存储 Harness 阶段所需的 API 配置
type APIConfig struct {
	// ===== LLM 聊天模型 API（用于推理 + 总结） =====
	LLMAPIUrl   string  // 聊天模型 API 地址
	LLMAPIKey   string  // API Key
	LLMModel    string  // 模型名称，如 ernie-bot-4
	Temperature float64 // 温度参数

	// ===== Embedding 向量化模型 API（用于长期记忆检索，可选） =====
	EmbeddingAPIUrl string // 向量化模型 API 地址
	EmbeddingAPIKey string // API Key
	EmbeddingModel  string // 模型名称

	// ===== Harness 配置 =====
	MaxRetries    int // 工具调用最大重试次数
	RetryDelayMs  int // 重试间隔（毫秒）
	StepTimeoutMs int // 单步超时（毫秒）

	// ===== 通用配置 =====
	ServerPort string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:       "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions",
		LLMAPIKey:       "", // 填入聊天模型 API Key
		LLMModel:        "ernie-bot-4",
		Temperature:     0.3,

		EmbeddingAPIUrl: "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/embedding-v1",
		EmbeddingAPIKey: "", // 填入向量化模型 API Key
		EmbeddingModel:  "embedding-v1",

		MaxRetries:      3,
		RetryDelayMs:    200,
		StepTimeoutMs:   5000,
		ServerPort:      "8086",
	}
}

func (c *APIConfig) IsRealLLM() bool      { return c.LLMAPIKey != "" }
func (c *APIConfig) IsRealEmbedding() bool { return c.EmbeddingAPIKey != "" }
