// Package memory 实现三层记忆系统：
//   - ShortTerm  短期记忆：近 N 轮对话滑动窗口
//   - LongTerm   长期记忆：支持语义向量（embedding）或 TF 词袋降级
//   - Preference 用户偏好：从对话中自动提取并持久化的键值对
package memory

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ─────────────────────────────── 短期记忆 ────────────────────────────────

// ConversationMessage 是单条对话记录
type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// ShortTerm 维护最近 MaxTurns 轮的对话上下文
type ShortTerm struct {
	Messages []ConversationMessage `json:"messages"`
	MaxTurns int                   `json:"max_turns"`
}

// NewShortTerm 创建短期记忆，maxTurns 为保留的最大对话轮数
func NewShortTerm(maxTurns int) *ShortTerm {
	return &ShortTerm{MaxTurns: maxTurns}
}

// Add 追加一条消息，超出窗口时自动丢弃最早记录
func (m *ShortTerm) Add(role, content string) {
	m.Messages = append(m.Messages, ConversationMessage{
		Role:      role,
		Content:   content,
		Timestamp: time.Now().Format("15:04:05"),
	})
	max := m.MaxTurns * 2 // 每轮 = user + assistant 两条
	if len(m.Messages) > max {
		m.Messages = m.Messages[len(m.Messages)-max:]
	}
}

// ─────────────────────────────── 长期记忆 ────────────────────────────────

// Item 是长期记忆的存储单元
type Item struct {
	ID         int       `json:"id"`
	Content    string    `json:"content"`
	Importance float64   `json:"importance"` // 0~1，越高越重要
	Embedding  []float64 `json:"embedding,omitempty"`
}

// LongTerm 支持语义向量召回（embedding 优先）或 TF 词袋降级
type LongTerm struct {
	Items   []Item
	vocabID map[string]int
	vocab   []string
	nextID  int
}

// NewLongTerm 创建长期记忆
func NewLongTerm() *LongTerm {
	return &LongTerm{vocabID: make(map[string]int)}
}

func (m *LongTerm) buildVocab(text string) {
	for _, t := range tokenize(text) {
		if _, ok := m.vocabID[t]; !ok {
			m.vocabID[t] = len(m.vocab)
			m.vocab = append(m.vocab, t)
		}
	}
}

func (m *LongTerm) textToVector(text string) []float64 {
	vec := make([]float64, len(m.vocabID))
	for _, t := range tokenize(text) {
		if idx, ok := m.vocabID[t]; ok {
			vec[idx]++
		}
	}
	return vec
}

// Store 将内容存入长期记忆（embedding 可选，传 nil 则使用 TF 降级）
func (m *LongTerm) Store(content string, importance float64, embedding []float64) {
	for _, item := range m.Items {
		m.buildVocab(item.Content)
	}
	m.buildVocab(content)
	m.Items = append(m.Items, Item{
		ID:         m.nextID,
		Content:    content,
		Importance: importance,
		Embedding:  embedding,
	})
	m.nextID++
}

// StoreItem 直接插入已有 Item（用于从 DB 恢复数据）
func (m *LongTerm) StoreItem(item Item) {
	m.buildVocab(item.Content)
	if item.ID >= m.nextID {
		m.nextID = item.ID + 1
	}
	m.Items = append(m.Items, item)
}

// Recall 从长期记忆中召回与 query 最相关的 topK 条
// 优先使用 embedding 余弦相似度，若无 embedding 则退回 TF
func (m *LongTerm) Recall(query string, topK int, queryEmbedding []float64) []Item {
	if len(m.Items) == 0 {
		return nil
	}
	type scored struct {
		item Item
		s    float64
	}
	var items []scored
	for _, item := range m.Items {
		var sim float64
		if len(queryEmbedding) > 0 && len(item.Embedding) == len(queryEmbedding) {
			sim = cosine(queryEmbedding, item.Embedding)
		} else {
			// TF 降级
			m.buildVocab(query)
			qv := m.textToVector(query)
			iv := m.textToVector(item.Content)
			if len(qv) < len(iv) {
				qv = append(qv, make([]float64, len(iv)-len(qv))...)
			} else if len(iv) < len(qv) {
				iv = append(iv, make([]float64, len(qv)-len(iv))...)
			}
			sim = cosine(qv, iv)
		}
		items = append(items, scored{item: item, s: sim*0.7 + item.Importance*0.3})
	}
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].s > items[i].s {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
	if topK > len(items) {
		topK = len(items)
	}
	result := make([]Item, topK)
	for i := range result {
		result[i] = items[i].item
	}
	return result
}

// ─────────────────────────────── 用户偏好 ────────────────────────────────

// Preference 以键值对形式存储用户偏好信息
type Preference struct {
	Data map[string]string `json:"data"`
}

// NewPreference 创建用户偏好存储
func NewPreference() *Preference {
	return &Preference{Data: make(map[string]string)}
}

// Save 保存单条偏好
func (p *Preference) Save(key, value string) {
	if key != "" && value != "" {
		p.Data[key] = value
	}
}

// SaveBatch 批量保存偏好（从 LLM 提取结果）
func (p *Preference) SaveBatch(kvs map[string]string) {
	for k, v := range kvs {
		if k != "" && v != "" {
			p.Data[k] = v
		}
	}
}

// ExtractAndSave 从对话文本中用规则提取偏好（兜底，LLM 提取优先）
func (p *Preference) ExtractAndSave(msg string) (key, value string, ok bool) {
	if strings.Contains(msg, "我喜欢") {
		parts := strings.SplitN(msg, "喜欢", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			key, value = "喜好", strings.TrimSpace(parts[1])
			p.Data[key] = value
			return key, value, true
		}
	}
	if strings.Contains(msg, "我爱") {
		parts := strings.SplitN(msg, "爱", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			key, value = "喜好", strings.TrimSpace(parts[1])
			p.Data[key] = value
			return key, value, true
		}
	}
	if strings.Contains(msg, "我叫") {
		parts := strings.SplitN(msg, "叫", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			key, value = "姓名", strings.TrimSpace(parts[1])
			p.Data[key] = value
			return key, value, true
		}
	}
	return "", "", false
}

// BuildContext 将偏好数据格式化为给 LLM 的上下文字符串
func (p *Preference) BuildContext() string {
	if len(p.Data) == 0 {
		return ""
	}
	var items []string
	for k, v := range p.Data {
		items = append(items, fmt.Sprintf("%s: %s", k, v))
	}
	return "【用户偏好】\n" + strings.Join(items, "\n")
}

// ─────────────────────────────── 内部工具函数 ────────────────────────────

// tokenize 将文本切成词元（中文逐字，英文按单词）
func tokenize(text string) []string {
	var tokens []string
	word := ""
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			if word != "" {
				tokens = append(tokens, strings.ToLower(word))
				word = ""
			}
			tokens = append(tokens, string(r))
		} else if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			word += string(r)
		} else {
			if word != "" {
				tokens = append(tokens, strings.ToLower(word))
				word = ""
			}
		}
	}
	if word != "" {
		tokens = append(tokens, strings.ToLower(word))
	}
	return tokens
}

// cosine 计算两个向量的余弦相似度
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
