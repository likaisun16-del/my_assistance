// Package infra 管理所有外部基础设施连接：Milvus / PostgreSQL / Elasticsearch / Kafka
// 每个连接失败时优雅降级，不影响应用启动。
package infra

import (
	"context"
	"database/sql"
	"encoding/json"
	"final/config"
	"fmt"
	"log"
	"strings"
	"time"

	es "github.com/elastic/go-elasticsearch/v8"
	_ "github.com/lib/pq"
	milvusClient "github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"github.com/segmentio/kafka-go"
)

// Status 记录各基础设施的连接状态
type Status struct {
	Milvus string `json:"milvus"`
	PG     string `json:"pg"`
	ES     string `json:"elasticsearch"`
	Kafka  string `json:"kafka"`
}

// Infrastructure 持有所有外部连接句柄
type Infrastructure struct {
	cfg    *config.APIConfig
	milvus milvusClient.Client
	pg     *sql.DB
	es     *es.Client
	kafkaW *kafka.Writer
	Ready  Status
}

// New 尝试连接所有基础设施，失败则降级为内存模式。
func New(cfg *config.APIConfig) *Infrastructure {
	inf := &Infrastructure{cfg: cfg}
	inf.connectMilvus()
	inf.connectPostgres()
	inf.connectES()
	inf.connectKafka()
	return inf
}

// ─────────────────────────────── 连接初始化 ───────────────────────────────

func (inf *Infrastructure) connectMilvus() {
	mc, err := milvusClient.NewClient(context.Background(), milvusClient.Config{
		Address: inf.cfg.MilvusAddr(),
	})
	if err != nil {
		log.Printf("⚠️  Milvus 连接失败: %v (将使用内存向量库)", err)
		inf.Ready.Milvus = "disconnected"
		return
	}
	inf.milvus = mc
	inf.Ready.Milvus = "connected"
	log.Println("✅ Milvus 已连接:", inf.cfg.MilvusAddr())
}

func (inf *Infrastructure) connectPostgres() {
	pg, err := sql.Open("postgres", inf.cfg.PGDSN())
	if err != nil {
		log.Printf("⚠️  PostgreSQL 打开失败: %v", err)
		inf.Ready.PG = "disconnected"
		return
	}
	if err := pg.Ping(); err != nil {
		log.Printf("⚠️  PostgreSQL Ping 失败: %v", err)
		inf.Ready.PG = "disconnected"
		return
	}
	inf.pg = pg
	inf.Ready.PG = "connected"
	inf.initPGSchema()
	log.Println("✅ PostgreSQL 已连接:", inf.cfg.PGDSN())
}

func (inf *Infrastructure) connectES() {
	esCfg := es.Config{
		Addresses: inf.cfg.ESAddresses,
		Username:  inf.cfg.ESUsername,
		Password:  inf.cfg.ESPassword,
	}
	esClient, err := es.NewClient(esCfg)
	if err != nil {
		log.Printf("⚠️  Elasticsearch 连接失败: %v", err)
		inf.Ready.ES = "disconnected"
		return
	}
	res, err := esClient.Info()
	if err != nil {
		log.Printf("⚠️  Elasticsearch Ping 失败: %v", err)
		inf.Ready.ES = "disconnected"
		return
	}
	res.Body.Close()
	inf.es = esClient
	inf.Ready.ES = "connected"
	log.Println("✅ Elasticsearch 已连接:", inf.cfg.ESAddresses)
}

func (inf *Infrastructure) connectKafka() {
	inf.kafkaW = &kafka.Writer{
		Addr:         kafka.TCP(inf.cfg.KafkaBrokers...),
		Topic:        inf.cfg.KafkaTopic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := kafka.DialLeader(ctx, "tcp", inf.cfg.KafkaBrokers[0], inf.cfg.KafkaTopic, 0)
	if err != nil {
		log.Printf("⚠️  Kafka 连接失败: %v (事件将输出到日志)", err)
		inf.Ready.Kafka = "disconnected"
		return
	}
	conn.Close()
	inf.Ready.Kafka = "connected"
	log.Println("✅ Kafka 已连接:", inf.cfg.KafkaBrokers)
}

// ─────────────────────────────── PostgreSQL ───────────────────────────────

// initPGSchema 建表（幂等）
func (inf *Infrastructure) initPGSchema() {
	if inf.pg == nil {
		return
	}
	ddls := []string{
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id    TEXT NOT NULL,
			key        TEXT NOT NULL,
			value      TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (user_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS task_snapshots (
			task_id    TEXT PRIMARY KEY,
			state      JSONB NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS chat_history (
			id         SERIAL PRIMARY KEY,
			role       TEXT NOT NULL,
			content    TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS long_term_memory (
			id          SERIAL PRIMARY KEY,
			content     TEXT NOT NULL,
			importance  FLOAT NOT NULL DEFAULT 0.5,
			embedding   JSONB,
			created_at  TIMESTAMP DEFAULT NOW()
		)`,
	}
	for _, ddl := range ddls {
		if _, err := inf.pg.Exec(ddl); err != nil {
			log.Printf("⚠️  PG 建表失败: %v", err)
		}
	}
	log.Println("✅ PostgreSQL 表结构已初始化")
}

// SavePreference 持久化用户偏好到 PostgreSQL（upsert）
func (inf *Infrastructure) SavePreference(userID, key, value string) {
	if inf.pg == nil {
		return
	}
	_, err := inf.pg.Exec(
		`INSERT INTO user_preferences (user_id, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, key) DO UPDATE SET value = $3, updated_at = NOW()`,
		userID, key, value,
	)
	if err != nil {
		log.Printf("⚠️  偏好保存到 PG 失败: %v", err)
	}
}

// SaveSnapshot 持久化任务快照到 PostgreSQL（upsert）
func (inf *Infrastructure) SaveSnapshot(taskID string, stateJSON []byte) {
	if inf.pg == nil {
		return
	}
	_, err := inf.pg.Exec(
		`INSERT INTO task_snapshots (task_id, state) VALUES ($1, $2)
		 ON CONFLICT (task_id) DO UPDATE SET state = $2, created_at = NOW()`,
		taskID, stateJSON,
	)
	if err != nil {
		log.Printf("⚠️  快照保存到 PG 失败: %v", err)
	}
}

// LoadPreferences 从 PostgreSQL 加载指定用户的全部偏好，返回 map[key]value
func (inf *Infrastructure) LoadPreferences(userID string) map[string]string {
	result := make(map[string]string)
	if inf.pg == nil {
		return result
	}
	rows, err := inf.pg.Query(`SELECT key, value FROM user_preferences WHERE user_id = $1`, userID)
	if err != nil {
		log.Printf("⚠️  加载偏好失败: %v", err)
		return result
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			result[k] = v
		}
	}
	return result
}

// LongTermRow 是从 PG 读取的长期记忆行
type LongTermRow struct {
	ID         int
	Content    string
	Importance float64
	Embedding  []float64
}

// SaveLongTermItem 将一条长期记忆持久化到 PostgreSQL，返回数据库自增 ID
func (inf *Infrastructure) SaveLongTermItem(content string, importance float64, embeddingJSON []byte) int {
	if inf.pg == nil {
		return -1
	}
	var id int
	err := inf.pg.QueryRow(
		`INSERT INTO long_term_memory (content, importance, embedding) VALUES ($1, $2, $3) RETURNING id`,
		content, importance, embeddingJSON,
	).Scan(&id)
	if err != nil {
		log.Printf("⚠️  长期记忆保存失败: %v", err)
		return -1
	}
	return id
}

// LoadLongTermItems 从 PostgreSQL 加载全部长期记忆条目
func (inf *Infrastructure) LoadLongTermItems() []LongTermRow {
	if inf.pg == nil {
		return nil
	}
	rows, err := inf.pg.Query(`SELECT id, content, importance, embedding FROM long_term_memory ORDER BY id`)
	if err != nil {
		log.Printf("⚠️  加载长期记忆失败: %v", err)
		return nil
	}
	defer rows.Close()
	var items []LongTermRow
	for rows.Next() {
		var row LongTermRow
		var embJSON []byte
		if err := rows.Scan(&row.ID, &row.Content, &row.Importance, &embJSON); err != nil {
			continue
		}
		if len(embJSON) > 0 {
			json.Unmarshal(embJSON, &row.Embedding)
		}
		items = append(items, row)
	}
	return items
}

// ─────────────────────────────── Elasticsearch ───────────────────────────

// SearchES 在 Elasticsearch 中执行 JSON 查询，返回原始响应字符串
func (inf *Infrastructure) SearchES(index, queryJSON string) (string, error) {
	if inf.es == nil {
		return "", fmt.Errorf("elasticsearch not connected")
	}
	resp, err := inf.es.Search(
		inf.es.Search.WithIndex(index),
		inf.es.Search.WithBody(strings.NewReader(queryJSON)),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	data, _ := json.Marshal(result)
	return string(data), nil
}

// ─────────────────────────────── Milvus ──────────────────────────────────

// MilvusSearch 在 Milvus 中进行向量近邻搜索，返回匹配文档 ID 列表
func (inf *Infrastructure) MilvusSearch(collection string, vector []float32, topK int) ([]int64, error) {
	if inf.milvus == nil {
		return nil, fmt.Errorf("milvus not connected")
	}
	sp, _ := entity.NewIndexFlatSearchParam()
	results, err := inf.milvus.Search(
		context.Background(), collection, []string{},
		"", []string{"content"},
		[]entity.Vector{entity.FloatVector(vector)},
		"embedding", entity.L2,
		topK, sp,
	)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range results {
		for _, id := range r.IDs.FieldData().GetScalars().GetLongData().Data {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ─────────────────────────────── Kafka ───────────────────────────────────

// PublishEvent 向 Kafka 发布事件；未连接时退化为日志输出
func (inf *Infrastructure) PublishEvent(eventType, payload string) {
	msg := kafka.Message{
		Key:   []byte(eventType),
		Value: []byte(payload),
	}
	if inf.Ready.Kafka == "connected" {
		if err := inf.kafkaW.WriteMessages(context.Background(), msg); err != nil {
			log.Printf("⚠️  Kafka 写入失败: %v", err)
		}
	} else {
		log.Printf("📋 [Kafka-fallback] %s: %s", eventType, payload)
	}
}

// ─────────────────────────────── 生命周期 ────────────────────────────────

// Close 释放所有连接资源
func (inf *Infrastructure) Close() {
	if inf.milvus != nil {
		inf.milvus.Close()
	}
	if inf.pg != nil {
		inf.pg.Close()
	}
	if inf.kafkaW != nil {
		inf.kafkaW.Close()
	}
}
