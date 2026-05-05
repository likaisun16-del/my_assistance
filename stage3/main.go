package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"stage3/config"
	"strings"
	"time"
)

// ==================== Tool 定义 ====================

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
	return Tool{
		Name: "get_time", Description: "获取当前时间",
		Parameters: []ToolParam{{Name: "timezone", Type: "string", Description: "时区，如 Asia/Shanghai", Required: false}},
		Execute: func(params map[string]interface{}) (string, error) {
			loc := time.Local
			if v, ok := params["timezone"].(string); ok && v != "" {
				if l, err := time.LoadLocation(v); err == nil {
					loc = l
				}
			}
			return time.Now().In(loc).Format("2006-01-02 15:04:05 Monday"), nil
		},
	}
}

func getWeatherTool() Tool {
	return Tool{
		Name: "get_weather", Description: "获取指定城市的天气信息",
		Parameters: []ToolParam{{Name: "city", Type: "string", Description: "城市名称", Required: true}},
		Execute: func(params map[string]interface{}) (string, error) {
			city, _ := params["city"].(string)
			db := map[string]string{"北京": "晴天 22°C", "东京": "多云 18°C 湿度65%", "上海": "小雨 20°C", "纽约": "晴天 15°C"}
			if w, ok := db[city]; ok {
				return w, nil
			}
			return city + ": 晴天 20°C（模拟）", nil
		},
	}
}

func searchWebTool() Tool {
	return Tool{
		Name: "search_web", Description: "搜索互联网获取信息",
		Parameters: []ToolParam{{Name: "query", Type: "string", Description: "搜索关键词", Required: true}},
		Execute: func(params map[string]interface{}) (string, error) {
			q, _ := params["query"].(string)
			db := map[string]string{
				"AI应用工程师": "AI应用工程师是将AI技术落地到实际业务中的工程师，需具备ML基础、API开发、Prompt工程等能力。",
			}
			for k, v := range db {
				if strings.Contains(q, k) {
					return v, nil
				}
			}
			return fmt.Sprintf("关于「%s」的搜索结果（模拟）", q), nil
		},
	}
}

// ==================== Tool Agent ====================

type ToolCallResult struct {
	ToolName   string                 `json:"tool_name"`
	Params     map[string]interface{} `json:"params"`
	ToolResult string                 `json:"tool_result"`
}

type AgentResponse struct {
	Reply      string          `json:"reply"`
	ToolCall   *ToolCallResult `json:"tool_call,omitempty"`
	IsMock     bool            `json:"is_mock"`
}

type ToolAgent struct {
	cfg   *config.APIConfig
	tools map[string]Tool
}

func NewToolAgent(cfg *config.APIConfig, tools []Tool) *ToolAgent {
	m := make(map[string]Tool)
	for _, t := range tools {
		m[t.Name] = t
	}
	return &ToolAgent{cfg: cfg, tools: m}
}

func (a *ToolAgent) Run(query string) AgentResponse {
	// LLM 选择工具（模拟）
	toolCall := a.decideTool(query)
	if toolCall == nil {
		return AgentResponse{Reply: "我无法处理这个请求，请尝试询问时间、天气或搜索内容。", IsMock: !a.cfg.IsRealAPI()}
	}

	// 执行工具
	tool, ok := a.tools[toolCall.ToolName]
	if !ok {
		return AgentResponse{Reply: fmt.Sprintf("工具 %s 不存在", toolCall.ToolName), IsMock: true}
	}

	result, err := tool.Execute(toolCall.Params)
	if err != nil {
		return AgentResponse{Reply: fmt.Sprintf("工具执行失败: %v", err), IsMock: true}
	}

	reply := fmt.Sprintf("根据查询结果：%s", result)
	return AgentResponse{
		Reply:    reply,
		ToolCall: &ToolCallResult{ToolName: toolCall.ToolName, Params: toolCall.Params, ToolResult: result},
		IsMock:   !a.cfg.IsRealAPI(),
	}
}

func (a *ToolAgent) decideTool(query string) *ToolCallResult {
	q := strings.ToLower(query)
	if strings.Contains(q, "几点") || strings.Contains(q, "时间") {
		params := map[string]interface{}{}
		if strings.Contains(q, "东京") {
			params["timezone"] = "Asia/Tokyo"
		}
		return &ToolCallResult{ToolName: "get_time", Params: params}
	}
	if strings.Contains(q, "天气") {
		city := "北京"
		for _, c := range []string{"北京", "东京", "上海", "纽约"} {
			if strings.Contains(q, c) {
				city = c
				break
			}
		}
		return &ToolCallResult{ToolName: "get_weather", Params: map[string]interface{}{"city": city}}
	}
	if strings.Contains(q, "查") || strings.Contains(q, "搜索") || strings.Contains(q, "是什么") {
		return &ToolCallResult{ToolName: "search_web", Params: map[string]interface{}{"query": query}}
	}
	return nil
}

// ==================== HTTP ====================

var agent *ToolAgent

type ChatRequest struct {
	Message string `json:"message"`
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
	resp := agent.Run(req.Message)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleTools(w http.ResponseWriter, r *http.Request) {
	type toolInfo struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Parameters  []ToolParam `json:"parameters"`
	}
	var tools []toolInfo
	for _, t := range agent.tools {
		tools = append(tools, toolInfo{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tools)
}

func main() {
	cfg := config.DefaultConfig()
	tools := []Tool{getTimeTool(), getWeatherTool(), searchWebTool()}
	agent = NewToolAgent(cfg, tools)

	http.HandleFunc("/api/chat", handleChat)
	http.HandleFunc("/api/tools", handleTools)
	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + cfg.ServerPort
	fmt.Println("========================================")
	fmt.Println("  Stage 3: Tool Agent (工具调用)")
	fmt.Println("========================================")
	fmt.Printf("  服务地址: http://localhost%s\n", addr)
	fmt.Printf("  LLM模型: %s (调度器)\n", cfg.LLMModel)
	fmt.Printf("  可用工具: get_time, get_weather, search_web\n")
	fmt.Println("========================================")
	log.Fatal(http.ListenAndServe(addr, nil))
}
