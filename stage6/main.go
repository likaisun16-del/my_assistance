package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"stage6/config"
	"strings"
	"time"
)

// ==================== 状态管理 ====================

type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
)

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
)

type Step struct {
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
	TaskID      string      `json:"task_id"`
	Query       string      `json:"query"`
	Status      TaskStatus  `json:"status"`
	Steps       []Step      `json:"steps"`
	CurrentStep int         `json:"current_step"`
	Result      string      `json:"result,omitempty"`
	CreatedAt   string      `json:"created_at"`
}

type Snapshot struct {
	State     TaskState `json:"state"`
	Timestamp string    `json:"timestamp"`
}

// ==================== 工具 ====================

type Tool struct {
	Name        string
	Description string
	Execute     func(params map[string]string) (string, error)
}

var weatherCallCount int

func getTimeTool() Tool {
	return Tool{Name: "get_time", Description: "获取当前时间",
		Execute: func(p map[string]string) (string, error) {
			return time.Now().Format("2006-01-02 15:04:05"), nil
		}}
}

func getWeatherTool() Tool {
	return Tool{Name: "get_weather", Description: "获取天气（模拟不稳定）",
		Execute: func(p map[string]string) (string, error) {
			weatherCallCount++
			if weatherCallCount == 1 {
				return "", fmt.Errorf("网络超时：天气API响应超时")
			}
			city := p["city"]
			if city == "" { city = "北京" }
			db := map[string]string{"东京": "多云 18°C", "北京": "晴天 22°C"}
			if w, ok := db[city]; ok { return w, nil }
			return city + ": 晴天 20°C（模拟）", nil
		}}
}

// ==================== Harness Agent ====================

type HarnessAgent struct {
	cfg       *config.APIConfig
	tools     map[string]Tool
	task      *TaskState
	snapshots []Snapshot
}

func NewHarnessAgent(cfg *config.APIConfig, tools []Tool) *HarnessAgent {
	m := make(map[string]Tool)
	for _, t := range tools { m[t.Name] = t }
	return &HarnessAgent{cfg: cfg, tools: m}
}

func (a *HarnessAgent) saveSnapshot() {
	snap := Snapshot{
		State:     *a.task,
		Timestamp: time.Now().Format("15:04:05"),
	}
	// 深拷贝
	data, _ := json.Marshal(a.task)
	json.Unmarshal(data, &snap.State)
	a.snapshots = append(a.snapshots, snap)
}

func (a *HarnessAgent) Run(query string) *TaskState {
	weatherCallCount = 0 // 重置

	steps := a.planSteps(query)
	now := time.Now().Format(time.RFC3339)
	a.task = &TaskState{
		TaskID:    fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Query:     query,
		Status:    StatusRunning,
		Steps:     steps,
		CreatedAt: now,
	}
	a.snapshots = nil

	a.saveSnapshot()

	for i := range a.task.Steps {
		a.task.CurrentStep = i
		step := &a.task.Steps[i]
		step.Status = StepRunning

		ok := a.executeStepWithRetry(step)
		if ok {
			step.Status = StepDone
		} else {
			step.Status = StepFailed
		}
		a.saveSnapshot()
	}

	// 汇总
	var results []string
	for _, s := range a.task.Steps {
		if s.Status == StepDone { results = append(results, s.Result) }
	}
	a.task.Result = strings.Join(results, "；")
	a.task.Status = StatusCompleted
	return a.task
}

func (a *HarnessAgent) executeStepWithRetry(step *Step) bool {
	tool, ok := a.tools[step.ToolName]
	if !ok {
		if step.ToolName == "" {
			step.Result = "通用回答"
			return true
		}
		step.Error = fmt.Sprintf("工具 %s 不存在", step.ToolName)
		return false
	}

	for attempt := 0; attempt < a.cfg.MaxRetries; attempt++ {
		resultCh := make(chan struct {
			result string
			err    error
		}, 1)
		go func() {
			r, e := tool.Execute(step.Params)
			resultCh <- struct {
				result string
				err    error
			}{r, e}
		}()

		select {
		case res := <-resultCh:
			if res.err == nil {
				step.Result = res.result
				return true
			}
			step.RetryCount = attempt + 1
			step.Error = res.err.Error()
			time.Sleep(time.Duration(a.cfg.RetryDelayMs) * time.Millisecond)
		case <-time.After(time.Duration(a.cfg.StepTimeoutMs) * time.Millisecond):
			step.RetryCount = attempt + 1
			step.Error = "执行超时"
			time.Sleep(time.Duration(a.cfg.RetryDelayMs) * time.Millisecond)
		}
	}
	return false
}

func (a *HarnessAgent) planSteps(query string) []Step {
	q := strings.ToLower(query)
	var steps []Step
	id := 1
	if strings.Contains(q, "时间") || strings.Contains(q, "几点") {
		steps = append(steps, Step{ID: id, Name: "查询时间", ToolName: "get_time", Params: map[string]string{}, Status: StepPending})
		id++
	}
	if strings.Contains(q, "天气") {
		city := "北京"
		for _, c := range []string{"东京", "北京", "上海"} {
			if strings.Contains(q, c) { city = c; break }
		}
		steps = append(steps, Step{ID: id, Name: fmt.Sprintf("查询%s天气", city), ToolName: "get_weather", Params: map[string]string{"city": city}, Status: StepPending})
		id++
	}
	if len(steps) == 0 {
		steps = append(steps, Step{ID: id, Name: "通用回答", ToolName: "", Params: map[string]string{}, Status: StepPending})
	}
	return steps
}

// ==================== HTTP ====================

var agent *HarnessAgent
var cfg *config.APIConfig

type TaskRequest struct {
	Message string `json:"message"`
}

type SnapshotInfo struct {
	Index     int    `json:"index"`
	Timestamp string `json:"timestamp"`
	StepCount int    `json:"step_count"`
	LastStep  string `json:"last_step"`
}

func handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	state := agent.Run(req.Message)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

func handleSnapshots(w http.ResponseWriter, r *http.Request) {
	var infos []SnapshotInfo
	for i, snap := range agent.snapshots {
		lastStep := ""
		if len(snap.State.Steps) > 0 {
			s := snap.State.Steps[len(snap.State.Steps)-1]
			lastStep = fmt.Sprintf("%s [%s]", s.Name, s.Status)
		}
		infos = append(infos, SnapshotInfo{
			Index: i, Timestamp: snap.Timestamp,
			StepCount: len(snap.State.Steps), LastStep: lastStep,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(infos)
}

func handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Index int `json:"index"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if req.Index < 0 || req.Index >= len(agent.snapshots) {
		http.Error(w, "Invalid snapshot index", http.StatusBadRequest)
		return
	}
	// 恢复快照并继续执行
	snap := agent.snapshots[req.Index]
	agent.task = &snap.State
	agent.task.Status = StatusRunning
	weatherCallCount = 10 // 重置避免再次模拟失败

	// 从中断处继续
	for i := agent.task.CurrentStep; i < len(agent.task.Steps); i++ {
		agent.task.CurrentStep = i
		step := &agent.task.Steps[i]
		if step.Status == StepDone { continue }
		step.Status = StepRunning
		ok := agent.executeStepWithRetry(step)
		if ok {
			step.Status = StepDone
		} else {
			step.Status = StepFailed
		}
		agent.saveSnapshot()
	}

	var results []string
	for _, s := range agent.task.Steps {
		if s.Status == StepDone { results = append(results, s.Result) }
	}
	agent.task.Result = strings.Join(results, "；")
	agent.task.Status = StatusCompleted

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agent.task)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"max_retries":      cfg.MaxRetries,
		"retry_delay_ms":   cfg.RetryDelayMs,
		"step_timeout_ms":  cfg.StepTimeoutMs,
		"llm_model":        cfg.LLMModel,
		"embedding_model":  cfg.EmbeddingModel,
	})
}

func main() {
	cfg = config.DefaultConfig()
	tools := []Tool{getTimeTool(), getWeatherTool()}
	agent = NewHarnessAgent(cfg, tools)

	http.HandleFunc("/api/task", handleTask)
	http.HandleFunc("/api/snapshots", handleSnapshots)
	http.HandleFunc("/api/restore", handleRestore)
	http.HandleFunc("/api/config", handleConfig)
	fs := http.FileServer(http.Dir("frontend"))
	http.Handle("/", fs)

	addr := ":" + cfg.ServerPort
	fmt.Println("========================================")
	fmt.Println("  Stage 6: 稳定AI助手 (Harness)")
	fmt.Println("========================================")
	fmt.Printf("  服务地址: http://localhost%s\n", addr)
	fmt.Printf("  聊天模型: %s\n", cfg.LLMModel)
	fmt.Printf("  向量模型: %s\n", cfg.EmbeddingModel)
	fmt.Printf("  最大重试: %d 次\n", cfg.MaxRetries)
	fmt.Printf("  单步超时: %d ms\n", cfg.StepTimeoutMs)
	fmt.Println("========================================")
	log.Fatal(http.ListenAndServe(addr, nil))
}
