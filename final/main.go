package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"final/config"
	"strings"
	"time"
	"unicode/utf8"

	// Milvus 向量数据库 SDK
	milvusClient "github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"

	// PostgreSQL 驱动
	"database/sql"
	_ "github.com/lib/pq"

	// Elasticsearch SDK
	es "github.com/elastic/go-elasticsearch/v8"

	// Kafka SDK
	"github.com/segmentio/kafka-go"
)

// ══════════════════════════════════════════════════════════════
//  Infrastructure: 基础设施连接管理
// ══════════════════════════════════════════════════════════════

type Infrastructure struct {
	cfg     *config.APIConfig
	milvus  milvusClient.Client
	pg      *sql.DB
	es      *es.Client
	kafkaW  *kafka.Writer
	ready   InfrastructureStatus
}

type InfrastructureStatus struct {
	Milvus     string `json:"milvus"`
	PostgreSQL string `json:"postgresql"`
	ES         string `json:"elasticsearch"`
	Kafka      string `json:"kafka"`
}

func NewInfrastructure(cfg *config.APIConfig) *Infrastructure {
	inf := &Infrastructure{cfg: cfg}

	// ===== Milvus =====
	milvusClient, err := milvusClient.NewClient(context.Background(), milvusClient.Config{
		Address: cfg.MilvusAddr(),
	})
	if err != nil {
		log.Printf("⚠️  Milvus 连接失败: %v (将使用内存向量库)", err)
		inf.ready.Milvus = "disconnected"
	} else {
		inf.milvus = milvusClient
		inf.ready.Milvus = "connected"
		log.Println("✅ Milvus 已连接:", cfg.MilvusAddr())
	}

	// ===== PostgreSQL =====
	pg, err := sql.Open("postgres", cfg.PGDSN())
	if err != nil {
		log.Printf("⚠️  PostgreSQL 连接失败: %v (将使用内存存储)", err)
		inf.ready.PostgreSQL = "disconnected"
	} else if err := pg.Ping(); err != nil {
		log.Printf("⚠️  PostgreSQL Ping 失败: %v", err)
		inf.ready.PostgreSQL = "disconnected"
	} else {
		inf.pg = pg
		inf.ready.PostgreSQL = "connected"
		inf.initPGSchema()
		log.Println("✅ PostgreSQL 已连接:", cfg.PGDSN())
	}

	// ===== Elasticsearch =====
	esCfg := es.Config{
		Addresses: cfg.ESAddresses,
		Username:  cfg.ESUsername,
		Password:  cfg.ESPassword,
	}
	esClient, err := es.NewClient(esCfg)
	if err != nil {
		log.Printf("⚠️  Elasticsearch 连接失败: %v (将使用内存检索)", err)
		inf.ready.ES = "disconnected"
	} else {
		inf.es = esClient
		// 验证连接
		if res, err := esClient.Info(); err == nil {
			res.Body.Close()
			inf.ready.ES = "connected"
			log.Println("✅ Elasticsearch 已连接:", cfg.ESAddresses)
		} else {
			log.Printf("⚠️  Elasticsearch Ping 失败: %v", err)
			inf.ready.ES = "disconnected"
		}
	}

	// ===== Kafka =====
	inf.kafkaW = &kafka.Writer{
		Addr:         kafka.TCP(cfg.KafkaBrokers...),
		Topic:        cfg.KafkaTopic,
		Balancer:     &kafka.LeastBytes{},
		BatchTimeout: 10 * time.Millisecond,
	}
	// 验证 Kafka（非阻塞，Kafka 可能启动较慢）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := kafka.DialLeader(ctx, "tcp", cfg.KafkaBrokers[0], cfg.KafkaTopic, 0)
	if err != nil {
		log.Printf("⚠️  Kafka 连接失败: %v (事件将输出到日志)", err)
		inf.ready.Kafka = "disconnected"
	} else {
		conn.Close()
		inf.ready.Kafka = "connected"
		log.Println("✅ Kafka 已连接:", cfg.KafkaBrokers)
	}

	return inf
}

// initPGSchema 初始化 PostgreSQL 表结构
func (inf *Infrastructure) initPGSchema() {
	if inf.pg == nil { return }
	queries := []string{
		`CREATE TABLE IF NOT EXISTS user_preferences (
			user_id TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT NOW(),
			PRIMARY KEY (user_id, key)
		)`,
		`CREATE TABLE IF NOT EXISTS task_snapshots (
			task_id TEXT PRIMARY KEY,
			state JSONB NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS chat_history (
			id SERIAL PRIMARY KEY,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT NOW()
		)`,
	}
	for _, q := range queries {
		if _, err := inf.pg.Exec(q); err != nil {
			log.Printf("⚠️  PG 建表失败: %v", err)
		}
	}
	log.Println("✅ PostgreSQL 表结构已初始化")
}

// PublishEvent 向 Kafka 发布事件
func (inf *Infrastructure) PublishEvent(eventType, payload string) {
	msg := kafka.Message{
		Key:   []byte(eventType),
		Value: []byte(payload),
	}
	if inf.ready.Kafka == "connected" {
		if err := inf.kafkaW.WriteMessages(context.Background(), msg); err != nil {
			log.Printf("⚠️  Kafka 写入失败: %v", err)
		}
	} else {
		log.Printf("📋 [Kafka-fallback] %s: %s", eventType, payload)
	}
}

// SaveSnapshotToPG 将快照持久化到 PostgreSQL
func (inf *Infrastructure) SaveSnapshotToPG(taskID string, stateJSON []byte) {
	if inf.pg == nil { return }
	_, err := inf.pg.Exec(
		`INSERT INTO task_snapshots (task_id, state) VALUES ($1, $2)
		 ON CONFLICT (task_id) DO UPDATE SET state = $2, created_at = NOW()`,
		taskID, stateJSON,
	)
	if err != nil {
		log.Printf("⚠️  快照保存到PG失败: %v", err)
	}
}

// SavePreferenceToPG 将用户偏好持久化到 PostgreSQL
func (inf *Infrastructure) SavePreferenceToPG(userID, key, value string) {
	if inf.pg == nil { return }
	_, err := inf.pg.Exec(
		`INSERT INTO user_preferences (user_id, key, value) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, key) DO UPDATE SET value = $3, updated_at = NOW()`,
		userID, key, value,
	)
	if err != nil {
		log.Printf("⚠️  偏好保存到PG失败: %v", err)
	}
}

// SearchES 在 Elasticsearch 中搜索
func (inf *Infrastructure) SearchES(index, query string) (string, error) {
	if inf.es == nil { return "", fmt.Errorf("ES not connected") }
	resp, err := inf.es.Search(
		inf.es.Search.WithIndex(index),
		inf.es.Search.WithBody(strings.NewReader(query)),
	)
	if err != nil { return "", err }
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	data, _ := json.Marshal(result)
	return string(data), nil
}

// MilvusSearch 在 Milvus 中进行向量搜索
func (inf *Infrastructure) MilvusSearch(collection string, vector []float32, topK int) ([]int64, error) {
	if inf.milvus == nil { return nil, fmt.Errorf("Milvus not connected") }
	sp, _ := entity.NewIndexFlatSearchParam()
	results, err := inf.milvus.Search(
		context.Background(), collection, []string{},
		"", []string{"content"},
		[]entity.Vector{entity.FloatVector(vector)},
		"embedding", entity.L2,
		topK, sp,
	)
	if err != nil { return nil, err }
	var ids []int64
	for _, r := range results {
		for _, id := range r.IDs.FieldData().GetScalars().GetLongData().Data {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Close 释放所有连接
func (inf *Infrastructure) Close() {
	if inf.milvus != nil { inf.milvus.Close() }
	if inf.pg != nil { inf.pg.Close() }
	if inf.kafkaW != nil { inf.kafkaW.Close() }
}

// ══════════════════════════════════════════════════════════════
//  Stage 1: LLM Client
// ══════════════════════════════════════════════════════════════

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMClient struct {
	cfg *config.APIConfig
}

func NewLLMClient(cfg *config.APIConfig) *LLMClient { return &LLMClient{cfg: cfg} }

func (c *LLMClient) Chat(systemPrompt string, messages []Message) string {
	if c.cfg.IsRealLLM() {
		// TODO: 调用真实 LLM API
	}
	return c.mockChat(systemPrompt, messages)
}

func (c *LLMClient) mockChat(systemPrompt string, messages []Message) string {
	var userQuery string
	for _, m := range messages {
		if m.Role == "user" { userQuery = m.Content }
	}
	q := strings.ToLower(userQuery)
	if strings.Contains(q, "你是谁") {
		return "我是一个全能AI助手，具备知识库、工具调用、推理、记忆和稳定执行能力。"
	}
	if strings.Contains(q, "后端工程师") {
		return "后端工程师负责服务器端逻辑开发：API设计、数据库、业务逻辑、系统架构、性能优化。常用Go/Java/Python/MySQL/Redis。"
	}
	return fmt.Sprintf("收到：「%s」——这是模拟LLM回复，接入真实API后会更智能。", userQuery)
}

// ══════════════════════════════════════════════════════════════
//  Stage 2: RAG Engine
// ══════════════════════════════════════════════════════════════

type Chunk struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
}

type TextSplitter struct {
	chunkSize int
	overlap   int
}

func NewTextSplitter(chunkSize, overlap int) *TextSplitter {
	return &TextSplitter{chunkSize: chunkSize, overlap: overlap}
}

func (s *TextSplitter) Split(text string) []Chunk {
	var chunks []Chunk
	id := 0
	step := s.chunkSize - s.overlap
	if step <= 0 { step = s.chunkSize }
	runes := []rune(text)
	for i := 0; i < len(runes); i += step {
		end := i + s.chunkSize
		if end > len(runes) { end = len(runes) }
		chunks = append(chunks, Chunk{ID: id, Content: string(runes[i:end])})
		id++
		if end >= len(runes) { break }
	}
	return chunks
}

type VectorStore struct {
	chunks   []Chunk
	vectors  [][]float64
	vocabMap map[string]int
	vocab    []string
}

func NewVectorStore() *VectorStore { return &VectorStore{vocabMap: make(map[string]int)} }

func tokenize(text string) []string {
	var tokens []string
	word := ""
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			if word != "" { tokens = append(tokens, strings.ToLower(word)); word = "" }
			tokens = append(tokens, string(r))
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			word += string(r)
		} else {
			if word != "" { tokens = append(tokens, strings.ToLower(word)); word = "" }
		}
	}
	if word != "" { tokens = append(tokens, strings.ToLower(word)) }
	return tokens
}

func (v *VectorStore) buildVocab(chunks []Chunk) {
	for _, c := range chunks {
		for _, t := range tokenize(c.Content) {
			if _, ok := v.vocabMap[t]; !ok {
				v.vocabMap[t] = len(v.vocab)
				v.vocab = append(v.vocab, t)
			}
		}
	}
}

func (v *VectorStore) textToVector(text string) []float64 {
	vec := make([]float64, len(v.vocabMap))
	for _, t := range tokenize(text) {
		if idx, ok := v.vocabMap[t]; ok { vec[idx]++ }
	}
	return vec
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) { return 0 }
	var dot, na, nb float64
	for i := range a { dot += a[i] * b[i]; na += a[i] * a[i]; nb += b[i] * b[i] }
	if na == 0 || nb == 0 { return 0 }
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func (v *VectorStore) Index(chunks []Chunk) {
	v.chunks = chunks
	v.buildVocab(chunks)
	v.vectors = make([][]float64, len(chunks))
	for i, c := range chunks { v.vectors[i] = v.textToVector(c.Content) }
}

type SearchResult struct {
	Chunk      Chunk   `json:"chunk"`
	Similarity float64 `json:"similarity"`
}

func (v *VectorStore) Search(query string, topK int) []SearchResult {
	qv := v.textToVector(query)
	results := make([]SearchResult, len(v.chunks))
	for i, cv := range v.vectors { results[i] = SearchResult{Chunk: v.chunks[i], Similarity: cosine(qv, cv)} }
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Similarity > results[i].Similarity { results[i], results[j] = results[j], results[i] }
		}
	}
	if topK > len(results) { topK = len(results) }
	return results[:topK]
}

type RAGEngine struct {
	cfg      *config.APIConfig
	store    *VectorStore
	splitter *TextSplitter
	loaded   bool
	inf      *Infrastructure
}

func NewRAGEngine(cfg *config.APIConfig, inf *Infrastructure) *RAGEngine {
	return &RAGEngine{cfg: cfg, store: NewVectorStore(), splitter: NewTextSplitter(cfg.ChunkSize, cfg.ChunkOverlap), inf: inf}
}

func (e *RAGEngine) Ingest(doc string) int {
	chunks := e.splitter.Split(doc)
	e.store.Index(chunks)
	e.loaded = true
	// 发布事件到 Kafka
	e.inf.PublishEvent("rag.ingest", fmt.Sprintf(`{"chunk_count":%d}`, len(chunks)))
	return len(chunks)
}

func (e *RAGEngine) Query(question string) (string, []SearchResult) {
	if !e.loaded { return "知识库为空，请先上传文档。", nil }
	results := e.store.Search(question, e.cfg.TopK)
	var parts []string
	for _, r := range results {
		if r.Similarity > 0.01 { parts = append(parts, r.Chunk.Content) }
	}
	context := strings.Join(parts, "\n\n")
	answer := fmt.Sprintf("基于知识库回答：\n\n问题「%s」的相关文档已检索到，上下文分析如下：\n%s", question, context)
	return answer, results
}

// ══════════════════════════════════════════════════════════════
//  Stage 3: Tool Agent
// ══════════════════════════════════════════════════════════════

type ToolParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  []ToolParam `json:"parameters"`
	Execute     func(params map[string]interface{}) (string, error)
}

func getTimeTool() Tool {
	return Tool{Name: "get_time", Description: "获取当前时间",
		Parameters: []ToolParam{{Name: "timezone", Type: "string", Description: "时区", Required: false}},
		Execute: func(p map[string]interface{}) (string, error) {
			loc := time.Local
			if v, ok := p["timezone"].(string); ok && v != "" {
				if l, err := time.LoadLocation(v); err == nil { loc = l }
			}
			return time.Now().In(loc).Format("2006-01-02 15:04:05"), nil
		}}
}

func getWeatherTool() Tool {
	return Tool{Name: "get_weather", Description: "获取天气信息",
		Parameters: []ToolParam{{Name: "city", Type: "string", Description: "城市", Required: true}},
		Execute: func(p map[string]interface{}) (string, error) {
			city, _ := p["city"].(string)
			db := map[string]string{"北京": "晴天 22°C", "东京": "多云 18°C 湿度65%", "上海": "小雨 20°C", "纽约": "晴天 15°C", "伦敦": "阴天 12°C"}
			if w, ok := db[city]; ok { return w, nil }
			return city + ": 晴天 20°C（模拟）", nil
		}}
}

func searchWebTool() Tool {
	return Tool{Name: "search_web", Description: "搜索互联网",
		Parameters: []ToolParam{{Name: "query", Type: "string", Description: "关键词", Required: true}},
		Execute: func(p map[string]interface{}) (string, error) {
			q, _ := p["query"].(string)
			db := map[string]string{
				"AI应用工程师": "AI应用工程师是将AI技术落地到业务的工程师，需ML基础、API开发、Prompt工程等能力。",
				"Go语言":    "Go是Google开发的开源编程语言，适用于高并发服务端应用。Docker即用Go开发。",
			}
			for k, v := range db { if strings.Contains(q, k) { return v, nil } }
			return fmt.Sprintf("关于「%s」的搜索结果（模拟）", q), nil
		}}
}

type ToolCallResult struct {
	ToolName   string                 `json:"tool_name"`
	Params     map[string]interface{} `json:"params"`
	ToolResult string                 `json:"tool_result"`
}

func decideTool(query string, tools map[string]Tool) *ToolCallResult {
	q := strings.ToLower(query)
	if strings.Contains(q, "几点") || strings.Contains(q, "时间") {
		params := map[string]interface{}{}
		if strings.Contains(q, "东京") { params["timezone"] = "Asia/Tokyo" }
		return &ToolCallResult{ToolName: "get_time", Params: params}
	}
	if strings.Contains(q, "天气") {
		city := "北京"
		for _, c := range []string{"东京", "北京", "上海", "纽约", "伦敦"} {
			if strings.Contains(q, c) { city = c; break }
		}
		return &ToolCallResult{ToolName: "get_weather", Params: map[string]interface{}{"city": city}}
	}
	if strings.Contains(q, "查") || strings.Contains(q, "搜索") || strings.Contains(q, "是什么") {
		return &ToolCallResult{ToolName: "search_web", Params: map[string]interface{}{"query": query}}
	}
	return nil
}

// ══════════════════════════════════════════════════════════════
//  Stage 5: Memory
// ══════════════════════════════════════════════════════════════

type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

type ShortTermMemory struct {
	Messages []ConversationMessage `json:"messages"`
	MaxTurns int                   `json:"max_turns"`
}

func NewShortTermMemory(maxTurns int) *ShortTermMemory { return &ShortTermMemory{MaxTurns: maxTurns} }

func (m *ShortTermMemory) Add(role, content string) {
	m.Messages = append(m.Messages, ConversationMessage{Role: role, Content: content, Timestamp: time.Now().Format("15:04:05")})
	max := m.MaxTurns * 2
	if len(m.Messages) > max { m.Messages = m.Messages[len(m.Messages)-max:] }
}

type MemoryItem struct {
	ID         int     `json:"id"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
}

type LongTermMemory struct {
	Items   []MemoryItem
	vocabID map[string]int
	vocab   []string
	nextID  int
}

func NewLongTermMemory() *LongTermMemory { return &LongTermMemory{vocabID: make(map[string]int)} }

func (m *LongTermMemory) buildVocab(text string) {
	for _, t := range tokenize(text) {
		if _, ok := m.vocabID[t]; !ok {
			m.vocabID[t] = len(m.vocab)
			m.vocab = append(m.vocab, t)
		}
	}
}

func (m *LongTermMemory) textToVector(text string) []float64 {
	vec := make([]float64, len(m.vocabID))
	for _, t := range tokenize(text) { if idx, ok := m.vocabID[t]; ok { vec[idx]++ } }
	return vec
}

func (m *LongTermMemory) Store(content string, importance float64) {
	for _, item := range m.Items { m.buildVocab(item.Content) }
	m.buildVocab(content)
	m.Items = append(m.Items, MemoryItem{ID: m.nextID, Content: content, Importance: importance})
	m.nextID++
}

func (m *LongTermMemory) Recall(query string, topK int) []MemoryItem {
	if len(m.Items) == 0 { return nil }
	qv := m.textToVector(query)
	type scored struct { item MemoryItem; s float64 }
	var items []scored
	for _, item := range m.Items {
		iv := m.textToVector(item.Content)
		items = append(items, scored{item: item, s: cosine(qv, iv)*0.7 + item.Importance*0.3})
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ { if items[j].s > items[i].s { items[i], items[j] = items[j], items[i] } }
	}
	if topK > len(items) { topK = len(items) }
	result := make([]MemoryItem, topK)
	for i := 0; i < topK; i++ { result[i] = items[i].item }
	return result
}

type UserPreference struct {
	Data map[string]string `json:"data"`
}

func NewUserPreference() *UserPreference { return &UserPreference{Data: make(map[string]string)} }

func extractImportantInfo(msg string) (key, value string, ok bool) {
	if strings.Contains(msg, "我喜欢") { parts := strings.SplitN(msg, "喜欢", 2); if len(parts) == 2 { return "喜好", strings.TrimSpace(parts[1]), true } }
	if strings.Contains(msg, "我爱") { parts := strings.SplitN(msg, "爱", 2); if len(parts) == 2 { return "喜好", strings.TrimSpace(parts[1]), true } }
	if strings.Contains(msg, "我叫") { parts := strings.SplitN(msg, "叫", 2); if len(parts) == 2 { return "姓名", strings.TrimSpace(parts[1]), true } }
	return "", "", false
}

// ══════════════════════════════════════════════════════════════
//  Stage 6: Harness
// ══════════════════════════════════════════════════════════════

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
)

type TaskStep struct {
	ID         int               `json:"id"`
	Name       string            `json:"name"`
	ToolName   string            `json:"tool_name"`
	Params     map[string]string `json:"params"`
	Status     StepStatus        `json:"status"`
	Result     string            `json:"result,omitempty"`
	Error      string            `json:"error,omitempty"`
	RetryCount int               `json:"retry_count"`
}

type TaskState struct {
	TaskID      string     `json:"task_id"`
	Query       string     `json:"query"`
	Status      string     `json:"status"`
	Steps       []TaskStep `json:"steps"`
	CurrentStep int        `json:"current_step"`
	Result      string     `json:"result,omitempty"`
}

type Snapshot struct {
	State     TaskState `json:"state"`
	Timestamp string    `json:"timestamp"`
}

// ══════════════════════════════════════════════════════════════
//  Unified Agent
// ══════════════════════════════════════════════════════════════

type ReActStepType string

const (
	ReActThought     ReActStepType = "Thought"
	ReActAction      ReActStepType = "Action"
	ReActObservation ReActStepType = "Observation"
	ReActFinalAnswer ReActStepType = "Final Answer"
)

type ReActStep struct {
	Type    ReActStepType     `json:"type"`
	Content string            `json:"content"`
	Tool    string            `json:"tool,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

type UnifiedAgent struct {
	cfg       *config.APIConfig
	llm       *LLMClient
	rag       *RAGEngine
	tools     map[string]Tool
	stm       *ShortTermMemory
	ltm       *LongTermMemory
	pref      *UserPreference
	snapshots []Snapshot
	task      *TaskState
	inf       *Infrastructure
}

func NewUnifiedAgent(cfg *config.APIConfig, inf *Infrastructure) *UnifiedAgent {
	tools := []Tool{getTimeTool(), getWeatherTool(), searchWebTool()}
	tm := make(map[string]Tool)
	for _, t := range tools { tm[t.Name] = t }
	return &UnifiedAgent{
		cfg:   cfg,
		llm:   NewLLMClient(cfg),
		rag:   NewRAGEngine(cfg, inf),
		tools: tm,
		stm:   NewShortTermMemory(cfg.ShortTermMaxTurns),
		ltm:   NewLongTermMemory(),
		pref:  NewUserPreference(),
		inf:   inf,
	}
}

func (a *UnifiedAgent) Process(query string) *UnifiedResponse {
	resp := &UnifiedResponse{Query: query, Mode: "chat"}

	// Stage 5: Memory
	a.stm.Add("user", query)
	if key, value, ok := extractImportantInfo(query); ok {
		a.pref.Data[key] = value
		a.ltm.Store(fmt.Sprintf("用户%s: %s", key, value), 0.8)
		resp.ExtractedInfo = fmt.Sprintf("已记住：%s = %s", key, value)
		// 持久化到 PG
		a.inf.SavePreferenceToPG("default", key, value)
	}

	// 智能路由
	switch {
	case a.needReAct(query):
		resp.Mode = "react"
		answer, steps, task := a.runReAct(query)
		resp.Answer = answer
		resp.Steps = steps
		resp.Task = task
	case a.needTool(query):
		resp.Mode = "tool"
		answer, toolCall := a.runToolAgent(query)
		resp.Answer = answer
		resp.ToolCall = toolCall
	case a.needRAG(query):
		resp.Mode = "rag"
		answer, searchResults := a.rag.Query(query)
		resp.Answer = answer
		resp.SearchResults = searchResults
	default:
		resp.Mode = "chat"
		memContext := a.buildMemoryContext(query)
		if memContext != "" {
			resp.Mode = "memory"
			resp.Answer = a.generateWithMemory(query, memContext)
		} else {
			resp.Answer = a.llm.Chat("你是一个简洁的AI助手", []Message{{Role: "user", Content: query}})
		}
	}

	a.stm.Add("assistant", resp.Answer)

	// 发布事件到 Kafka
	eventData, _ := json.Marshal(map[string]interface{}{"query": query, "mode": resp.Mode})
	a.inf.PublishEvent("agent.chat", string(eventData))

	resp.ShortTermCount = len(a.stm.Messages)
	resp.LongTermCount = len(a.ltm.Items)
	resp.Preferences = a.pref.Data
	return resp
}

func (a *UnifiedAgent) needTool(query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(q, "几点") || strings.Contains(q, "时间") ||
		strings.Contains(q, "天气") || strings.Contains(q, "查") ||
		strings.Contains(q, "搜索") || strings.Contains(q, "是什么")
}

func (a *UnifiedAgent) needRAG(query string) bool {
	return a.rag.loaded && !a.needTool(query) && !a.needReAct(query)
}

func (a *UnifiedAgent) needReAct(query string) bool {
	q := strings.ToLower(query)
	count := 0
	if strings.Contains(q, "时间") || strings.Contains(q, "几点") { count++ }
	if strings.Contains(q, "天气") { count++ }
	if strings.Contains(q, "总结") || strings.Contains(q, "汇总") { count++ }
	if strings.Contains(q, "查") || strings.Contains(q, "搜索") { count++ }
	return count >= 2
}

func (a *UnifiedAgent) runToolAgent(query string) (string, *ToolCallResult) {
	tc := decideTool(query, a.tools)
	if tc == nil { return "我无法处理这个请求。", nil }
	tool, ok := a.tools[tc.ToolName]
	if !ok { return fmt.Sprintf("工具 %s 不存在", tc.ToolName), tc }
	result, err := tool.Execute(tc.Params)
	if err != nil { return fmt.Sprintf("工具执行失败: %v", err), tc }
	tc.ToolResult = result
	return fmt.Sprintf("根据查询结果：%s", result), tc
}

func (a *UnifiedAgent) runReAct(query string) (string, []ReActStep, *TaskState) {
	var reactSteps []ReActStep
	var observations []string
	taskSteps := a.planTaskSteps(query)
	a.task = &TaskState{
		TaskID: fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Query:  query, Status: "running", Steps: taskSteps,
	}
	a.snapshots = nil
	a.saveSnapshot()

	for i := range a.task.Steps {
		ts := &a.task.Steps[i]
		a.task.CurrentStep = i
		ts.Status = StepRunning
		reactSteps = append(reactSteps, ReActStep{Type: ReActThought, Content: fmt.Sprintf("需要执行: %s", ts.Name)})

		if ts.ToolName == "" {
			reactSteps = append(reactSteps, ReActStep{Type: ReActFinalAnswer, Content: "汇总所有结果"})
			var results []string
			for _, s := range a.task.Steps { if s.Status == StepDone { results = append(results, s.Result) } }
			ts.Result = strings.Join(results, "；")
			ts.Status = StepDone
			break
		}

		reactSteps = append(reactSteps, ReActStep{Type: ReActAction, Content: fmt.Sprintf("调用 %s", ts.ToolName), Tool: ts.ToolName, Params: ts.Params})
		ok := a.executeStepWithRetry(ts)
		if ok {
			ts.Status = StepDone
			reactSteps = append(reactSteps, ReActStep{Type: ReActObservation, Content: ts.Result})
			observations = append(observations, ts.Result)
		} else {
			ts.Status = StepFailed
			reactSteps = append(reactSteps, ReActStep{Type: ReActObservation, Content: fmt.Sprintf("失败: %s", ts.Error)})
		}
		a.saveSnapshot()
	}

	answer := strings.Join(observations, "；")
	if len(observations) > 1 { answer = "综合查询结果：" + answer }
	reactSteps = append(reactSteps, ReActStep{Type: ReActFinalAnswer, Content: answer})
	a.task.Result = answer
	a.task.Status = "completed"
	return answer, reactSteps, a.task
}

func (a *UnifiedAgent) planTaskSteps(query string) []TaskStep {
	q := strings.ToLower(query)
	var steps []TaskStep
	id := 1
	if strings.Contains(q, "时间") || strings.Contains(q, "几点") {
		steps = append(steps, TaskStep{ID: id, Name: "查询时间", ToolName: "get_time", Params: map[string]string{}, Status: StepPending})
		id++
	}
	if strings.Contains(q, "天气") {
		city := "北京"
		for _, c := range []string{"东京", "北京", "上海"} { if strings.Contains(q, c) { city = c; break } }
		steps = append(steps, TaskStep{ID: id, Name: fmt.Sprintf("查询%s天气", city), ToolName: "get_weather", Params: map[string]string{"city": city}, Status: StepPending})
		id++
	}
	if strings.Contains(q, "总结") || strings.Contains(q, "汇总") {
		steps = append(steps, TaskStep{ID: id, Name: "汇总回答", ToolName: "", Params: map[string]string{}, Status: StepPending})
	}
	return steps
}

func (a *UnifiedAgent) executeStepWithRetry(step *TaskStep) bool {
	tool, ok := a.tools[step.ToolName]
	if !ok { return false }
	for attempt := 0; attempt < a.cfg.MaxRetries; attempt++ {
		result, err := tool.Execute(map[string]interface{}{})
		_ = step.Params
		if err == nil { step.Result = result; return true }
		step.RetryCount = attempt + 1
		step.Error = err.Error()
		time.Sleep(time.Duration(a.cfg.RetryDelayMs) * time.Millisecond)
	}
	return false
}

func (a *UnifiedAgent) saveSnapshot() {
	snap := Snapshot{State: *a.task, Timestamp: time.Now().Format("15:04:05")}
	data, _ := json.Marshal(a.task)
	json.Unmarshal(data, &snap.State)
	a.snapshots = append(a.snapshots, snap)
	// 持久化快照到 PostgreSQL
	stateJSON, _ := json.Marshal(snap.State)
	a.inf.SaveSnapshotToPG(a.task.TaskID, stateJSON)
}

func (a *UnifiedAgent) buildMemoryContext(query string) string {
	var parts []string
	if len(a.pref.Data) > 0 {
		var items []string
		for k, v := range a.pref.Data { items = append(items, fmt.Sprintf("%s: %s", k, v)) }
		parts = append(parts, "【用户偏好】\n"+strings.Join(items, "\n"))
	}
	ltmItems := a.ltm.Recall(query, a.cfg.LongTermTopK)
	if len(ltmItems) > 0 {
		var items []string
		for _, item := range ltmItems { items = append(items, item.Content) }
		parts = append(parts, "【长期记忆】\n"+strings.Join(items, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func (a *UnifiedAgent) generateWithMemory(query, memContext string) string {
	if strings.Contains(query, "推荐") && strings.Contains(memContext, "周杰伦") {
		return "根据你的偏好，推荐：\n1. 周杰伦 - 晴天\n2. 周杰伦 - 稻香\n3. 林俊杰 - 江南（风格相似）"
	}
	return fmt.Sprintf("基于你的个人记忆回答：「%s」", query)
}

// ══════════════════════════════════════════════════════════════
//  Unified Response
// ══════════════════════════════════════════════════════════════

type UnifiedResponse struct {
	Query          string            `json:"query"`
	Answer         string            `json:"answer"`
	Mode           string            `json:"mode"`
	Steps          []ReActStep       `json:"steps,omitempty"`
	ToolCall       *ToolCallResult   `json:"tool_call,omitempty"`
	SearchResults  []SearchResult    `json:"search_results,omitempty"`
	Task           *TaskState        `json:"task,omitempty"`
	ExtractedInfo  string            `json:"extracted_info,omitempty"`
	ShortTermCount int               `json:"short_term_count"`
	LongTermCount  int               `json:"long_term_count"`
	Preferences    map[string]string `json:"preferences"`
}

// ══════════════════════════════════════════════════════════════
//  HTTP
// ══════════════════════════════════════════════════════════════

var (
	agent  *UnifiedAgent
	appCfg *config.APIConfig
	infra  *Infrastructure
)

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
	var req struct{ Message string `json:"message"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "Invalid request", http.StatusBadRequest); return }
	resp := agent.Process(req.Message)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
	var req struct{ Content string `json:"content"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "Invalid request", http.StatusBadRequest); return }
	count := agent.rag.Ingest(req.Content)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"chunk_count": count, "chunks": agent.rag.store.chunks})
}

func handleMemory(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"short_term": agent.stm.Messages,
		"long_term":  agent.ltm.Items,
		"preference": agent.pref.Data,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleTools(w http.ResponseWriter, r *http.Request) {
	type info struct{ Name, Desc string; Params []ToolParam }
	var tools []info
	for _, t := range agent.tools { tools = append(tools, info{Name: t.Name, Desc: t.Description, Params: t.Parameters}) }
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tools)
}

func handleSnapshots(w http.ResponseWriter, r *http.Request) {
	var infos []map[string]interface{}
	for i, snap := range agent.snapshots {
		infos = append(infos, map[string]interface{}{"index": i, "timestamp": snap.Timestamp, "steps": len(snap.State.Steps)})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	var chunkPreviews []map[string]interface{}
	for _, c := range agent.rag.store.chunks {
		preview := c.Content
		if utf8.RuneCountInString(preview) > 60 {
			runes := []rune(preview)
			preview = string(runes[:60]) + "..."
		}
		chunkPreviews = append(chunkPreviews, map[string]interface{}{"id": c.ID, "content": preview})
	}
	resp := map[string]interface{}{
		"rag_loaded":        agent.rag.loaded,
		"rag_chunks":        chunkPreviews,
		"short_term_count":  len(agent.stm.Messages),
		"long_term_count":   len(agent.ltm.Items),
		"preferences":       agent.pref.Data,
		"tools_count":       len(agent.tools),
		"llm_model":         appCfg.LLMModel,
		"embedding_model":   appCfg.EmbeddingModel,
		"is_mock":           !appCfg.IsRealLLM(),
		"infrastructure":    infra.ready,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	appCfg = config.DefaultConfig()

	// 初始化基础设施
	log.Println("🔧 正在连接基础设施...")
	infra = NewInfrastructure(appCfg)
	defer infra.Close()

	// 初始化 Agent
	agent = NewUnifiedAgent(appCfg, infra)

	// API 路由
	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/api/memory", handleMemory)
	http.HandleFunc("/api/tools", handleTools)
	http.HandleFunc("/api/snapshots", handleSnapshots)
	http.HandleFunc("/api/status", handleStatus)

	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + appCfg.ServerPort
	fmt.Println("╔══════════════════════════════════════════════════╗")
	fmt.Println("║  Final Stage: 全阶段整合 AI 助手                ║")
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Printf("║  服务: http://localhost%s                       ║\n", addr)
	fmt.Printf("║  LLM: %s  |  Embedding: %s        \n", appCfg.LLMModel, appCfg.EmbeddingModel)
	fmt.Printf("║  Milvus: %s  |  PG: %s:%d       \n", infra.ready.Milvus, appCfg.PGHost, appCfg.PGPort)
	fmt.Printf("║  ES: %s  |  Kafka: %s        \n", infra.ready.ES, infra.ready.Kafka)
	fmt.Println("╠══════════════════════════════════════════════════╣")
	fmt.Println("║  ✅ Stage 1: LLM Chat   ✅ Stage 2: RAG         ║")
	fmt.Println("║  ✅ Stage 3: Tool Agent ✅ Stage 4: ReAct        ║")
	fmt.Println("║  ✅ Stage 5: Memory     ✅ Stage 6: Harness      ║")
	fmt.Println("╚══════════════════════════════════════════════════╝")

	log.Fatal(http.ListenAndServe(addr, nil))
}
