package engine

import (
	"os"
	"strings"
	"testing"
)

func TestTaskLib(t *testing.T) {
	e := newTestEngine(t)
	lib := e.Tasks()
	tk, err := lib.Save(Task{Title: "部署 / 发布", Cwd: "app", Body: "先跑测试\n再发布"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Name != "部署-发布" {
		t.Fatalf("name %q", tk.Name)
	}
	b, _ := os.ReadFile(lib.Dir() + "/部署-发布.md")
	if string(b) != "---\n名称: 部署 / 发布\n目录: app\n---\n\n先跑测试\n再发布\n" {
		t.Fatalf("file:\n%s", b)
	}
	if again, _ := lib.Save(Task{Title: "部署 / 发布", Body: "x"}); again.Name != "部署-发布-2" {
		t.Fatalf("unique name %q", again.Name)
	}
	if _, err := lib.Save(Task{Title: "", Body: "x"}); err == nil {
		t.Fatal("empty title accepted")
	}
	// Hand-written files: English keys, or no front matter at all.
	os.WriteFile(lib.Dir()+"/en.md", []byte("---\ntitle: Weekly\nserver: box2\ncwd: /srv/app\n---\nwrite it\n"), 0o644)
	os.WriteFile(lib.Dir()+"/plain.md", []byte("# 看日志\n\n看看最近的错误日志\n"), 0o644)
	if en, _ := lib.Get("en"); en.Title != "Weekly" || en.Node != "box2" || en.Cwd != "/srv/app" || en.Body != "write it" {
		t.Fatalf("english keys: %+v", en)
	}
	if p, _ := lib.Get("plain.md"); p.Title != "看日志" || !strings.HasPrefix(p.Body, "# 看日志") {
		t.Fatalf("no front matter: %+v", p)
	}
	if _, err := lib.Get("../etc/passwd"); err == nil {
		t.Fatal("path escape accepted")
	}
	if n := len(lib.List()); n != 4 {
		t.Fatalf("list: %d", n)
	}

	// Running a task: its text is the prompt, the note goes after it.
	s, _ := e.CreateSession("", "", "")
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Task: tk.Name})
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	if r.Task != tk.Name || r.TaskTitle != "部署 / 发布" || r.Title != "部署 / 发布" || r.Prompt != "先跑测试\n再发布" || r.Output != "DONE: 先跑测试" {
		t.Fatalf("task run: %+v", r)
	}
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Task: tk.Name, Text: "只发 staging"})
	r = waitDone(t, e, runIDOf(t, raw))
	if r.Title != "部署 / 发布：只发 staging" || r.Note != "只发 staging" || !strings.HasSuffix(r.Prompt, "\n\n---\n\n补充说明：只发 staging") {
		t.Fatalf("task + note: %q %q", r.Title, r.Prompt)
	}
	raw, err = e.Retry(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r2 := waitDone(t, e, runIDOf(t, raw)); r2.Task != tk.Name || r2.Note != "只发 staging" || r2.Prompt != r.Prompt {
		t.Fatalf("retry lost the task: %+v", r2)
	}
	if _, err := e.StartChat(ChatRequest{SessionID: s.ID, Task: "nope"}); err == nil {
		t.Fatal("unknown task accepted")
	}

	// A Markdown file in the code is a task too.
	os.WriteFile(s.Cwd+"/DEPLOY.md", []byte("# 发布流程\n\n1. 打包\n"), 0o644)
	raw, err = e.StartChat(ChatRequest{SessionID: s.ID, TaskFile: "DEPLOY.md"})
	if err != nil {
		t.Fatal(err)
	}
	r = waitDone(t, e, runIDOf(t, raw))
	if r.TaskFile != s.Cwd+"/DEPLOY.md" || r.TaskTitle != "发布流程" || r.Output != "DONE: # 发布流程" {
		t.Fatalf("task file: %+v", r)
	}
	if _, err := e.StartChat(ChatRequest{SessionID: s.ID, TaskFile: "missing.md"}); err == nil {
		t.Fatal("missing task file accepted")
	}

	if err := lib.Delete(tk.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Get(tk.Name); err == nil {
		t.Fatal("deleted task still there")
	}
}
