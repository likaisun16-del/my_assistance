package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ScriptReq struct {
	Topic    string `json:"topic"`
	Duration int    `json:"duration"`
	Style    string `json:"style"`
	UserID   string `json:"user_id,omitempty"`
}

type ScriptResp struct {
	Hook             string   `json:"hook"`
	Body             []string `json:"body"`
	CTA              string   `json:"cta"`
	DurationEstimate int      `json:"duration_estimate"`
}

type Client struct {
	baseURL string
	hc      *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: baseURL, hc: &http.Client{Timeout: timeout}}
}

func (c *Client) GenerateScript(ctx context.Context, req *ScriptReq) (*ScriptResp, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/script/generate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned %d: %s", resp.StatusCode, raw)
	}
	var out ScriptResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode agent response: %w", err)
	}
	return &out, nil
}
