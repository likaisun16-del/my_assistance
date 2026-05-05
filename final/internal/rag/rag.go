// Package rag 实现检索增强生成（Retrieval-Augmented Generation）。
// 包含：文本分割器、TF 词袋向量存储、余弦相似度检索、RAG 引擎。
package rag

import (
	"final/config"
	"final/internal/infra"
	"fmt"
	"math"
	"strings"
)

// ─────────────────────────────── 文本分割 ────────────────────────────────

// Chunk 是文本切片单元
type Chunk struct {
	ID      int    `json:"id"`
	Content string `json:"content"`
}

// TextSplitter 按字符窗口将长文本切成有重叠的 Chunk
type TextSplitter struct {
	chunkSize int
	overlap   int
}

// NewTextSplitter 创建文本分割器
func NewTextSplitter(chunkSize, overlap int) *TextSplitter {
	return &TextSplitter{chunkSize: chunkSize, overlap: overlap}
}

// Split 将文本切分为 Chunk 列表（Unicode 安全）
func (s *TextSplitter) Split(text string) []Chunk {
	var chunks []Chunk
	step := s.chunkSize - s.overlap
	if step <= 0 {
		step = s.chunkSize
	}
	runes := []rune(text)
	id := 0
	for i := 0; i < len(runes); i += step {
		end := i + s.chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, Chunk{ID: id, Content: string(runes[i:end])})
		id++
		if end >= len(runes) {
			break
		}
	}
	return chunks
}

// ─────────────────────────────── 向量存储 ────────────────────────────────

// VectorStore 是基于 TF 词袋的内存向量库
type VectorStore struct {
	Chunks   []Chunk
	vectors  [][]float64
	vocabMap map[string]int
	vocab    []string
}

// NewVectorStore 创建空向量库
func NewVectorStore() *VectorStore {
	return &VectorStore{vocabMap: make(map[string]int)}
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
		if idx, ok := v.vocabMap[t]; ok {
			vec[idx]++
		}
	}
	return vec
}

// cosine 计算余弦相似度
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

// Index 对 Chunk 列表建立向量索引
func (v *VectorStore) Index(chunks []Chunk) {
	v.Chunks = chunks
	v.buildVocab(chunks)
	v.vectors = make([][]float64, len(chunks))
	for i, c := range chunks {
		v.vectors[i] = v.textToVector(c.Content)
	}
}

// SearchResult 单条检索结果
type SearchResult struct {
	Chunk      Chunk   `json:"chunk"`
	Similarity float64 `json:"similarity"`
}

// Search 检索与 query 最相似的 topK 条 Chunk（冒泡排序，适合小规模）
func (v *VectorStore) Search(query string, topK int) []SearchResult {
	qv := v.textToVector(query)
	results := make([]SearchResult, len(v.Chunks))
	for i, cv := range v.vectors {
		results[i] = SearchResult{Chunk: v.Chunks[i], Similarity: cosine(qv, cv)}
	}
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Similarity > results[i].Similarity {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if topK > len(results) {
		topK = len(results)
	}
	return results[:topK]
}

// ─────────────────────────────── RAG 引擎 ────────────────────────────────

// Engine 整合文本分割、向量检索与答案生成
type Engine struct {
	cfg        *config.APIConfig
	store      *VectorStore
	splitter   *TextSplitter
	Loaded     bool
	inf        *infra.Infrastructure
	generateFn func(systemPrompt string, userMsg string) string // LLM 回调，由 agent 注入
}

// NewEngine 创建 RAG 引擎
func NewEngine(cfg *config.APIConfig, inf *infra.Infrastructure) *Engine {
	return &Engine{
		cfg:      cfg,
		store:    NewVectorStore(),
		splitter: NewTextSplitter(cfg.ChunkSize, cfg.ChunkOverlap),
		inf:      inf,
	}
}

// SetGenerateFn 注入 LLM 调用回调，供 Query 合成答案
func (e *Engine) SetGenerateFn(fn func(systemPrompt string, userMsg string) string) {
	e.generateFn = fn
}

// Ingest 将文档切分并建立向量索引，返回切片数量
func (e *Engine) Ingest(doc string) int {
	chunks := e.splitter.Split(doc)
	e.store.Index(chunks)
	e.Loaded = true
	e.inf.PublishEvent("rag.ingest", fmt.Sprintf(`{"chunk_count":%d}`, len(chunks)))
	return len(chunks)
}

// Query 检索知识库并返回答案和检索结果
func (e *Engine) Query(question string) (string, []SearchResult) {
	if !e.Loaded {
		return "知识库为空，请先上传文档。", nil
	}
	results := e.store.Search(question, e.cfg.TopK)
	var parts []string
	for _, r := range results {
		if r.Similarity > 0.01 {
			parts = append(parts, r.Chunk.Content)
		}
	}
	context := strings.Join(parts, "\n\n")
	if context == "" {
		return "知识库中未找到相关内容。", results
	}
	if e.generateFn != nil {
		systemPrompt := "你是一个基于知识库回答问题的助手。请仅根据提供的上下文内容回答问题，不要编造信息。如果上下文不足以回答，请说明。"
		userMsg := fmt.Sprintf("上下文：\n%s\n\n问题：%s", context, question)
		answer := e.generateFn(systemPrompt, userMsg)
		return answer, results
	}
	// 无 LLM 时直接返回检索到的原文
	return fmt.Sprintf("【知识库检索结果】\n%s", context), results
}

// Chunks 返回当前已索引的所有切片（供状态接口使用）
func (e *Engine) Chunks() []Chunk {
	return e.store.Chunks
}
