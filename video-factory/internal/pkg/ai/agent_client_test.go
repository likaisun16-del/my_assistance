package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGenerateScript_HappyPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/script/generate" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		var got ScriptReq
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Topic != "x" || got.Duration != 60 {
			t.Fatalf("bad request: %+v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hook":              "钩子",
			"body":              []string{"a", "b"},
			"cta":               "关注",
			"duration_estimate": 60,
		})
	}))
	defer ts.Close()

	c := NewClient(ts.URL, 5*time.Second)
	out, err := c.GenerateScript(context.Background(), &ScriptReq{Topic: "x", Duration: 60, Style: "口播"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Hook != "钩子" || len(out.Body) != 2 || out.DurationEstimate != 60 {
		t.Fatalf("bad output: %+v", out)
	}
}

func TestGenerateScript_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL, 5*time.Second)
	if _, err := c.GenerateScript(context.Background(), &ScriptReq{Topic: "x"}); err == nil {
		t.Fatal("expect error")
	}
}
