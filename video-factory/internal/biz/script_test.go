package biz

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/pkg/ai"
)

type fakeProjectRepo struct{ next int64 }

func (f *fakeProjectRepo) Create(_ context.Context, userID int64, title string) (int64, error) {
	f.next++
	return f.next, nil
}

type fakeTask struct {
	id        int64
	projectID int64
	status    string
	result    json.RawMessage
	errMsg    string
}

type fakeTaskRepo struct {
	tasks map[int64]*fakeTask
	next  int64
}

func newFakeTaskRepo() *fakeTaskRepo { return &fakeTaskRepo{tasks: map[int64]*fakeTask{}} }

func (f *fakeTaskRepo) Create(_ context.Context, projectID int64, _ string, _ any) (int64, error) {
	f.next++
	f.tasks[f.next] = &fakeTask{id: f.next, projectID: projectID, status: "running"}
	return f.next, nil
}
func (f *fakeTaskRepo) UpdateResult(_ context.Context, id int64, status string, result any, errMsg string) error {
	t := f.tasks[id]
	t.status = status
	t.errMsg = errMsg
	if result != nil {
		b, _ := json.Marshal(result)
		t.result = b
	}
	return nil
}
func (f *fakeTaskRepo) GetResultJSON(_ context.Context, id int64) (json.RawMessage, string, error) {
	t := f.tasks[id]
	return t.result, t.status, nil
}

type fakeAgent struct {
	called bool
	err    error
}

func (f *fakeAgent) GenerateScript(_ context.Context, r *ai.ScriptReq) (*ai.ScriptResp, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return &ai.ScriptResp{Hook: "h", Body: []string{"b1"}, CTA: "c", DurationEstimate: r.Duration}, nil
}

func TestCreateScriptTask_RunsSyncAndStoresResult(t *testing.T) {
	pr := &fakeProjectRepo{}
	tr := newFakeTaskRepo()
	agent := &fakeAgent{}
	uc := NewScriptUsecase(pr, tr, agent)

	out, err := uc.Create(context.Background(), 42, &CreateScriptInput{
		Title: "demo", Topic: "RAG", Duration: 60, Style: "口播",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !agent.called {
		t.Fatal("agent not called")
	}
	if out.Status != "succeeded" {
		t.Fatalf("status=%s", out.Status)
	}
	if len(out.Result) == 0 {
		t.Fatal("result empty")
	}
}

func TestCreateScriptTask_AgentFailureMarksFailed(t *testing.T) {
	pr := &fakeProjectRepo{}
	tr := newFakeTaskRepo()
	agent := &fakeAgent{err: errors.New("boom")}
	uc := NewScriptUsecase(pr, tr, agent)

	_, err := uc.Create(context.Background(), 1, &CreateScriptInput{Title: "x", Topic: "y", Duration: 60})
	if err == nil {
		t.Fatal("expect error")
	}
	if tr.tasks[1].status != "failed" {
		t.Fatalf("status=%s", tr.tasks[1].status)
	}
	if tr.tasks[1].errMsg != "boom" {
		t.Fatalf("errMsg=%s", tr.tasks[1].errMsg)
	}
}
