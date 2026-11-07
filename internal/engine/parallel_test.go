package engine

import (
	"os"
	"strings"
	"testing"
)

func TestParallel(t *testing.T) {
	e := newTestEngine(t)
	tk, err := e.Tasks().Save(Task{Title: "拆开做", Parallel: 3, Body: "把 a 和 b 都做了"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(e.Tasks().Dir() + "/" + tk.Name + ".md"); !strings.Contains(string(b), "并行: 3") {
		t.Fatalf("task file:\n%s", b)
	}
	s, _ := e.CreateSession("", "", "")
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Task: tk.Name})
	if err != nil {
		t.Fatal(err)
	}
	id := runIDOf(t, raw)
	waitFor(t, "two sub-agents running", func() bool { return e.snapshot(id).Activity == "2 个子 Codex 并行中" })
	r := waitDone(t, e, id)
	if r.Parallel != 3 || r.Output != "PAR cap=3" {
		t.Fatalf("run: parallel=%d out=%q", r.Parallel, r.Output)
	}
	st := r.Steps[0]
	if len(st.Agents) != 2 || st.Agents[0].Name != "part_a" || st.Agents[0].Status != "completed" || st.Agents[1].Tokens != 1000 {
		t.Fatalf("agents: %+v %+v", st.Agents[0], st.Agents[1])
	}
	own := 0
	for _, it := range st.Items {
		if it.Kind == ItemCommand && it.ThreadID == st.Agents[1].ThreadID && it.Status == "completed" {
			own++
		}
	}
	if own != 1 {
		t.Fatal("the sub-agent's command should be on its thread")
	}
	// Follow-ups keep the setting; an explicit 1 turns it off; plain messages never get the hint.
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "再来"})
	if r = waitDone(t, e, runIDOf(t, raw)); r.Parallel != 3 || r.Output != "PAR cap=3" {
		t.Fatalf("follow-up: %d %q", r.Parallel, r.Output)
	}
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "单干", Parallel: 1})
	if r = waitDone(t, e, runIDOf(t, raw)); r.Parallel != 0 || r.Output != "DONE: 单干" {
		t.Fatalf("explicit single: %d %q", r.Parallel, r.Output)
	}
	other, _ := e.CreateSession("", "x", "")
	raw, _ = e.StartChat(ChatRequest{SessionID: other.ID, Text: "hi", Parallel: 99})
	if r = waitDone(t, e, runIDOf(t, raw)); r.Parallel != maxParallel {
		t.Fatalf("clamp: %d", r.Parallel)
	}
}
