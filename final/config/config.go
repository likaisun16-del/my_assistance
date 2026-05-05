package config

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
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

	// ===== 搜索 API（可选，支持 Tavily 等）=====
	SearchAPIKey string
	SearchAPIURL string

	// ===== 通用配置 =====
	ServerPort string
}

// yamlFile 对应 config/config.yaml 的结构
type yamlFile struct {
	LLM struct {
		APIUrl      string  `yaml:"api_url"`
		APIKey      string  `yaml:"api_key"`
		Model       string  `yaml:"model"`
		Temperature float64 `yaml:"temperature"`
	} `yaml:"llm"`
	Embedding struct {
		APIUrl string `yaml:"api_url"`
		APIKey string `yaml:"api_key"`
		Model  string `yaml:"model"`
	} `yaml:"embedding"`
	Milvus struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"milvus"`
	Postgres struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		Database string `yaml:"database"`
	} `yaml:"postgres"`
	Elasticsearch struct {
		Addresses []string `yaml:"addresses"`
		Username  string   `yaml:"username"`
		Password  string   `yaml:"password"`
	} `yaml:"elasticsearch"`
	Kafka struct {
		Brokers []string `yaml:"brokers"`
		Topic   string   `yaml:"topic"`
	} `yaml:"kafka"`
	RAG struct {
		ChunkSize    int `yaml:"chunk_size"`
		ChunkOverlap int `yaml:"chunk_overlap"`
		TopK         int `yaml:"top_k"`
	} `yaml:"rag"`
	Memory struct {
		ShortTermMaxTurns int `yaml:"short_term_max_turns"`
		LongTermTopK      int `yaml:"long_term_top_k"`
	} `yaml:"memory"`
	Harness struct {
		MaxRetries    int `yaml:"max_retries"`
		RetryDelayMs  int `yaml:"retry_delay_ms"`
		StepTimeoutMs int `yaml:"step_timeout_ms"`
		MaxIterations int `yaml:"max_iterations"`
	} `yaml:"harness"`
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`
	Search struct {
		APIKey string `yaml:"api_key"`
		APIURL string `yaml:"api_url"`
	} `yaml:"search"`
}

// DefaultConfig 从 config/config.yaml 加载配置
func DefaultConfig() *APIConfig {
	data, err := os.ReadFile("config/config.yaml")
	if err != nil {
		log.Fatalf("读取 config/config.yaml 失败: %v", err)
	}

	var y yamlFile
	if err := yaml.Unmarshal(data, &y); err != nil {
		log.Fatalf("解析 config/config.yaml 失败: %v", err)
	}

	return &APIConfig{
		LLMAPIUrl:   y.LLM.APIUrl,
		LLMAPIKey:   y.LLM.APIKey,
		LLMModel:    y.LLM.Model,
		Temperature: y.LLM.Temperature,

		EmbeddingAPIUrl: y.Embedding.APIUrl,
		EmbeddingAPIKey: y.Embedding.APIKey,
		EmbeddingModel:  y.Embedding.Model,

		MilvusHost: y.Milvus.Host,
		MilvusPort: y.Milvus.Port,

		PGHost:     y.Postgres.Host,
		PGPort:     y.Postgres.Port,
		PGUser:     y.Postgres.User,
		PGPassword: y.Postgres.Password,
		PGDatabase: y.Postgres.Database,

		ESAddresses: y.Elasticsearch.Addresses,
		ESUsername:  y.Elasticsearch.Username,
		ESPassword:  y.Elasticsearch.Password,

		KafkaBrokers: y.Kafka.Brokers,
		KafkaTopic:   y.Kafka.Topic,

		ChunkSize:    y.RAG.ChunkSize,
		ChunkOverlap: y.RAG.ChunkOverlap,
		TopK:         y.RAG.TopK,

		ShortTermMaxTurns: y.Memory.ShortTermMaxTurns,
		LongTermTopK:      y.Memory.LongTermTopK,

		MaxRetries:    y.Harness.MaxRetries,
		RetryDelayMs:  y.Harness.RetryDelayMs,
		StepTimeoutMs: y.Harness.StepTimeoutMs,
		MaxIterations: y.Harness.MaxIterations,

		SearchAPIKey: y.Search.APIKey,
		SearchAPIURL: y.Search.APIURL,

		ServerPort: y.Server.Port,
	}
}

func (c *APIConfig) IsRealLLM() bool      { return c.LLMAPIKey != "" }
func (c *APIConfig) IsRealEmbedding() bool { return c.EmbeddingAPIKey != "" }

// PGDSN 返回 PostgreSQL 连接串
func (c *APIConfig) PGDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.PGUser, c.PGPassword, c.PGHost, c.PGPort, c.PGDatabase)
}

// MilvusAddr 返回 Milvus 地址
func (c *APIConfig) MilvusAddr() string {
	return fmt.Sprintf("%s:%d", c.MilvusHost, c.MilvusPort)
}
