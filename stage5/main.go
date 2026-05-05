package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"stage5/config"
	"strings"
	"time"
)

// ==================== 短期记忆 ====================

type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

type ShortTermMemory struct {
	Messages []ConversationMessage `json:"messages"`
	MaxTurns int                   `json:"max_turns"`
}

func NewShortTermMemory(maxTurns int) *ShortTermMemory {
	return &ShortTermMemory{MaxTurns: maxTurns}
}

func (m *ShortTermMemory) Add(role, content string) {
	m.Messages = append(m.Messages, ConversationMessage{
		Role: role, Content: content, Timestamp: time.Now().Format("15:04:05"),
	})
	max := m.MaxTurns * 2
	if len(m.Messages) > max {
		m.Messages = m.Messages[len(m.Messages)-max:]
	}
}

// ==================== 长期记忆 ====================

type MemoryItem struct {
	ID         int     `json:"id"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
}

type LongTermMemory struct {
	Items   []MemoryItem `json:"items"`
	vocabID map[string]int
	vocab   []string
	nextID  int
}

func NewLongTermMemory() *LongTermMemory {
	return &LongTermMemory{vocabID: make(map[string]int)}
}

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
	for _, t := range tokenize(text) {
		if idx, ok := m.vocabID[t]; ok { vec[idx]++ }
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

func (m *LongTermMemory) Store(content string, importance float64) {
	for _, item := range m.Items { m.buildVocab(item.Content) }
	m.buildVocab(content)
	m.Items = append(m.Items, MemoryItem{ID: m.nextID, Content: content, Importance: importance})
	m.nextID++
	// 重建所有向量
	for i := range m.Items {
		_ = m.textToVector(m.Items[i].Content)
	}
}

func (m *LongTermMemory) Recall(query string, topK int) []MemoryItem {
	if len(m.Items) == 0 { return nil }
	qv := m.textToVector(query)
	type scored struct { item MemoryItem; score float64 }
	var items []scored
	for _, item := range m.Items {
		iv := m.textToVector(item.Content)
		s := cosine(qv, iv)*0.7 + item.Importance*0.3
		items = append(items, scored{item: item, score: s})
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].score > items[i].score { items[i], items[j] = items[j], items[i] }
		}
	}
	if topK > len(items) { topK = len(items) }
	result := make([]MemoryItem, topK)
	for i := 0; i < topK; i++ { result[i] = items[i].item }
	return result
}

// ==================== 用户偏好 ====================

type UserPreference struct {
	Data map[string]string `json:"data"`
}

func NewUserPreference() *UserPreference {
	return &UserPreference{Data: make(map[string]string)}
}

// ==================== 记忆管理器 ====================

type MemoryManager struct {
	ShortTerm  *ShortTermMemory  `json:"short_term"`
	LongTerm   *LongTermMemory   `json:"long_term"`
	Preference *UserPreference   `json:"preference"`
}

func NewMemoryManager(cfg *config.APIConfig) *MemoryManager {
	return &MemoryManager{
		ShortTerm:  NewShortTermMemory(cfg.ShortTermMaxTurns),
		LongTerm:   NewLongTermMemory(),
		Preference: NewUserPreference(),
	}
}

func (m *MemoryManager) BuildContext(query string) string {
	var parts []string
	if len(m.Preference.Data) > 0 {
		var items []string
		for k, v := range m.Preference.Data { items = append(items, fmt.Sprintf("%s: %s", k, v)) }
		parts = append(parts, "【用户偏好】\n"+strings.Join(items, "\n"))
	}
	longMemItems := m.LongTerm.Recall(query, 3)
	if len(longMemItems) > 0 {
		var items []string
		for _, item := range longMemItems { items = append(items, item.Content) }
		parts = append(parts, "【长期记忆】\n"+strings.Join(items, "\n"))
	}
	if len(m.ShortTerm.Messages) > 0 {
		var items []string
		for _, msg := range m.ShortTerm.Messages {
			label := "用户"
			if msg.Role == "assistant" { label = "助手" }
			items = append(items, fmt.Sprintf("%s: %s", label, msg.Content))
		}
		parts = append(parts, "【近期对话】\n"+strings.Join(items, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// ==================== LLM + 信息提取 ====================

func extractImportantInfo(userMsg string) (key, value string, isImportant bool) {
	if strings.Contains(userMsg, "我喜欢") {
		parts := strings.SplitN(userMsg, "喜欢", 2)
		if len(parts) == 2 { return "喜好", strings.TrimSpace(parts[1]), true }
	}
	if strings.Contains(userMsg, "我爱") {
		parts := strings.SplitN(userMsg, "爱", 2)
		if len(parts) == 2 { return "喜好", strings.TrimSpace(parts[1]), true }
	}
	if strings.Contains(userMsg, "我叫") {
		parts := strings.SplitN(userMsg, "叫", 2)
		if len(parts) == 2 { return "姓名", strings.TrimSpace(parts[1]), true }
	}
	return "", "", false
}

func generateReply(query, memoryContext string) string {
	if strings.Contains(query, "推荐") || strings.Contains(query, "建议") {
		if strings.Contains(memoryContext, "周杰伦") {
			return "根据你的偏好，推荐：\n1. 周杰伦 - 晴天\n2. 周杰伦 - 稻香\n3. 林俊杰 - 江南（风格相似）"
		}
		return "推荐热门音乐：\n1. 周杰伦 - 晴天\n2. 邓紫棋 - 光年之外\n3. 毛不易 - 消愁"
	}
	if memoryContext != "" {
		return fmt.Sprintf("基于你的个人记忆，我回答：「%s」", query)
	}
	return fmt.Sprintf("收到你的问题：「%s」。接入真实LLM后会有更智能的回答。", query)
}

// ==================== HTTP ====================

var memMgr *MemoryManager

type ChatRequest struct {
	Message string `json:"message"`
}

type ChatResponse struct {
	Reply          string                       `json:"reply"`
	ExtractedInfo  string                       `json:"extracted_info,omitempty"`
	ShortTermCount int                          `json:"short_term_count"`
	LongTermCount  int                          `json:"long_term_count"`
	Preferences    map[string]string            `json:"preferences"`
}

type MemoryResponse struct {
	ShortTerm  []ConversationMessage `json:"short_term"`
	LongTerm   []MemoryItem          `json:"long_term"`
	Preference map[string]string     `json:"preference"`
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	memMgr.ShortTerm.Add("user", req.Message)

	var extractedInfo string
	if key, value, ok := extractImportantInfo(req.Message); ok {
		memMgr.Preference.Data[key] = value
		memMgr.LongTerm.Store(fmt.Sprintf("用户%s: %s", key, value), 0.8)
		extractedInfo = fmt.Sprintf("已记住：%s = %s", key, value)
	}

	context := memMgr.BuildContext(req.Message)
	reply := generateReply(req.Message, context)
	memMgr.ShortTerm.Add("assistant", reply)

	resp := ChatResponse{
		Reply:          reply,
		ExtractedInfo:  extractedInfo,
		ShortTermCount: len(memMgr.ShortTerm.Messages),
		LongTermCount:  len(memMgr.LongTerm.Items),
		Preferences:    memMgr.Preference.Data,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleMemory(w http.ResponseWriter, r *http.Request) {
	resp := MemoryResponse{
		ShortTerm:  memMgr.ShortTerm.Messages,
		LongTerm:   memMgr.LongTerm.Items,
		Preference: memMgr.Preference.Data,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	cfg := config.DefaultConfig()
	memMgr = NewMemoryManager(cfg)

	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/memory", handleMemory)
	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + cfg.ServerPort
	fmt.Println("========================================")
	fmt.Println("  Stage 5: 带记忆的AI助手 (Memory)")
	fmt.Println("========================================")
	fmt.Printf("  服务地址: http://localhost%s\n", addr)
	fmt.Printf("  聊天模型: %s\n", cfg.LLMModel)
	fmt.Printf("  向量模型: %s\n", cfg.EmbeddingModel)
	fmt.Printf("  短期记忆: 最近 %d 轮\n", cfg.ShortTermMaxTurns)
	fmt.Println("========================================")
	log.Fatal(http.ListenAndServe(addr, nil))
}
