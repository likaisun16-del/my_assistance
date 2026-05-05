// Package handler 实现所有 HTTP API 的请求处理逻辑，并注册到 ServeMux。
package handler

import (
	"encoding/json"
	"final/config"
	"final/internal/agent"
	"final/internal/infra"
	"net/http"
	"unicode/utf8"
)

// Server 聚合 Agent 和基础设施引用，挂载所有 HTTP 路由
type Server struct {
	agent  *agent.UnifiedAgent
	infra  *infra.Infrastructure
	cfg    *config.APIConfig
}

// New 创建 Server 并注册所有路由到 mux
func New(a *agent.UnifiedAgent, inf *infra.Infrastructure, cfg *config.APIConfig) *Server {
	s := &Server{agent: a, infra: inf, cfg: cfg}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	http.HandleFunc("/api/chat",      s.chat)
	http.HandleFunc("/api/upload",    s.upload)
	http.HandleFunc("/api/memory",    s.memory)
	http.HandleFunc("/api/tools",     s.toolsList)
	http.HandleFunc("/api/snapshots", s.snapshots)
	http.HandleFunc("/api/status",    s.status)
}

// ─────────────────────────────── 路由处理 ────────────────────────────────

// POST /api/chat — 统一对话入口
func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	resp := s.agent.Process(req.Message)
	writeJSON(w, resp)
}

// POST /api/upload — 上传文档到 RAG 知识库
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	count := s.agent.RAG().Ingest(req.Content)
	writeJSON(w, map[string]interface{}{
		"chunk_count": count,
		"chunks":      s.agent.RAG().Chunks(),
	})
}

// GET /api/memory — 查看三层记忆状态
func (s *Server) memory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]interface{}{
		"short_term": s.agent.ShortTerm().Messages,
		"long_term":  s.agent.LongTerm().Items,
		"preference": s.agent.Preferences().Data,
	})
}

// GET /api/tools — 列出所有可用工具
func (s *Server) toolsList(w http.ResponseWriter, r *http.Request) {
	type toolInfo struct {
		Name string      `json:"name"`
		Desc string      `json:"description"`
	}
	var list []toolInfo
	for _, t := range s.agent.Tools() {
		list = append(list, toolInfo{Name: t.Name, Desc: t.Description})
	}
	writeJSON(w, list)
}

// GET /api/snapshots — 列出任务执行快照摘要
func (s *Server) snapshots(w http.ResponseWriter, r *http.Request) {
	snaps := s.agent.Snapshots()
	infos := make([]map[string]interface{}, 0, len(snaps))
	for i, snap := range snaps {
		infos = append(infos, map[string]interface{}{
			"index":     i,
			"timestamp": snap.Timestamp,
			"steps":     len(snap.State.Steps),
		})
	}
	writeJSON(w, infos)
}

// GET /api/status — 系统状态与配置摘要
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	// RAG chunk 预览（最多 60 字符）
	var chunkPreviews []map[string]interface{}
	for _, c := range s.agent.RAG().Chunks() {
		preview := c.Content
		if utf8.RuneCountInString(preview) > 60 {
			runes := []rune(preview)
			preview = string(runes[:60]) + "..."
		}
		chunkPreviews = append(chunkPreviews, map[string]interface{}{
			"id":      c.ID,
			"content": preview,
		})
	}
	writeJSON(w, map[string]interface{}{
		"rag_loaded":       s.agent.RAG().Loaded,
		"rag_chunks":       chunkPreviews,
		"short_term_count": len(s.agent.ShortTerm().Messages),
		"long_term_count":  len(s.agent.LongTerm().Items),
		"preferences":      s.agent.Preferences().Data,
		"tools_count":      len(s.agent.Tools()),
		"llm_model":        s.cfg.LLMModel,
		"embedding_model":  s.cfg.EmbeddingModel,
		"is_mock":          !s.cfg.IsRealLLM(),
		"infrastructure":   s.infra.Ready,
	})
}

// ─────────────────────────────── 工具函数 ────────────────────────────────

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
