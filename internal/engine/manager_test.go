package engine

import (
	"strings"
	"testing"
)

// While a task runs, messages go to the manager: it reports progress and
// passes instructions to the working Codex without disturbing it otherwise.
func TestManager(t *testing.T) {
	e := newTestEngine(t, nil)
	s, _ := e.CreateSession("", "", "")
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW STEERABLE 长任务"})
	if err != nil {
		t.Fatal(err)
	}
	worker := runIDOf(t, raw)
	waitFor(t, "worker turn", func() bool { return e.snapshot(worker).Steps[0].TurnID != "" })

	raw, err = e.StartChat(ChatRequest{SessionID: s.ID, Text: "进度怎么样"})
	if err != nil {
		t.Fatal(err)
	}
	m := waitDone(t, e, runIDOf(t, raw))
	if m.Kind != KindManager || m.Target != worker || m.Sandbox != "read-only" || m.Output != "MGR 进度怎么样 → 任务执行中 | inPrompt=true" {
		t.Fatalf("manager: kind=%s target=%s out=%q", m.Kind, m.Target, m.Output)
	}
	if st := e.snapshot(worker).Status; st != StatusRunning {
		t.Fatalf("a question must not disturb the task: %s", st)
	}
	e.mu.Lock()
	info := e.sessionInfo(e.sessions[s.ID])
	e.mu.Unlock()
	if info.LastRunID != worker || info.Status != StatusRunning {
		t.Fatalf("the session should show its task, not the manager: %+v", info)
	}
	if _, err := e.StartChat(ChatRequest{SessionID: s.ID, Task: "x"}); err == nil {
		t.Fatal("a second task while one runs must be refused")
	}

	// Instructions reach the running turn.
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "转告：只跑 auth 模块"})
	if m = waitDone(t, e, runIDOf(t, raw)); !strings.HasPrefix(m.Output, "MGR send ok=true") {
		t.Fatalf("steer via manager: %q", m.Output)
	}
	w := waitDone(t, e, worker)
	if w.Status != StatusCompleted || w.Output != "STEERED: 只跑 auth 模块" {
		t.Fatalf("worker after steer: %s %q", w.Status, w.Output)
	}
	noted := false
	for _, it := range w.Steps[0].Items {
		noted = noted || (it.Title == "管家转告" && it.Text == "只跑 auth 模块")
	}
	if !noted {
		t.Fatal("the task timeline should show what was passed on")
	}

	// Afterwards messages continue the task; the manager has its own thread.
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "谢谢"})
	if r := waitDone(t, e, runIDOf(t, raw)); r.Kind != KindChat || r.Output != "DONE: 谢谢" {
		t.Fatalf("follow-up: %s %q", r.Kind, r.Output)
	}
	e.mu.Lock()
	mt, wt := e.sessions[s.ID].ManagerThreadID, e.sessions[s.ID].ThreadID
	e.mu.Unlock()
	if mt == "" || wt == "" || mt == wt {
		t.Fatalf("threads: manager %q task %q", mt, wt)
	}

	// The manager can pause the task.
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW 第二个任务"})
	worker2 := runIDOf(t, raw)
	waitFor(t, "worker2 turn", func() bool { return e.snapshot(worker2).Steps[0].TurnID != "" })
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "暂停一下"})
	if m = waitDone(t, e, runIDOf(t, raw)); m.Output != "MGR pause ok=true" {
		t.Fatalf("pause via manager: %q", m.Output)
	}
	waitFor(t, "paused", func() bool { return e.snapshot(worker2).Status == StatusPaused })
	if err := e.Steer(worker2, "x"); err == nil || !strings.Contains(err.Error(), "暂停") {
		t.Fatalf("steering a paused task: %v", err)
	}
	e.Stop(worker2)
	waitDone(t, e, worker2)
	if _, err := e.Retry(m.ID); err == nil {
		t.Fatal("manager replies are not re-run")
	}
}
