package repo

import (
	"context"
	"os"
	"testing"

	"github.com/AGI-Core/AGI-saber/video-factory/internal/data"
)

func TestTaskCreateAndUpdateResult(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set TEST_PG_DSN to run")
	}
	db, err := data.NewPG(dsn)
	if err != nil {
		t.Fatal(err)
	}

	pr := NewProjectRepo(db)
	pid, err := pr.Create(context.Background(), 1, "ut-project")
	if err != nil {
		t.Fatal(err)
	}

	tr := NewTaskRepo(db)
	tid, err := tr.Create(context.Background(), pid, "script", map[string]any{"topic": "hi", "duration": 60})
	if err != nil {
		t.Fatal(err)
	}

	if err := tr.UpdateResult(context.Background(), tid, "succeeded",
		map[string]any{"hook": "ok", "body": []string{"a", "b"}}, ""); err != nil {
		t.Fatal(err)
	}

	got, err := tr.Get(context.Background(), tid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "succeeded" {
		t.Fatalf("status=%s", got.Status)
	}
	if len(got.Result) == 0 {
		t.Fatal("result empty")
	}
}
