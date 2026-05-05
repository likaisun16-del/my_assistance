// Package llm 封装与 LLM API 的交互逻辑。
// 若未配置 API Key 则自动回退到规则驱动的 Mock 实现，保证无需真实接口也能运行。
package llm

import (
	"final/config"
	"fmt"
	"strings"
)

// Message 表示单条对话消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Client 是 LLM 聊天客户端
type Client struct {
	cfg *config.APIConfig
}

// New 创建 LLM 客户端
func New(cfg *config.APIConfig) *Client {
	return &Client{cfg: cfg}
}

// Chat 发送对话请求，返回回复文本。
// 若配置了真实 API Key 则调用远程接口，否则使用 Mock。
func (c *Client) Chat(systemPrompt string, messages []Message) string {
	if c.cfg.IsRealLLM() {
		// TODO: 调用真实 LLM API（ERNIE / OpenAI 兼容接口）
		_ = systemPrompt
	}
	return c.mock(messages)
}

// mock 基于关键词规则模拟 LLM 回复，用于演示和测试
func (c *Client) mock(messages []Message) string {
	var userQuery string
	for _, m := range messages {
		if m.Role == "user" {
			userQuery = m.Content
		}
	}
	q := strings.ToLower(userQuery)
	switch {
	case strings.Contains(q, "你是谁"):
		return "我是一个全能 AI 助手，具备知识库、工具调用、推理、记忆和稳定执行能力。"
	case strings.Contains(q, "后端工程师"):
		return "后端工程师负责服务器端逻辑开发：API 设计、数据库、业务逻辑、系统架构、性能优化。常用 Go / Java / Python / MySQL / Redis。"
	default:
		return fmt.Sprintf("收到：「%s」——这是模拟 LLM 回复，接入真实 API 后会更智能。", userQuery)
	}
}
