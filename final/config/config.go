package config

import (
	"fmt"
	"os"
)

// APIConfig 整合所有阶段的 API + 基础设施配置
type APIConfig struct {
	// ===== LLM 聊天模型 API =====
	LLMAPIUrl   string
	LLMAPIKey   string
	LLMModel    string
	Temperature float64

	// ===== Embedding 向量化模型 API =====
	EmbeddingAPIUrl string
	EmbeddingAPIKey string
	EmbeddingModel  string

	// ===== Milvus 向量数据库 =====
	MilvusHost string
	MilvusPort int

	// ===== PostgreSQL 关系型数据库 =====
	PGHost     string
	PGPort     int
	PGUser     string
	PGPassword string
	PGDatabase string

	// ===== Elasticsearch =====
	ESAddresses []string
	ESUsername  string
	ESPassword  string

	// ===== Kafka =====
	KafkaBrokers []string
	KafkaTopic   string

	// ===== RAG 配置 =====
	ChunkSize    int
	ChunkOverlap int
	TopK         int

	// ===== Memory 配置 =====
	ShortTermMaxTurns int
	LongTermTopK      int

	// ===== Harness 配置 =====
	MaxRetries    int
	RetryDelayMs  int
	StepTimeoutMs int
	MaxIterations int

	// ===== 通用配置 =====
	ServerPort string
}

// DefaultConfig 返回默认配置，优先读取环境变量（Docker Compose 注入）
func DefaultConfig() *APIConfig {
	return &APIConfig{
		LLMAPIUrl:        envOr("LLM_API_URL", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/chat/completions"),
		LLMAPIKey:        envOr("LLM_API_KEY", ""),
		LLMModel:         envOr("LLM_MODEL", "ernie-bot-4"),
		Temperature:      0.7,

		EmbeddingAPIUrl:  envOr("EMBEDDING_API_URL", "https://aip.baidubce.com/rpc/2.0/ai_custom/v1/wenxinworkshop/embeddings/embedding-v1"),
		EmbeddingAPIKey:  envOr("EMBEDDING_API_KEY", ""),
		EmbeddingModel:   envOr("EMBEDDING_MODEL", "embedding-v1"),

		MilvusHost:       envOr("MILVUS_HOST", "milvus"),
		MilvusPort:       19530,

		PGHost:           envOr("PG_HOST", "postgres"),
		PGPort:           5432,
		PGUser:           envOr("PG_USER", "aiagent"),
		PGPassword:       envOr("PG_PASSWORD", "aiagent123"),
		PGDatabase:       envOr("PG_DATABASE", "aiagent"),

		ESAddresses:      []string{envOr("ES_ADDRESS", "http://elasticsearch:9200")},
		ESUsername:       envOr("ES_USERNAME", "elastic"),
		ESPassword:       envOr("ES_PASSWORD", "changeme"),

		KafkaBrokers:     []string{envOr("KAFKA_BROKER", "kafka:9092")},
		KafkaTopic:       envOr("KAFKA_TOPIC", "agent-events"),

		ChunkSize:        200,
		ChunkOverlap:     50,
		TopK:             3,

		ShortTermMaxTurns: 5,
		LongTermTopK:      3,

		MaxRetries:      3,
		RetryDelayMs:    200,
		StepTimeoutMs:   5000,
		MaxIterations:   5,

		ServerPort:      envOr("SERVER_PORT", "8090"),
	}
}

func (c *APIConfig) IsRealLLM() bool      { return c.LLMAPIKey != "" }
func (c *APIConfig) IsRealEmbedding() bool { return c.EmbeddingAPIKey != "" }

// PGDSN 返回 PostgreSQL 连接串
func (c *APIConfig) PGDSN() string {
	return fmtDSN("postgres", c.PGUser, c.PGPassword, c.PGHost, c.PGPort, c.PGDatabase)
}

// MilvusAddr 返回 Milvus 地址
func (c *APIConfig) MilvusAddr() string {
	return fmt.Sprintf("%s:%d", c.MilvusHost, c.MilvusPort)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fmtDSN(driver, user, pass, host string, port int, db string) string {
	return fmt.Sprintf("%s://%s:%s@%s:%d/%s?sslmode=disable", driver, user, pass, host, port, db)
}
