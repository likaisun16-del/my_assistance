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
	"bytes"
	"encoding/json"
	"final/config"
	"final/internal/infra"
	"final/internal/llm"
	"final/internal/memory"
	"final/internal/rag"
	"final/internal/tools"
	"fmt"
	"log"
	"net/http"
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
	llmClient := llm.New(cfg)
	ragEngine := rag.NewEngine(cfg, inf)
	a := &UnifiedAgent{
		cfg:   cfg,
		llm:   llmClient,
		rag:   ragEngine,
		tools: tools.DefaultTools(),
		stm:   memory.NewShortTerm(cfg.ShortTermMaxTurns),
		ltm:   memory.NewLongTerm(),
		pref:  memory.NewPreference(),
		inf:   inf,
	}
	// 注入 RAG 的 LLM 合成回调
	a.rag.SetGenerateFn(func(systemPrompt, userMsg string) string {
		return a.llm.Chat(systemPrompt, []llm.Message{{Role: "user", Content: userMsg}})
	})
	// 将 RAG 注册为可选工具（私人黑洞知识库检索）
	a.tools["rag_search"] = tools.Tool{
		Name:        "rag_search",
		Description: "从私人黑洞（个人知识库）中检索相关文档内容",
		Parameters: []tools.Param{
			{Name: "query", Type: "string", Description: "检索关键词或问题", Required: true},
		},
		Execute: func(params map[string]interface{}) (string, error) {
			q, _ := params["query"].(string)
			if q == "" {
				q = "相关内容"
			}
			if !a.rag.Loaded {
				return "", fmt.Errorf("知识库为空，请先在「私人黑洞」上传文档")
			}
			answer, _ := a.rag.Query(q)
			return answer, nil
		},
	}
	// 用 LLM 知识 + 可选 Tavily API 替换默认的 mock search_web
	a.tools["search_web"] = tools.Tool{
		Name:        "search_web",
		Description: "搜索互联网获取最新信息",
		Parameters: []tools.Param{
			{Name: "query", Type: "string", Description: "搜索关键词", Required: true},
		},
		Execute: func(params map[string]interface{}) (string, error) {
			q, _ := params["query"].(string)
			if q == "" {
				return "", fmt.Errorf("搜索关键词不能为空")
			}
			// 优先尝试 Tavily 真实搜索
			if a.cfg.SearchAPIKey != "" {
				if result, err := tavilySearch(q, a.cfg.SearchAPIKey, a.cfg.SearchAPIURL); err == nil {
					return result, nil
				}
			}
			// 降级：用 LLM 知识库回答
			return a.llm.Chat(
				"你是一个知识丰富的搜索引擎助手。请基于你的知识，对用户的搜索问题给出准确、详细的回答。直接给出答案，不要说「我不知道」或「我无法搜索」。",
				[]llm.Message{{Role: "user", Content: "搜索：" + q}},
			), nil
		},
	}
	// 从 PostgreSQL 恢复跨会话记忆
	a.restoreFromDB()
	return a
}

// RegisterTool 动态注册一个工具（支持 MCP 工具热插入）
func (a *UnifiedAgent) RegisterTool(t tools.Tool) {
	a.tools[t.Name] = t
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

// ChatOptions 控制本次对话的路由行为
type ChatOptions struct {
	UseRAG        bool     // 是否使用 RAG 知识库
	SelectedTools []string // 用户明确选中的工具列表；nil = 自动路由，[] = 禁用工具
	Explicit      bool     // true 时以 SelectedTools/UseRAG 为准，false 时自动路由
}

// Process 是统一入口（自动路由，向后兼容）
func (a *UnifiedAgent) Process(query string) *Response {
	return a.process(query, ChatOptions{Explicit: false})
}

// ProcessWithOptions 带显式选项的入口，供前端精确控制路由
func (a *UnifiedAgent) ProcessWithOptions(query string, opts ChatOptions) *Response {
	return a.process(query, opts)
}

func (a *UnifiedAgent) process(query string, opts ChatOptions) *Response {
	resp := &Response{Query: query, Mode: "chat"}

	// Stage 5：更新短期记忆
	a.stm.Add("user", query)

	// 偏好提取：优先 LLM，降级规则
	go func() {
		kvs := a.llm.ExtractPreferences(query)
		if len(kvs) > 0 {
			a.pref.SaveBatch(kvs)
			for k, v := range kvs {
				a.inf.SavePreference("default", k, v)
				content := fmt.Sprintf("用户%s: %s", k, v)
				emb, _ := a.llm.Embed(content)
				embJSON, _ := json.Marshal(emb)
				a.inf.SaveLongTermItem(content, 0.8, embJSON)
				a.ltm.Store(content, 0.8, emb)
			}
		}
	}()

	// 同步规则提取（用于立即展示 ExtractedInfo）
	if key, value, ok := a.pref.ExtractAndSave(query); ok {
		resp.ExtractedInfo = fmt.Sprintf("已记住：%s = %s", key, value)
	}

	// 构造多轮历史消息（含 STM 上下文）
	histMsgs := a.buildHistoryMessages(query)

	if opts.Explicit {
		switch {
		case len(opts.SelectedTools) > 0:
			filtered := a.filterTools(opts.SelectedTools)
			if a.needReActFromTools(query, filtered) {
				resp.Mode = "react"
				answer, steps, task := a.runReActWithTools(query, filtered)
				resp.Answer, resp.Steps, resp.Task = answer, steps, task
			} else {
				resp.Mode = "tool"
				answer, tc := a.runToolFromSet(query, filtered)
				resp.Answer, resp.ToolCall = answer, tc
			}
		case opts.UseRAG && a.rag.Loaded:
			resp.Mode = "rag"
			answer, results := a.rag.Query(query)
			resp.Answer, resp.SearchResults = answer, results
		default:
			memCtx := a.buildMemoryContext(query)
			if memCtx != "" {
				resp.Mode = "memory"
				resp.Answer = a.generateWithMemory(query, memCtx, histMsgs)
			} else {
				resp.Answer = a.llm.Chat("你是一个简洁的AI助手", histMsgs)
			}
		}
	} else {
		switch {
		case a.needReAct(query):
			resp.Mode = "react"
			answer, steps, task := a.runReActWithTools(query, a.tools)
			resp.Answer, resp.Steps, resp.Task = answer, steps, task
		case a.needTool(query):
			resp.Mode = "tool"
			answer, tc := a.runToolFromSet(query, a.tools)
			resp.Answer, resp.ToolCall = answer, tc
		case a.needRAG(query):
			resp.Mode = "rag"
			answer, results := a.rag.Query(query)
			resp.Answer, resp.SearchResults = answer, results
		default:
			memCtx := a.buildMemoryContext(query)
			if memCtx != "" {
				resp.Mode = "memory"
				resp.Answer = a.generateWithMemory(query, memCtx, histMsgs)
			} else {
				resp.Answer = a.llm.Chat("你是一个简洁的AI助手", histMsgs)
			}
		}
	}

	a.stm.Add("assistant", resp.Answer)

	eventData, _ := json.Marshal(map[string]interface{}{"query": query, "mode": resp.Mode})
	a.inf.PublishEvent("agent.chat", string(eventData))

	resp.ShortTermCount = len(a.stm.Messages)
	resp.LongTermCount = len(a.ltm.Items)
	resp.Preferences = a.pref.Data
	return resp
}

// buildHistoryMessages 将 STM 历史消息转为 LLM 消息列表（末尾附上当前 user query）
func (a *UnifiedAgent) buildHistoryMessages(query string) []llm.Message {
	var msgs []llm.Message
	// STM 最后一条是刚加入的 user query，跳过重复
	for _, m := range a.stm.Messages {
		if m.Role == "user" || m.Role == "assistant" {
			msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
		}
	}
	// 如果最后一条不是当前 query（初次调用时 STM 已包含），则附上
	if len(msgs) == 0 || msgs[len(msgs)-1].Content != query {
		msgs = append(msgs, llm.Message{Role: "user", Content: query})
	}
	return msgs
}

// filterTools 按名称列表过滤可用工具集
func (a *UnifiedAgent) filterTools(names []string) map[string]tools.Tool {
	result := make(map[string]tools.Tool)
	for _, name := range names {
		if t, ok := a.tools[name]; ok {
			result[name] = t
		}
	}
	return result
}

// needReActFromTools — 只要工具集非空就走 ReAct，保证每次工具调用都有完整推理轨迹
func (a *UnifiedAgent) needReActFromTools(query string, ts map[string]tools.Tool) bool {
	return len(ts) > 0
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

// tavilySearch 调用 Tavily Search API，返回格式化的搜索结果摘要
func tavilySearch(query, apiKey, apiURL string) (string, error) {
	if apiURL == "" {
		apiURL = "https://api.tavily.com/search"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"api_key":      apiKey,
		"query":        query,
		"search_depth": "basic",
		"max_results":  5,
	})
	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(body)) //nolint
	if err != nil {
		return "", fmt.Errorf("Tavily 请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Tavily 返回错误状态: %d", resp.StatusCode)
	}
	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("解析 Tavily 响应失败: %w", err)
	}
	// 优先返回 Tavily 合成的 answer
	if result.Answer != "" {
		var sb strings.Builder
		sb.WriteString(result.Answer)
		if len(result.Results) > 0 {
			sb.WriteString("\n\n**来源：**\n")
			for i, r := range result.Results {
				if i >= 3 {
					break
				}
				sb.WriteString(fmt.Sprintf("- [%s](%s)\n", r.Title, r.URL))
			}
		}
		return sb.String(), nil
	}
	// 无 answer 时拼接 top 结果摘要
	if len(result.Results) == 0 {
		return "", fmt.Errorf("Tavily 返回空结果")
	}
	var sb strings.Builder
	for i, r := range result.Results {
		if i >= 3 {
			break
		}
		sb.WriteString(fmt.Sprintf("**%s**\n%s\n%s\n\n", r.Title, r.Content, r.URL))
	}
	return strings.TrimSpace(sb.String()), nil
}

// ─────────────────────────────── Stage 3：Tool Agent ─────────────────────

func (a *UnifiedAgent) runToolFromSet(query string, ts map[string]tools.Tool) (string, *tools.CallResult) {
	tc := tools.Decide(query, ts)
	if tc == nil {
		return "我无法处理这个请求。", nil
	}
	tool, ok := ts[tc.ToolName]
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

func (a *UnifiedAgent) runReActWithTools(query string, ts map[string]tools.Tool) (string, []ReActStep, *TaskState) {
	var reactSteps []ReActStep
	var observations []string

	// ── Step 1: Planner LLM 决定调哪些工具及参数 ──────────────────────────
	planItems := a.llmPlanSteps(query, ts)

	// 若 Planner 决定不需要任何工具，直接走 LLM 对话
	if len(planItems) == 0 {
		answer := a.llm.Chat("你是一个简洁的AI助手", a.buildHistoryMessages(query))
		reactSteps = append(reactSteps, ReActStep{Type: StepThought, Content: "分析后无需调用工具，直接回答"})
		reactSteps = append(reactSteps, ReActStep{Type: StepFinalAnswer, Content: answer})
		return answer, reactSteps, nil
	}

	// 将 planItems 转换为 TaskStep 列表
	var taskSteps []TaskStep
	for i, pi := range planItems {
		taskSteps = append(taskSteps, TaskStep{
			ID: i + 1, Name: pi.Reason, ToolName: pi.Tool,
			Params: pi.Params, Status: StepPending,
		})
	}

	a.task = &TaskState{
		TaskID: fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Query:  query, Status: "running", Steps: taskSteps,
	}
	a.snapshots = nil
	a.saveSnapshot()

	// ── Step 2: 按 Planner 计划逐步执行工具 ───────────────────────────────
	for i := range a.task.Steps {
		ts2 := &a.task.Steps[i]
		a.task.CurrentStep = i
		ts2.Status = StepRunning

		// Thought：展示 Planner 给出的调用理由
		reactSteps = append(reactSteps, ReActStep{
			Type:    StepThought,
			Content: ts2.Name, // Name 即 Planner 生成的 reason
		})
		reactSteps = append(reactSteps, ReActStep{
			Type:    StepAction,
			Content: fmt.Sprintf("调用 %s", ts2.ToolName),
			Tool:    ts2.ToolName,
			Params:  ts2.Params,
		})

		tool, ok := ts[ts2.ToolName]
		if !ok {
			ts2.Status = StepFailed
			ts2.Error = fmt.Sprintf("工具 %s 不在允许列表中", ts2.ToolName)
			reactSteps = append(reactSteps, ReActStep{Type: StepObservation, Content: ts2.Error})
			a.saveSnapshot()
			continue
		}
		if a.executeStepWithRetryTool(ts2, tool) {
			ts2.Status = StepDone
			reactSteps = append(reactSteps, ReActStep{Type: StepObservation, Content: ts2.Result})
			observations = append(observations, fmt.Sprintf("[%s] %s", ts2.ToolName, ts2.Result))
		} else {
			ts2.Status = StepFailed
			reactSteps = append(reactSteps, ReActStep{Type: StepObservation, Content: fmt.Sprintf("执行失败: %s", ts2.Error)})
		}
		a.saveSnapshot()
	}

	// ── Step 3: Generator LLM 综合所有观察结果生成最终答案 ────────────────
	answer := a.llmGenerate(query, observations)
	reactSteps = append(reactSteps, ReActStep{Type: StepFinalAnswer, Content: answer})
	a.task.Result = answer
	a.task.Status = "completed"
	return answer, reactSteps, a.task
}

// ─────────────────────────── Planner LLM ─────────────────────────────────

// planItem 是 Planner LLM 输出的单个工具调用计划
type planItem struct {
	Tool   string            `json:"tool"`
	Params map[string]string `json:"params"`
	Reason string            `json:"reason"`
}

// llmPlanSteps 调用 Planner LLM，从允许的工具集中智能选择需要调用的工具及参数。
// 若 LLM 不可用或解析失败，降级为关键词规则。
func (a *UnifiedAgent) llmPlanSteps(query string, ts map[string]tools.Tool) []planItem {
	if !a.cfg.IsRealLLM() {
		return a.rulePlanItems(query, ts)
	}

	// 构造工具描述
	var toolLines []string
	for name, t := range ts {
		var pDescs []string
		for _, p := range t.Parameters {
			req := ""
			if p.Required {
				req = "（必填）"
			}
			pDescs = append(pDescs, fmt.Sprintf("%s(%s)%s", p.Name, p.Type, req))
		}
		params := strings.Join(pDescs, ", ")
		if params == "" {
			params = "无参数"
		}
		toolLines = append(toolLines, fmt.Sprintf("- %s: %s [参数: %s]", name, t.Description, params))
	}

	planPrompt := fmt.Sprintf(`你是一个任务规划器。根据用户问题，从可用工具中选出真正需要调用的工具（不要为了用工具而用工具，按需选择）。

用户问题：%s

可用工具：
%s

请以 JSON 数组格式输出执行计划，格式如下：
[{"tool":"工具名","params":{"参数名":"参数值"},"reason":"一句话说明为什么调用这个工具"}]

如果无需工具直接回答，输出 []。只输出 JSON，不要其他内容。`,
		query, strings.Join(toolLines, "\n"))

	raw := a.llm.Chat("你是一个精准的任务规划器，只在必要时才调用工具，不做无意义的调用。",
		[]llm.Message{{Role: "user", Content: planPrompt}})

	// 清洗 LLM 输出（可能包含 markdown 代码块）
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var items []planItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		log.Printf("⚠️  Planner LLM 解析失败 (%v)，降级到规则规划。原始输出: %s", err, raw)
		return a.rulePlanItems(query, ts)
	}

	// 过滤：只保留工具集中实际存在的工具
	var valid []planItem
	for _, item := range items {
		if _, ok := ts[item.Tool]; ok {
			if item.Params == nil {
				item.Params = map[string]string{}
			}
			valid = append(valid, item)
		}
	}
	return valid
}

// rulePlanItems 关键词规则降级规划（无真实 LLM 时使用）
func (a *UnifiedAgent) rulePlanItems(query string, ts map[string]tools.Tool) []planItem {
	q := strings.ToLower(query)
	var items []planItem

	if _, ok := ts["get_time"]; ok {
		if strings.Contains(q, "时间") || strings.Contains(q, "几点") || strings.Contains(q, "现在") {
			params := map[string]string{}
			if strings.Contains(q, "东京") {
				params["timezone"] = "Asia/Tokyo"
			}
			items = append(items, planItem{Tool: "get_time", Params: params, Reason: "查询当前时间"})
		}
	}
	if _, ok := ts["get_weather"]; ok {
		if strings.Contains(q, "天气") {
			city := "北京"
			for _, c := range []string{"东京", "北京", "上海", "广州", "深圳", "纽约", "伦敦"} {
				if strings.Contains(q, c) {
					city = c
					break
				}
			}
			items = append(items, planItem{Tool: "get_weather", Params: map[string]string{"city": city}, Reason: "查询" + city + "天气"})
		}
	}
	if _, ok := ts["search_web"]; ok {
		if strings.Contains(q, "搜索") || strings.Contains(q, "查询") || strings.Contains(q, "介绍") ||
			strings.Contains(q, "是什么") || strings.Contains(q, "怎么") || strings.Contains(q, "如何") {
			items = append(items, planItem{Tool: "search_web", Params: map[string]string{"query": query}, Reason: "搜索相关信息"})
		}
	}
	if _, ok := ts["rag_search"]; ok {
		items = append(items, planItem{Tool: "rag_search", Params: map[string]string{"query": query}, Reason: "检索个人知识库"})
	}
	// MCP / 自定义工具
	builtins := map[string]bool{"get_time": true, "get_weather": true, "search_web": true, "rag_search": true}
	for name, t := range ts {
		if builtins[name] {
			continue
		}
		params := a.extractParamsForTool(query, t)
		items = append(items, planItem{Tool: name, Params: params, Reason: "调用工具 " + name})
	}
	return items
}

// ─────────────────────────── Generator LLM ───────────────────────────────

// llmGenerate 调用 Generator LLM，将多个工具观察结果合成为自然语言最终答案
func (a *UnifiedAgent) llmGenerate(query string, observations []string) string {
	if len(observations) == 0 {
		return a.llm.Chat("你是一个简洁的AI助手", a.buildHistoryMessages(query))
	}
	if !a.cfg.IsRealLLM() {
		return "综合查询结果：" + strings.Join(observations, "；")
	}

	var obsBuilder strings.Builder
	for i, obs := range observations {
		obsBuilder.WriteString(fmt.Sprintf("%d. %s\n", i+1, obs))
	}

	genPrompt := fmt.Sprintf(`请根据以下工具执行结果，综合回答用户的问题。回答要自然流畅、重点突出，不要机械罗列原始数据，也不要重复问题本身。

用户问题：%s

工具执行结果：
%s`, query, obsBuilder.String())

	return a.llm.Chat("你是一个善于综合信息的AI助手，能将多个工具的执行结果整合成清晰自然的回答。",
		[]llm.Message{{Role: "user", Content: genPrompt}})
}

// extractParamsForTool 用 LLM 从 query 中提取工具所需参数；无法调用时用 query 填充首个必填参数
func (a *UnifiedAgent) extractParamsForTool(query string, t tools.Tool) map[string]string {
	result := make(map[string]string)
	if len(t.Parameters) == 0 {
		return result
	}
	if !a.cfg.IsRealLLM() {
		for _, p := range t.Parameters {
			if p.Required {
				result[p.Name] = query
				break
			}
		}
		return result
	}
	var lines []string
	for _, p := range t.Parameters {
		req := ""
		if p.Required {
			req = "（必填）"
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)%s: %s", p.Name, p.Type, req, p.Description))
	}
	prompt := fmt.Sprintf(
		"从下面的用户消息中提取工具「%s」所需的参数，以JSON对象格式输出，只输出JSON，不加任何说明。\n\n参数说明：\n%s\n\n用户消息：%s",
		t.Name, strings.Join(lines, "\n"), query,
	)
	raw := a.llm.Chat("", []llm.Message{{Role: "user", Content: prompt}})
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		// LLM 输出无法解析时兜底：用 query 填充首个必填参数
		for _, p := range t.Parameters {
			if p.Required {
				result[p.Name] = query
				break
			}
		}
	}
	return result
}

// ─────────────────────────────── Stage 6：Harness ────────────────────────

// executeStepWithRetryTool 带重试的步骤执行，使用传入的具体工具实例
func (a *UnifiedAgent) executeStepWithRetryTool(step *TaskStep, tool tools.Tool) bool {
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
	queryEmb, _ := a.llm.Embed(query)
	ltmItems := a.ltm.Recall(query, a.cfg.LongTermTopK, queryEmb)
	if len(ltmItems) > 0 {
		var items []string
		for _, item := range ltmItems {
			items = append(items, item.Content)
		}
		parts = append(parts, "【长期记忆】\n"+strings.Join(items, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// generateWithMemory 结合记忆上下文和多轮历史生成个性化回复
func (a *UnifiedAgent) generateWithMemory(query, memContext string, histMsgs []llm.Message) string {
	systemPrompt := "你是一个记住用户偏好的个人助手。以下是你掌握的用户信息：\n" + memContext
	return a.llm.Chat(systemPrompt, histMsgs)
}

// restoreFromDB 启动时从 PostgreSQL 恢复跨会话偏好和长期记忆
func (a *UnifiedAgent) restoreFromDB() {
	// 恢复偏好
	prefs := a.inf.LoadPreferences("default")
	a.pref.SaveBatch(prefs)

	// 恢复长期记忆
	rows := a.inf.LoadLongTermItems()
	for _, row := range rows {
		a.ltm.StoreItem(memory.Item{
			ID:         row.ID,
			Content:    row.Content,
			Importance: row.Importance,
			Embedding:  row.Embedding,
		})
	}
	if len(prefs) > 0 || len(rows) > 0 {
		log.Printf("✅ 记忆恢复：%d 条偏好，%d 条长期记忆", len(prefs), len(rows))
	}
}
