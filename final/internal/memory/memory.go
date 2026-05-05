// Package memory 实现三层记忆系统：
//   - ShortTerm  短期记忆：近 N 轮对话滑动窗口
//   - LongTerm   长期记忆：TF 词袋向量 + 重要性加权的语义召回
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
	ID         int     `json:"id"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"` // 0~1，越高越重要
}

// LongTerm 基于 TF 词袋向量实现语义召回，重要性参与打分加权
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

// Store 将内容存入长期记忆，importance 决定后续召回权重
func (m *LongTerm) Store(content string, importance float64) {
	for _, item := range m.Items {
		m.buildVocab(item.Content)
	}
	m.buildVocab(content)
	m.Items = append(m.Items, Item{ID: m.nextID, Content: content, Importance: importance})
	m.nextID++
}

// Recall 从长期记忆中召回与 query 最相关的 topK 条（语义 0.7 + 重要性 0.3）
func (m *LongTerm) Recall(query string, topK int) []Item {
	if len(m.Items) == 0 {
		return nil
	}
	qv := m.textToVector(query)
	type scored struct {
		item Item
		s    float64
	}
	var items []scored
	for _, item := range m.Items {
		iv := m.textToVector(item.Content)
		items = append(items, scored{
			item: item,
			s:    cosine(qv, iv)*0.7 + item.Importance*0.3,
		})
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

// ExtractAndSave 从对话文本中提取偏好并保存，返回 (key, value, 是否提取成功)
func (p *Preference) ExtractAndSave(msg string) (key, value string, ok bool) {
	key, value, ok = extractInfo(msg)
	if ok {
		p.Data[key] = value
	}
	return
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

// extractInfo 从消息中提取偏好键值（规则驱动）
func extractInfo(msg string) (key, value string, ok bool) {
	if strings.Contains(msg, "我喜欢") {
		parts := strings.SplitN(msg, "喜欢", 2)
		if len(parts) == 2 {
			return "喜好", strings.TrimSpace(parts[1]), true
		}
	}
	if strings.Contains(msg, "我爱") {
		parts := strings.SplitN(msg, "爱", 2)
		if len(parts) == 2 {
			return "喜好", strings.TrimSpace(parts[1]), true
		}
	}
	if strings.Contains(msg, "我叫") {
		parts := strings.SplitN(msg, "叫", 2)
		if len(parts) == 2 {
			return "姓名", strings.TrimSpace(parts[1]), true
		}
	}
	return "", "", false
}

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
