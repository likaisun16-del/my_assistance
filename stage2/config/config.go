package config

// APIConfig 存储 RAG 阶段所需的所有 API 配置
type APIConfig struct {
	// ===== LLM 聊天模型 API（用于生成最终回答） =====
	LLMAPIUrl   string  // 聊天模型 API 地址
	LLMAPIKey   string  // API Key
	LLMModel    string  // 模型名称，如 ernie-bot-4
	Temperature float64 // 温度参数

	// ===== Embedding 向量化模型 API（用于文档向量化和检索） =====
	EmbeddingAPIUrl string // 向量化模型 API 地址，如 https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/embedding-v1
	EmbeddingAPIKey string // API Key
	EmbeddingModel  string // 模型名称，如 embedding-v1

	// ===== RAG 配置 =====
	ChunkSize int // 文档切分大小（字符数）
	ChunkOverlap int // 切片重叠字符数
	TopK      int // 检索返回的 top-k 文档数

	// ===== 通用配置 =====
	ServerPort string
}

// DefaultConfig 返回默认配置（使用模拟模型）
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:       "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions",
		LLMAPIKey:       "", // 填入你的聊天模型 API Key
		LLMModel:        "ernie-bot-4",
		Temperature:     0.7,

		EmbeddingAPIUrl: "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/embedding-v1",
		EmbeddingAPIKey: "", // 填入你的向量化模型 API Key
		EmbeddingModel:  "embedding-v1",

		ChunkSize:    200,
		ChunkOverlap: 50,
		TopK:         3,
		ServerPort:   "8082",
	}
}

func (c *APIConfig) IsRealLLM() bool      { return c.LLMAPIKey != "" }
func (c *APIConfig) IsRealEmbedding() bool { return c.EmbeddingAPIKey != "" }
