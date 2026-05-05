package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"stage2/config"
	"strings"
	"unicode/utf8"
)

// ==================== 文档切分 ====================

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
	if step <= 0 {
		step = s.chunkSize
	}
	runes := []rune(text)
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

// ==================== 向量存储 ====================

type VectorStore struct {
	chunks   []Chunk
	vectors  [][]float64
	vocabMap map[string]int
}

func NewVectorStore() *VectorStore {
	return &VectorStore{vocabMap: make(map[string]int)}
}

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
	v.vocabMap = make(map[string]int)
	for _, chunk := range chunks {
		for _, token := range tokenize(chunk.Content) {
			if _, ok := v.vocabMap[token]; !ok {
				v.vocabMap[token] = len(v.vocabMap)
			}
		}
	}
}

func (v *VectorStore) textToVector(text string) []float64 {
	vec := make([]float64, len(v.vocabMap))
	for _, token := range tokenize(text) {
		if idx, ok := v.vocabMap[token]; ok {
			vec[idx]++
		}
	}
	return vec
}

func cosineSimilarity(a, b []float64) float64 {
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

func (v *VectorStore) Index(chunks []Chunk) {
	v.chunks = chunks
	v.buildVocab(chunks)
	v.vectors = make([][]float64, len(chunks))
	for i, chunk := range chunks {
		v.vectors[i] = v.textToVector(chunk.Content)
	}
}

type SearchResult struct {
	Chunk      Chunk   `json:"chunk"`
	Similarity float64 `json:"similarity"`
}

func (v *VectorStore) Search(query string, topK int) []SearchResult {
	queryVec := v.textToVector(query)
	results := make([]SearchResult, len(v.chunks))
	for i, chunkVec := range v.vectors {
		results[i] = SearchResult{Chunk: v.chunks[i], Similarity: cosineSimilarity(queryVec, chunkVec)}
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

// ==================== RAG Engine ====================

type RAGEngine struct {
	cfg      *config.APIConfig
	store    *VectorStore
	splitter *TextSplitter
	docLoaded bool
}

func NewRAGEngine(cfg *config.APIConfig) *RAGEngine {
	return &RAGEngine{
		cfg:      cfg,
		store:    NewVectorStore(),
		splitter: NewTextSplitter(cfg.ChunkSize, cfg.ChunkOverlap),
	}
}

func (e *RAGEngine) IngestDocument(doc string) int {
	chunks := e.splitter.Split(doc)
	e.store.Index(chunks)
	e.docLoaded = true
	return len(chunks)
}

func (e *RAGEngine) Query(question string) (string, []SearchResult) {
	results := e.store.Search(question, e.cfg.TopK)
	var contextParts []string
	for _, r := range results {
		if r.Similarity > 0.01 {
			contextParts = append(contextParts, r.Chunk.Content)
		}
	}
	context := strings.Join(contextParts, "\n\n")

	if e.cfg.IsRealLLM() {
		// TODO: 调用真实 LLM API
	}

	// 模拟回答
	answer := fmt.Sprintf("基于知识库回答：\n\n问题「%s」的相关文档片段已找到，结合上下文分析如下：\n%s",
		question, context)
	return answer, results
}

// ==================== HTTP Handlers ====================

var engine *RAGEngine

type UploadRequest struct {
	Content string `json:"content"`
}

type UploadResponse struct {
	ChunkCount int      `json:"chunk_count"`
	Chunks     []Chunk  `json:"chunks"`
}

type QueryRequest struct {
	Question string `json:"question"`
}

type QueryResponse struct {
	Answer   string         `json:"answer"`
	Results  []SearchResult `json:"results"`
	HasDoc   bool           `json:"has_doc"`
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	count := engine.IngestDocument(req.Content)
	chunks := engine.store.chunks

	resp := UploadResponse{ChunkCount: count, Chunks: chunks}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if !engine.docLoaded {
		resp := QueryResponse{HasDoc: false}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	answer, results := engine.Query(req.Question)

	// 截断过长的 chunk 内容用于前端展示
	for i := range results {
		if utf8.RuneCountInString(results[i].Chunk.Content) > 80 {
			runes := []rune(results[i].Chunk.Content)
			results[i].Chunk.Content = string(runes[:80]) + "..."
		}
	}

	resp := QueryResponse{Answer: answer, Results: results, HasDoc: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	cfg := config.DefaultConfig()
	engine = NewRAGEngine(cfg)

	http.HandleFunc("/api/upload", handleUpload)
	http.HandleFunc("/api/query", handleQuery)
	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + cfg.ServerPort
	fmt.Println("========================================")
	fmt.Println("  Stage 2: RAG 知识库助手")
	fmt.Println("========================================")
	fmt.Printf("  服务地址: http://localhost%s\n", addr)
	fmt.Printf("  聊天模型: %s\n", cfg.LLMModel)
	fmt.Printf("  向量模型: %s\n", cfg.EmbeddingModel)
	fmt.Printf("  切片大小: %d 字符\n", cfg.ChunkSize)
	fmt.Printf("  Top-K: %d\n", cfg.TopK)
	fmt.Println("========================================")

	log.Fatal(http.ListenAndServe(addr, nil))
}
