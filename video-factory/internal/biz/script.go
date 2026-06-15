package biz

import (
	"context"
	"encoding/json"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/pkg/ai"
)

type ProjectRepo interface {
	Create(ctx context.Context, userID int64, title string) (int64, error)
}

type TaskRepo interface {
	Create(ctx context.Context, projectID int64, taskType string, params any) (int64, error)
	UpdateResult(ctx context.Context, id int64, status string, result any, errMsg string) error
	GetResultJSON(ctx context.Context, id int64) (json.RawMessage, string, error)
}

type AgentClient interface {
	GenerateScript(ctx context.Context, req *ai.ScriptReq) (*ai.ScriptResp, error)
}

type CreateScriptInput struct {
	Title    string
	Topic    string
	Duration int
	Style    string
}

type CreateScriptOutput struct {
	TaskID    int64
	ProjectID int64
	Status    string
	Result    json.RawMessage
}

type ScriptUsecase struct {
	pr    ProjectRepo
	tr    TaskRepo
	agent AgentClient
}

func NewScriptUsecase(pr ProjectRepo, tr TaskRepo, agent AgentClient) *ScriptUsecase {
	return &ScriptUsecase{pr: pr, tr: tr, agent: agent}
}

func (uc *ScriptUsecase) Create(ctx context.Context, userID int64, in *CreateScriptInput) (*CreateScriptOutput, error) {
	pid, err := uc.pr.Create(ctx, userID, in.Title)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"topic":    in.Topic,
		"duration": in.Duration,
		"style":    in.Style,
	}
	tid, err := uc.tr.Create(ctx, pid, "script", params)
	if err != nil {
		return nil, err
	}

	resp, err := uc.agent.GenerateScript(ctx, &ai.ScriptReq{
		Topic:    in.Topic,
		Duration: in.Duration,
		Style:    in.Style,
	})
	if err != nil {
		_ = uc.tr.UpdateResult(ctx, tid, "failed", nil, err.Error())
		return nil, err
	}
	if err := uc.tr.UpdateResult(ctx, tid, "succeeded", resp, ""); err != nil {
		return nil, err
	}
	resultJSON, status, err := uc.tr.GetResultJSON(ctx, tid)
	if err != nil {
		return nil, err
	}
	return &CreateScriptOutput{
		TaskID:    tid,
		ProjectID: pid,
		Status:    status,
		Result:    resultJSON,
	}, nil
}
