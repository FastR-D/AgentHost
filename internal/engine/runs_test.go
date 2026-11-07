package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func listRuns(t *testing.T, e *Engine, f RunFilter) []RunSummary {
	t.Helper()
	var out []RunSummary
	if err := json.Unmarshal(e.ListRuns(f), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRunRecords(t *testing.T) {
	e := newTestEngine(t)
	a, _ := e.CreateSession("", "a", "")
	b, _ := e.CreateSession("", "b", "")
	var ids []string
	for _, text := range []string{"一", "二", "三"} {
		raw, err := e.StartChat(ChatRequest{SessionID: a.ID, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, waitDone(t, e, runIDOf(t, raw)).ID)
		time.Sleep(2 * time.Millisecond) // distinct creation times for paging
	}
	raw, _ := e.StartChat(ChatRequest{SessionID: b.ID, Text: "SLOW"})
	busy := runIDOf(t, raw)
	waitFor(t, "busy turn", func() bool { return e.snapshot(busy).Steps[0].TurnID != "" })
	// A manager answer about the busy task: not a record of its own.
	raw, _ = e.StartChat(ChatRequest{SessionID: b.ID, Text: "进度？"})
	mgr := waitDone(t, e, runIDOf(t, raw)).ID

	if st := e.RunStats(""); st.Total != 4 || st.Active != 1 || st.Counts["completed"] != 3 {
		t.Fatalf("stats: %+v", st)
	}
	// Paging: newest first, then older than the last one seen.
	page := listRuns(t, e, RunFilter{TasksOnly: true, Limit: 2})
	if len(page) != 2 || page[0].ID != busy || page[1].ID != ids[2] {
		t.Fatalf("page 1: %v", page)
	}
	page = listRuns(t, e, RunFilter{TasksOnly: true, Limit: 2, Before: page[1].CreatedAt})
	if len(page) != 2 || page[0].ID != ids[1] || page[1].ID != ids[0] {
		t.Fatalf("page 2: %v", page)
	}

	if err := e.DeleteRun(busy); err == nil {
		t.Fatal("a running task must not be deleted")
	}
	if err := e.DeleteRun(mgr); err != nil {
		t.Fatal(err) // allowed: it is finished
	}
	if err := e.DeleteRun(ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "runs", ids[1]+".json")); !os.IsNotExist(err) {
		t.Fatal("run file not removed")
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "logs", ids[1]+".jsonl")); !os.IsNotExist(err) {
		t.Fatal("log file not removed")
	}
	e.mu.Lock()
	left := append([]string{}, e.sessions[a.ID].RunIDs...)
	e.mu.Unlock()
	if len(left) != 2 || left[0] != ids[0] || left[1] != ids[2] {
		t.Fatalf("session runs: %v", left)
	}

	// Cleanup: dry run counts, the real one deletes; the busy task stays,
	// and the session left empty is removed with its runs.
	if n := e.CleanupRuns("", time.Hour, false); n != 0 {
		t.Fatalf("nothing is an hour old: %d", n)
	}
	if n := e.CleanupRuns("", 0, true); n != 2 {
		t.Fatalf("dry run: %d", n)
	}
	if n := e.CleanupRuns("", 0, false); n != 2 {
		t.Fatalf("cleanup: %d", n)
	}
	e.mu.Lock()
	_, aLeft := e.sessions[a.ID]
	_, bLeft := e.sessions[b.ID]
	e.mu.Unlock()
	if aLeft || !bLeft {
		t.Fatalf("sessions after cleanup: a=%v b=%v", aLeft, bLeft)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "sessions", a.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("empty session file not removed")
	}
	e.Stop(busy)
	waitDone(t, e, busy)
	if st := e.RunStats(""); st.Total != 1 || st.Counts["stopped"] != 1 {
		t.Fatalf("stats after: %+v", st)
	}
}
