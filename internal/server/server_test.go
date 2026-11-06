package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"agenthost/internal/engine"
)

// The task library API accepts exactly what the web UI sends.
func TestTaskLibAPI(t *testing.T) {
	dir := t.TempDir()
	lib, _ := engine.NewLibrary(dir+"/wf", nil)
	eng, err := engine.New(engine.Config{DataDir: dir + "/data", Workspace: dir + "/ws", CodexBin: "/bin/false"}, lib) // never a real Codex
	if err != nil {
		t.Fatal(err)
	}
	h := New(eng, "tok", nil, map[string]any{})
	do := func(method, path, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer tok")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	// Including a blank updatedAt, as an unsaved form might.
	code, m := do("POST", "/api/tasklib", `{"name":"","title":"写周报","node":"","cwd":"","body":"看提交写周报","updatedAt":""}`)
	if code != http.StatusOK || m["title"] != "写周报" || m["name"] != "写周报" {
		t.Fatalf("create: %d %v", code, m)
	}
	code, m = do("PUT", "/api/tasklib/"+"%E5%86%99%E5%91%A8%E6%8A%A5", `{"title":"写周报","cwd":"reports","body":"新内容"}`)
	if code != http.StatusOK || m["cwd"] != "reports" || m["name"] != "写周报" {
		t.Fatalf("update: %d %v", code, m)
	}
	if code, _ := do("PUT", "/api/tasklib/missing", `{"title":"x","body":"y"}`); code != http.StatusNotFound {
		t.Fatalf("update missing: %d", code)
	}
	if code, m := do("POST", "/api/tasklib", `{"title":"","body":"y"}`); code != http.StatusBadRequest {
		t.Fatalf("empty title: %d %v", code, m)
	}
	if code, m := do("GET", "/api/tasklib/写周报", ""); code != 200 || m["body"] != "新内容" {
		t.Fatalf("get: %d %v", code, m)
	}
	// Starting a saved task opens a session where the task says.
	code, m = do("POST", "/api/tasks", `{"task":"写周报"}`)
	if code != http.StatusCreated || m["session"].(map[string]any)["cwd"] != dir+"/ws/reports" || m["run"].(map[string]any)["taskTitle"] != "写周报" {
		t.Fatalf("start task: %d %v", code, m)
	}
	// Skills in a directory.
	os.MkdirAll(dir+"/ws/reports/skills/fmt", 0o755)
	os.WriteFile(dir+"/ws/reports/skills/fmt/SKILL.md", []byte("---\nname: fmt\ndescription: 排版\n---\n"), 0o644)
	if code, m := do("GET", "/api/skills?path=reports", ""); code != 200 || len(m["skills"].([]any)) != 1 {
		t.Fatalf("skills: %d %v", code, m)
	}
	if code, _ := do("DELETE", "/api/tasklib/写周报", ""); code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
}
