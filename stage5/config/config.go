package config

// APIConfig 存储 Memory 阶段所需的 API 配置
type APIConfig struct {
	// ===== LLM 聊天模型 API（用于对话 + 信息提取 + 总结） =====
	LLMAPIUrl   string  // 聊天模型 API 地址
	LLMAPIKey   string  // API Key
	LLMModel    string  // 模型名称，如 ernie-bot-4
	Temperature float64 // 温度参数

	// ===== Embedding 向量化模型 API（用于长期记忆的向量化存储和检索） =====
	EmbeddingAPIUrl string // 向量化模型 API 地址
	EmbeddingAPIKey string // API Key
	EmbeddingModel  string // 模型名称，如 embedding-v1

	// ===== Memory 配置 =====
	ShortTermMaxTurns int // 短期记忆保留的最大对话轮数
	LongTermTopK      int // 长期记忆检索 top-k

	// ===== 通用配置 =====
	ServerPort string
}

// DefaultConfig 返回默认配置
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:        "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions",
		LLMAPIKey:        "", // 填入聊天模型 API Key
		LLMModel:         "ernie-bot-4",
		Temperature:      0.7,

		EmbeddingAPIUrl:  "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/embedding-v1",
		EmbeddingAPIKey:  "", // 填入向量化模型 API Key
		EmbeddingModel:   "embedding-v1",

		ShortTermMaxTurns: 5,
		LongTermTopK:      3,
		ServerPort:        "8085",
	}
}

func (c *APIConfig) IsRealLLM() bool      { return c.LLMAPIKey != "" }
func (c *APIConfig) IsRealEmbedding() bool { return c.EmbeddingAPIKey != "" }
