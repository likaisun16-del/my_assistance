package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"stage1/config"
	"strings"
	"time"
)

// ==================== LLM Client ====================

// Message 聊天消息
type Message struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"`
}

// LLMClient 大模型客户端
type LLMClient struct {
	cfg *config.APIConfig
}

func NewLLMClient(cfg *config.APIConfig) *LLMClient {
	return &LLMClient{cfg: cfg}
}

// Chat 调用 LLM 进行对话
func (c *LLMClient) Chat(systemPrompt string, messages []Message) string {
	if c.cfg.IsRealAPI() {
		// TODO: 调用真实 LLM API
		// 使用 c.cfg.LLMAPIUrl, c.cfg.LLMAPIKey, c.cfg.LLMModel
		return c.callRealAPI(systemPrompt, messages)
	}
	return c.mockChat(systemPrompt, messages)
}

// callRealAPI 调用真实 LLM API（待实现）
func (c *LLMClient) callRealAPI(systemPrompt string, messages []Message) string {
	// 真实场景中，这里使用 HTTP POST 调用 LLM API
	// 示例：
	//   body := map[string]interface{}{
	//       "model": c.cfg.LLMModel,
	//       "messages": append([]Message{{Role: "system", Content: systemPrompt}}, messages...),
	//       "temperature": c.cfg.Temperature,
	//   }
	//   resp, err := http.Post(c.cfg.LLMAPIUrl+"?access_token="+c.cfg.LLMAPIKey, ...)
	return c.mockChat(systemPrompt, messages)
}

// mockChat 模拟 LLM 回复
func (c *LLMClient) mockChat(systemPrompt string, messages []Message) string {
	var userQuery string
	for _, m := range messages {
		if m.Role == "user" {
			userQuery = m.Content
		}
	}

	q := strings.ToLower(userQuery)

	if strings.Contains(q, "你是谁") || strings.Contains(q, "who are you") {
		if strings.Contains(systemPrompt, "简洁") {
			return "我是一个AI助手，致力于用简短清晰的方式回答你的问题。"
		}
		return "我是一个AI助手，很高兴为你服务！"
	}

	if strings.Contains(q, "后端工程师") || strings.Contains(q, "backend") {
		return "后端工程师是负责服务器端逻辑开发的工程师，主要工作包括：\n1. 设计和开发API接口\n2. 数据库设计与优化\n3. 业务逻辑实现\n4. 系统架构设计\n5. 性能优化与问题排查\n\n常用技术栈：Go、Java、Python、MySQL、Redis等。"
	}

	if strings.Contains(q, "天气") {
		return "抱歉，我目前无法获取实时天气信息。"
	}

	if strings.Contains(q, "时间") || strings.Contains(q, "几点") {
		return fmt.Sprintf("现在是 %s。", time.Now().Format("2006-01-02 15:04:05"))
	}

	if c.cfg.Temperature > 0.7 && rand.Float64() > 0.5 {
		return fmt.Sprintf("关于「%s」，这是一个有趣的话题。接入真实LLM后，我可以用更智能的方式回答你！", userQuery)
	}

	return fmt.Sprintf("收到你的问题：「%s」。这是模拟LLM的回复，接入真实API后即可获得智能回答。", userQuery)
}

// ==================== HTTP Handlers ====================

// ChatRequest 聊天请求
type ChatRequest struct {
	Message string `json:"message"`
}

// ChatResponse 聊天响应
type ChatResponse struct {
	Reply   string `json:"reply"`
	Model   string `json:"model"`
	IsMock  bool   `json:"is_mock"`
}

var (
	llmClient    *LLMClient
	systemPrompt = "你是一个简洁的AI助手，请用简短清晰的方式回答问题"
)

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

	messages := []Message{{Role: "user", Content: req.Message}}
	reply := llmClient.Chat(systemPrompt, messages)

	resp := ChatResponse{
		Reply:  reply,
		Model:  llmClient.cfg.LLMModel,
		IsMock: !llmClient.cfg.IsRealAPI(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ==================== Main ====================

func main() {
	cfg := config.DefaultConfig()
	llmClient = NewLLMClient(cfg)

	// API 路由
	http.HandleFunc("/api/chat", handleChat)

	// 前端静态文件
	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + cfg.ServerPort
	fmt.Println("========================================")
	fmt.Println("  Stage 1: 基础聊天助手 (LLM Wrapper)")
	fmt.Println("========================================")
	fmt.Printf("  服务地址: http://localhost%s\n", addr)
	fmt.Printf("  LLM模型: %s\n", cfg.LLMModel)
	fmt.Printf("  模式: %s\n", map[bool]string{true: "真实API", false: "模拟LLM"}[cfg.IsRealAPI()])
	fmt.Println("========================================")

	log.Fatal(http.ListenAndServe(addr, nil))
}
