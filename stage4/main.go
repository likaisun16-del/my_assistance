package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"stage4/config"
	"strings"
	"time"
)

// ==================== 工具 ====================

type Tool struct {
	Name        string
	Description string
	Execute     func(params map[string]string) (string, error)
}

func getTimeTool() Tool {
	return Tool{Name: "get_time", Description: "获取当前时间",
		Execute: func(p map[string]string) (string, error) {
			return time.Now().Format("2006-01-02 15:04:05"), nil
		}}
}

func getWeatherTool() Tool {
	return Tool{Name: "get_weather", Description: "获取天气",
		Execute: func(p map[string]string) (string, error) {
			city := p["city"]
			db := map[string]string{"东京": "多云 18°C 湿度65%", "北京": "晴天 22°C"}
			if w, ok := db[city]; ok { return w, nil }
			return city + ": 晴天 20°C（模拟）", nil
		}}
}

// ==================== ReAct 引擎 ====================

type StepType string

const (
	StepThought     StepType = "Thought"
	StepAction      StepType = "Action"
	StepObservation StepType = "Observation"
	StepFinalAnswer StepType = "Final Answer"
)

type ReActStep struct {
	Type    StepType          `json:"type"`
	Content string            `json:"content"`
	Tool    string            `json:"tool,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

type ReActResponse struct {
	Steps  []ReActStep `json:"steps"`
	Answer string      `json:"answer"`
	IsMock bool        `json:"is_mock"`
}

type ReActAgent struct {
	cfg   *config.APIConfig
	tools map[string]Tool
}

func NewReActAgent(cfg *config.APIConfig, tools []Tool) *ReActAgent {
	m := make(map[string]Tool)
	for _, t := range tools { m[t.Name] = t }
	return &ReActAgent{cfg: cfg, tools: m}
}

func (a *ReActAgent) Run(query string) ReActResponse {
	var steps []ReActStep
	var observations []string
	planned := a.planSteps(query)

	for i, ps := range planned {
		if i >= a.cfg.MaxIterations {
			steps = append(steps, ReActStep{Type: StepThought, Content: "已达到最大迭代次数，停止推理"})
			break
		}

		// Thought
		steps = append(steps, ReActStep{Type: StepThought, Content: ps.Thought})

		// Action
		if ps.Tool == "" {
			steps = append(steps, ReActStep{Type: StepFinalAnswer, Content: "直接回答"})
			break
		}

		tool, ok := a.tools[ps.Tool]
		if !ok {
			steps = append(steps, ReActStep{Type: StepAction, Content: fmt.Sprintf("工具 %s 不存在", ps.Tool), Tool: ps.Tool})
			continue
		}

		steps = append(steps, ReActStep{Type: StepAction, Content: fmt.Sprintf("调用 %s", ps.Tool), Tool: ps.Tool, Params: ps.Params})

		// Observation
		result, err := tool.Execute(ps.Params)
		if err != nil {
			steps = append(steps, ReActStep{Type: StepObservation, Content: fmt.Sprintf("错误: %v", err)})
		} else {
			steps = append(steps, ReActStep{Type: StepObservation, Content: result})
			observations = append(observations, result)
		}
	}

	// Final Answer
	answer := strings.Join(observations, "；")
	if len(observations) > 1 {
		answer = "综合查询结果：" + answer
	}
	steps = append(steps, ReActStep{Type: StepFinalAnswer, Content: answer})

	return ReActResponse{Steps: steps, Answer: answer, IsMock: !a.cfg.IsRealAPI()}
}

type plannedStep struct {
	Thought string
	Tool    string
	Params  map[string]string
}

func (a *ReActAgent) planSteps(query string) []plannedStep {
	q := strings.ToLower(query)
	var steps []plannedStep

	if strings.Contains(q, "时间") || strings.Contains(q, "几点") {
		steps = append(steps, plannedStep{Thought: "需要获取当前时间", Tool: "get_time", Params: map[string]string{}})
	}
	if strings.Contains(q, "天气") {
		city := "北京"
		for _, c := range []string{"东京", "北京", "上海"} {
			if strings.Contains(q, c) { city = c; break }
		}
		steps = append(steps, plannedStep{Thought: fmt.Sprintf("需要查询%s天气", city), Tool: "get_weather", Params: map[string]string{"city": city}})
	}
	if len(steps) == 0 {
		steps = append(steps, plannedStep{Thought: "无需工具，直接回答", Tool: ""})
	}
	return steps
}

// ==================== HTTP ====================

var reactAgent *ReActAgent

type ChatRequest struct {
	Message string `json:"message"`
}

func handleReact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	resp := reactAgent.Run(req.Message)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	cfg := config.DefaultConfig()
	tools := []Tool{getTimeTool(), getWeatherTool()}
	reactAgent = NewReActAgent(cfg, tools)

	http.HandleFunc("/api/react", handleReact)
	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + cfg.ServerPort
	fmt.Println("========================================")
	fmt.Println("  Stage 4: ReAct AI助手 (多步推理)")
	fmt.Println("========================================")
	fmt.Printf("  服务地址: http://localhost%s\n", addr)
	fmt.Printf("  LLM模型: %s (推理引擎)\n", cfg.LLMModel)
	fmt.Printf("  最大迭代: %d 次\n", cfg.MaxIterations)
	fmt.Println("========================================")
	log.Fatal(http.ListenAndServe(addr, nil))
}
