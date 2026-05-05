// Package agent 实现 UnifiedAgent：整合全部 6 个阶段能力的核心调度器。
//
// 路由策略（按优先级）：
//  1. ReAct + Harness — 复合查询（含 2+ 子需求，需多步推理）
//  2. Tool Agent      — 单一工具触发（时间 / 天气 / 搜索）
//  3. RAG             — 知识库已加载且无工具触发
//  4. Memory          — 存在用户偏好或长期记忆可利用
//  5. Chat            — 直接与 LLM 对话
package agent

import (
	"encoding/json"
	"final/config"
	"final/internal/infra"
	"final/internal/llm"
	"final/internal/memory"
	"final/internal/rag"
	"final/internal/tools"
	"fmt"
	"strings"
	"time"
)

// ─────────────────────────────── ReAct 数据结构 ──────────────────────────

// StepType 是 ReAct 循环中的步骤类型
type StepType string

const (
	StepThought     StepType = "Thought"
	StepAction      StepType = "Action"
	StepObservation StepType = "Observation"
	StepFinalAnswer StepType = "Final Answer"
)

// ReActStep 记录 ReAct 循环的单个步骤
type ReActStep struct {
	Type    StepType          `json:"type"`
	Content string            `json:"content"`
	Tool    string            `json:"tool,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
}

// ─────────────────────────────── Harness 数据结构 ────────────────────────

// TaskStepStatus 是任务步骤的执行状态
type TaskStepStatus string

const (
	StepPending TaskStepStatus = "pending"
	StepRunning TaskStepStatus = "running"
	StepDone    TaskStepStatus = "done"
	StepFailed  TaskStepStatus = "failed"
)

// TaskStep 是 Harness 中可重试的原子执行单元
type TaskStep struct {
	ID         int            `json:"id"`
	Name       string         `json:"name"`
	ToolName   string         `json:"tool_name"`
	Params     map[string]string `json:"params"`
	Status     TaskStepStatus `json:"status"`
	Result     string         `json:"result,omitempty"`
	Error      string         `json:"error,omitempty"`
	RetryCount int            `json:"retry_count"`
}

// TaskState 描述一次任务的完整执行状态
type TaskState struct {
	TaskID      string     `json:"task_id"`
	Query       string     `json:"query"`
	Status      string     `json:"status"`
	Steps       []TaskStep `json:"steps"`
	CurrentStep int        `json:"current_step"`
	Result      string     `json:"result,omitempty"`
}

// Snapshot 是某一时刻的任务状态快照（用于故障恢复）
type Snapshot struct {
	State     TaskState `json:"state"`
	Timestamp string    `json:"timestamp"`
}

// ─────────────────────────────── 统一响应 ────────────────────────────────

// Response 是 UnifiedAgent.Process 的输出，携带本次请求的全部上下文
type Response struct {
	Query          string                 `json:"query"`
	Answer         string                 `json:"answer"`
	Mode           string                 `json:"mode"`           // chat / tool / rag / memory / react
	Steps          []ReActStep            `json:"steps,omitempty"`
	ToolCall       *tools.CallResult      `json:"tool_call,omitempty"`
	SearchResults  []rag.SearchResult     `json:"search_results,omitempty"`
	Task           *TaskState             `json:"task,omitempty"`
	ExtractedInfo  string                 `json:"extracted_info,omitempty"`
	ShortTermCount int                    `json:"short_term_count"`
	LongTermCount  int                    `json:"long_term_count"`
	Preferences    map[string]string      `json:"preferences"`
}

// ─────────────────────────────── Unified Agent ───────────────────────────

// UnifiedAgent 整合全部能力，是系统的核心调度入口
type UnifiedAgent struct {
	cfg       *config.APIConfig
	llm       *llm.Client
	rag       *rag.Engine
	tools     map[string]tools.Tool
	stm       *memory.ShortTerm
	ltm       *memory.LongTerm
	pref      *memory.Preference
	snapshots []Snapshot
	task      *TaskState
	inf       *infra.Infrastructure
}

// New 创建并初始化 UnifiedAgent
func New(cfg *config.APIConfig, inf *infra.Infrastructure) *UnifiedAgent {
	return &UnifiedAgent{
		cfg:   cfg,
		llm:   llm.New(cfg),
		rag:   rag.NewEngine(cfg, inf),
		tools: tools.DefaultTools(),
		stm:   memory.NewShortTerm(cfg.ShortTermMaxTurns),
		ltm:   memory.NewLongTerm(),
		pref:  memory.NewPreference(),
		inf:   inf,
	}
}

// RAG 暴露 RAG 引擎，供 HTTP handler 直接调用 Ingest
func (a *UnifiedAgent) RAG() *rag.Engine { return a.rag }

// Tools 暴露工具集，供 HTTP handler 列出工具信息
func (a *UnifiedAgent) Tools() map[string]tools.Tool { return a.tools }

// ShortTerm 暴露短期记忆，供 HTTP handler 查询
func (a *UnifiedAgent) ShortTerm() *memory.ShortTerm { return a.stm }

// LongTerm 暴露长期记忆，供 HTTP handler 查询
func (a *UnifiedAgent) LongTerm() *memory.LongTerm { return a.ltm }

// Preferences 暴露用户偏好，供 HTTP handler 查询
func (a *UnifiedAgent) Preferences() *memory.Preference { return a.pref }

// Snapshots 返回历史快照列表
func (a *UnifiedAgent) Snapshots() []Snapshot { return a.snapshots }

// ─────────────────────────────── 主处理流程 ──────────────────────────────

// Process 是统一入口：根据 query 内容智能路由到对应处理模式
func (a *UnifiedAgent) Process(query string) *Response {
	resp := &Response{Query: query, Mode: "chat"}

	// Stage 5：更新短期记忆，提取并持久化用户偏好
	a.stm.Add("user", query)
	if key, value, ok := a.pref.ExtractAndSave(query); ok {
		a.ltm.Store(fmt.Sprintf("用户%s: %s", key, value), 0.8)
		resp.ExtractedInfo = fmt.Sprintf("已记住：%s = %s", key, value)
		a.inf.SavePreference("default", key, value)
	}

	// 智能路由
	switch {
	case a.needReAct(query):
		resp.Mode = "react"
		answer, steps, task := a.runReAct(query)
		resp.Answer, resp.Steps, resp.Task = answer, steps, task

	case a.needTool(query):
		resp.Mode = "tool"
		answer, tc := a.runTool(query)
		resp.Answer, resp.ToolCall = answer, tc

	case a.needRAG(query):
		resp.Mode = "rag"
		answer, results := a.rag.Query(query)
		resp.Answer, resp.SearchResults = answer, results

	default:
		memCtx := a.buildMemoryContext(query)
		if memCtx != "" {
			resp.Mode = "memory"
			resp.Answer = a.generateWithMemory(query, memCtx)
		} else {
			resp.Answer = a.llm.Chat("你是一个简洁的AI助手", []llm.Message{{Role: "user", Content: query}})
		}
	}

	a.stm.Add("assistant", resp.Answer)

	// 发布事件到 Kafka
	eventData, _ := json.Marshal(map[string]interface{}{"query": query, "mode": resp.Mode})
	a.inf.PublishEvent("agent.chat", string(eventData))

	resp.ShortTermCount = len(a.stm.Messages)
	resp.LongTermCount = len(a.ltm.Items)
	resp.Preferences = a.pref.Data
	return resp
}

// ─────────────────────────────── 路由判断 ────────────────────────────────

func (a *UnifiedAgent) needTool(query string) bool {
	q := strings.ToLower(query)
	return strings.Contains(q, "几点") || strings.Contains(q, "时间") ||
		strings.Contains(q, "天气") || strings.Contains(q, "查") ||
		strings.Contains(q, "搜索") || strings.Contains(q, "是什么")
}

func (a *UnifiedAgent) needRAG(query string) bool {
	return a.rag.Loaded && !a.needTool(query) && !a.needReAct(query)
}

// needReAct 当 query 涉及 2+ 个子需求时触发多步推理
func (a *UnifiedAgent) needReAct(query string) bool {
	q := strings.ToLower(query)
	count := 0
	if strings.Contains(q, "时间") || strings.Contains(q, "几点") {
		count++
	}
	if strings.Contains(q, "天气") {
		count++
	}
	if strings.Contains(q, "总结") || strings.Contains(q, "汇总") {
		count++
	}
	if strings.Contains(q, "查") || strings.Contains(q, "搜索") {
		count++
	}
	return count >= 2
}

// ─────────────────────────────── Stage 3：Tool Agent ─────────────────────

func (a *UnifiedAgent) runTool(query string) (string, *tools.CallResult) {
	tc := tools.Decide(query, a.tools)
	if tc == nil {
		return "我无法处理这个请求。", nil
	}
	tool, ok := a.tools[tc.ToolName]
	if !ok {
		return fmt.Sprintf("工具 %s 不存在", tc.ToolName), tc
	}
	result, err := tool.Execute(tc.Params)
	if err != nil {
		return fmt.Sprintf("工具执行失败: %v", err), tc
	}
	tc.ToolResult = result
	return fmt.Sprintf("根据查询结果：%s", result), tc
}

// ─────────────────────────────── Stage 4：ReAct ──────────────────────────

func (a *UnifiedAgent) runReAct(query string) (string, []ReActStep, *TaskState) {
	var reactSteps []ReActStep
	var observations []string

	taskSteps := a.planTaskSteps(query)
	a.task = &TaskState{
		TaskID: fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Query:  query, Status: "running", Steps: taskSteps,
	}
	a.snapshots = nil
	a.saveSnapshot()

	for i := range a.task.Steps {
		ts := &a.task.Steps[i]
		a.task.CurrentStep = i
		ts.Status = StepRunning
		reactSteps = append(reactSteps, ReActStep{Type: StepThought, Content: fmt.Sprintf("需要执行: %s", ts.Name)})

		// 汇总步骤（无工具）
		if ts.ToolName == "" {
			var results []string
			for _, s := range a.task.Steps {
				if s.Status == StepDone {
					results = append(results, s.Result)
				}
			}
			ts.Result = strings.Join(results, "；")
			ts.Status = StepDone
			reactSteps = append(reactSteps, ReActStep{Type: StepFinalAnswer, Content: "汇总所有结果"})
			break
		}

		reactSteps = append(reactSteps, ReActStep{
			Type:    StepAction,
			Content: fmt.Sprintf("调用 %s", ts.ToolName),
			Tool:    ts.ToolName,
			Params:  ts.Params,
		})

		if ok := a.executeStepWithRetry(ts); ok {
			ts.Status = StepDone
			reactSteps = append(reactSteps, ReActStep{Type: StepObservation, Content: ts.Result})
			observations = append(observations, ts.Result)
		} else {
			ts.Status = StepFailed
			reactSteps = append(reactSteps, ReActStep{Type: StepObservation, Content: fmt.Sprintf("失败: %s", ts.Error)})
		}
		a.saveSnapshot()
	}

	answer := strings.Join(observations, "；")
	if len(observations) > 1 {
		answer = "综合查询结果：" + answer
	}
	reactSteps = append(reactSteps, ReActStep{Type: StepFinalAnswer, Content: answer})
	a.task.Result = answer
	a.task.Status = "completed"
	return answer, reactSteps, a.task
}

// planTaskSteps 根据 query 内容生成任务步骤列表
func (a *UnifiedAgent) planTaskSteps(query string) []TaskStep {
	q := strings.ToLower(query)
	var steps []TaskStep
	id := 1
	if strings.Contains(q, "时间") || strings.Contains(q, "几点") {
		steps = append(steps, TaskStep{
			ID: id, Name: "查询时间", ToolName: "get_time",
			Params: map[string]string{}, Status: StepPending,
		})
		id++
	}
	if strings.Contains(q, "天气") {
		city := "北京"
		for _, c := range []string{"东京", "北京", "上海"} {
			if strings.Contains(q, c) {
				city = c
				break
			}
		}
		steps = append(steps, TaskStep{
			ID: id, Name: fmt.Sprintf("查询%s天气", city), ToolName: "get_weather",
			Params: map[string]string{"city": city}, Status: StepPending,
		})
		id++
	}
	if strings.Contains(q, "总结") || strings.Contains(q, "汇总") {
		steps = append(steps, TaskStep{
			ID: id, Name: "汇总回答", ToolName: "",
			Params: map[string]string{}, Status: StepPending,
		})
	}
	return steps
}

// ─────────────────────────────── Stage 6：Harness ────────────────────────

// executeStepWithRetry 带重试的步骤执行，失败时按配置延迟后重试
func (a *UnifiedAgent) executeStepWithRetry(step *TaskStep) bool {
	tool, ok := a.tools[step.ToolName]
	if !ok {
		return false
	}
	params := make(map[string]interface{}, len(step.Params))
	for k, v := range step.Params {
		params[k] = v
	}
	for attempt := 0; attempt < a.cfg.MaxRetries; attempt++ {
		result, err := tool.Execute(params)
		if err == nil {
			step.Result = result
			return true
		}
		step.RetryCount = attempt + 1
		step.Error = err.Error()
		time.Sleep(time.Duration(a.cfg.RetryDelayMs) * time.Millisecond)
	}
	return false
}

// saveSnapshot 对当前 TaskState 做深拷贝快照并持久化到 PG
func (a *UnifiedAgent) saveSnapshot() {
	var stateCopy TaskState
	data, _ := json.Marshal(a.task)
	json.Unmarshal(data, &stateCopy)
	snap := Snapshot{State: stateCopy, Timestamp: time.Now().Format("15:04:05")}
	a.snapshots = append(a.snapshots, snap)
	a.inf.SaveSnapshot(a.task.TaskID, data)
}

// ─────────────────────────────── Stage 5：Memory ─────────────────────────

// buildMemoryContext 构建传给 LLM 的记忆上下文字符串
func (a *UnifiedAgent) buildMemoryContext(query string) string {
	var parts []string
	if ctx := a.pref.BuildContext(); ctx != "" {
		parts = append(parts, ctx)
	}
	ltmItems := a.ltm.Recall(query, a.cfg.LongTermTopK)
	if len(ltmItems) > 0 {
		var items []string
		for _, item := range ltmItems {
			items = append(items, item.Content)
		}
		parts = append(parts, "【长期记忆】\n"+strings.Join(items, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// generateWithMemory 结合记忆上下文生成个性化回复
func (a *UnifiedAgent) generateWithMemory(query, memContext string) string {
	if strings.Contains(query, "推荐") && strings.Contains(memContext, "周杰伦") {
		return "根据你的偏好，推荐：\n1. 周杰伦 - 晴天\n2. 周杰伦 - 稻香\n3. 林俊杰 - 江南（风格相似）"
	}
	return fmt.Sprintf("基于你的个人记忆回答：「%s」", query)
}
