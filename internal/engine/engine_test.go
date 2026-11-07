package engine

import (
	"agenthost/internal/node"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a fake `codex app-server` when FAKE_CODEX=1.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_CODEX") == "1" {
		fakeAppServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeAppServer() {
	var mu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	notify := func(method string, params any) {
		send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	}
	fmt.Fprintln(os.Stderr, "fake codex app-server starting")
	// Server -> client requests (tool calls) and their answers.
	var pmu sync.Mutex
	pending := map[string]chan json.RawMessage{}
	srvN := 0
	call := func(method string, params any) json.RawMessage {
		pmu.Lock()
		srvN++
		id := fmt.Sprintf("srv-%d", srvN)
		ch := make(chan json.RawMessage, 1)
		pending[id] = ch
		pmu.Unlock()
		send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		return <-ch
	}
	var dynTools []string
	thread := ""
	interrupted := make(chan struct{}, 1)
	steered := make(chan string, 4)
	curTurn := ""
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	turnN := 0
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &msg) != nil || len(msg.ID) == 0 {
			continue
		}
		if msg.Method == "" { // answer to one of our requests
			var full struct {
				Result json.RawMessage `json:"result"`
			}
			json.Unmarshal(sc.Bytes(), &full)
			var id string
			json.Unmarshal(msg.ID, &id)
			pmu.Lock()
			ch := pending[id]
			delete(pending, id)
			pmu.Unlock()
			if ch != nil {
				ch <- full.Result
			}
			continue
		}
		reply := func(result any) { send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result}) }
		switch msg.Method {
		case "initialize":
			reply(map[string]any{"userAgent": "fake"})
		case "thread/start":
			var p struct {
				DynamicTools []struct {
					Name string `json:"name"`
				} `json:"dynamicTools"`
			}
			json.Unmarshal(msg.Params, &p)
			dynTools = nil
			for _, t := range p.DynamicTools {
				dynTools = append(dynTools, t.Name)
			}
			thread = fmt.Sprintf("thread-%d", time.Now().UnixNano())
			reply(map[string]any{"thread": map[string]any{"id": thread}})
		case "thread/resume":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			json.Unmarshal(msg.Params, &p)
			thread = p.ThreadID
			reply(map[string]any{"thread": map[string]any{"id": thread}})
		case "turn/steer":
			var p struct {
				ExpectedTurnID string `json:"expectedTurnId"`
				Input          []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			json.Unmarshal(msg.Params, &p)
			if p.ExpectedTurnID != curTurn {
				send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32600, "message": "no such active turn"}})
				continue
			}
			text := ""
			for _, in := range p.Input {
				text += in.Text
			}
			steered <- text
			reply(map[string]any{"turnId": curTurn})
		case "turn/interrupt":
			reply(map[string]any{})
			select {
			case interrupted <- struct{}{}:
			default:
			}
		case "turn/start":
			var p struct {
				Input []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					Name string `json:"name"`
					Path string `json:"path"`
				} `json:"input"`
			}
			json.Unmarshal(msg.Params, &p)
			prompt := ""
			for _, in := range p.Input {
				prompt += in.Text
			}
			turnN++
			turn := fmt.Sprintf("turn-%d", turnN)
			curTurn = turn
			reply(map[string]any{"turn": map[string]any{"id": turn}})
			go func() {
				base := map[string]any{"threadId": thread, "turnId": turn}
				with := func(kv map[string]any) map[string]any {
					m := map[string]any{}
					for k, v := range base {
						m[k] = v
					}
					for k, v := range kv {
						m[k] = v
					}
					return m
				}
				done := func(status string, errMsg string) {
					t := map[string]any{"id": turn, "status": status}
					if errMsg != "" {
						t["error"] = map[string]any{"message": errMsg}
					}
					notify("turn/completed", map[string]any{"threadId": thread, "turn": t})
				}
				notify("turn/started", with(nil))
				reply2 := func(text string) {
					id := "msg-" + turn
					notify("item/started", with(map[string]any{"item": map[string]any{"type": "agentMessage", "id": id, "text": "", "phase": "final_answer"}}))
					notify("item/completed", with(map[string]any{"item": map[string]any{"type": "agentMessage", "id": id, "text": text, "phase": "final_answer"}}))
					done("completed", "")
				}
				tool := func(name string, args any) map[string]any {
					item := map[string]any{"type": "dynamicToolCall", "id": "dt-" + name, "tool": name, "arguments": args, "status": "inProgress"}
					notify("item/started", with(map[string]any{"item": item}))
					raw := call("item/tool/call", with(map[string]any{"callId": "c-" + name, "tool": name, "arguments": args}))
					item["status"] = "completed"
					notify("item/completed", with(map[string]any{"item": item}))
					var res map[string]any
					json.Unmarshal(raw, &res)
					return res
				}
				// The manager: answers from the task state, or uses its tools.
				if strings.Contains(prompt, "【用户的问题】") { // resumed threads keep their tools in real Codex
					q := prompt[strings.Index(prompt, "【用户的问题】")+len("【用户的问题】"):]
					q = strings.TrimSpace(q)
					switch {
					case strings.HasPrefix(q, "转告："):
						res := tool("send_to_task", map[string]any{"message": strings.TrimPrefix(q, "转告：")})
						reply2(fmt.Sprintf("MGR send ok=%v %v", res["success"], res["contentItems"]))
					case strings.HasPrefix(q, "暂停"):
						res := tool("pause_task", map[string]any{})
						reply2(fmt.Sprintf("MGR pause ok=%v", res["success"]))
					default:
						res := tool("get_task_status", map[string]any{})
						status := ""
						if items, _ := res["contentItems"].([]any); len(items) > 0 {
							status = items[0].(map[string]any)["text"].(string)
						}
						state := regexp.MustCompile(`状态：(\S+?)[，\n]`).FindStringSubmatch(status)
						if state == nil {
							state = []string{"", "?"}
						}
						reply2("MGR " + q + " → 任务" + state[1] + " | inPrompt=" + fmt.Sprint(strings.Contains(prompt, "【任务现状")))
					}
					return
				}
				// Parallel: two sub-agents on their own threads, then a summary.
				if strings.Contains(prompt, "最多同时派") {
					cap := "?"
					for i, a := range os.Args {
						if i > 0 && os.Args[i-1] == "-c" && strings.HasPrefix(a, "agents.max_concurrent_threads_per_session=") {
							cap = strings.TrimPrefix(a, "agents.max_concurrent_threads_per_session=")
						}
					}
					sub := func(name, th string, kind string) {
						item := map[string]any{"type": "subAgentActivity", "id": "sa-" + name + kind, "kind": kind, "agentThreadId": th, "agentPath": "/root/" + name}
						notify("item/started", with(map[string]any{"item": item}))
						notify("item/completed", with(map[string]any{"item": item}))
					}
					for _, n := range []string{"part_a", "part_b"} {
						th := thread + "-" + n
						sub(n, th, "started")
						cmd := map[string]any{"type": "commandExecution", "id": "cmd-" + n, "command": "make " + n, "cwd": "/tmp", "status": "inProgress"}
						notify("item/started", map[string]any{"threadId": th, "turnId": "t-" + n, "item": cmd})
						notify("thread/tokenUsage/updated", map[string]any{"threadId": th, "turnId": "t-" + n, "tokenUsage": map[string]any{"total": map[string]any{"totalTokens": 1000}}})
					}
					time.Sleep(300 * time.Millisecond) // both running: the test looks at the activity
					for _, n := range []string{"part_a", "part_b"} {
						th := thread + "-" + n
						code := 0
						notify("item/completed", map[string]any{"threadId": th, "turnId": "t-" + n, "item": map[string]any{"type": "commandExecution", "id": "cmd-" + n, "command": "make " + n, "cwd": "/tmp", "status": "completed", "exitCode": code}})
						notify("item/completed", map[string]any{"threadId": th, "turnId": "t-" + n, "item": map[string]any{"type": "agentMessage", "id": "msg-" + n, "text": n + " done", "phase": "final_answer"}})
						sub(n, th, "completed")
					}
					reply2("PAR cap=" + cap)
					return
				}
				if strings.Contains(prompt, "report_progress") && contains(dynTools, "report_progress") {
					tool("report_progress", map[string]any{"steps": []map[string]any{{"title": "看文件", "status": "running"}, {"title": "写总结", "status": "pending"}}})
					tool("report_progress", map[string]any{"steps": []map[string]any{{"title": "看文件", "status": "completed", "note": "3 个文件"}, {"title": "写总结", "status": "completed"}}})
					reply2("ORCH DONE")
					return
				}
				notify("item/started", with(map[string]any{"item": map[string]any{"type": "reasoning", "id": "rs-" + turn, "summary": []string{}}}))
				notify("item/reasoning/summaryTextDelta", with(map[string]any{"itemId": "rs-" + turn, "delta": "thinking about it"}))
				notify("item/completed", with(map[string]any{"item": map[string]any{"type": "reasoning", "id": "rs-" + turn, "summary": []string{"thinking about it"}}}))
				notify("turn/plan/updated", with(map[string]any{"plan": []map[string]any{{"step": "do it", "status": "inProgress"}}}))
				cmd := map[string]any{"type": "commandExecution", "id": "cmd-" + turn, "command": "/bin/bash -lc 'echo hi'", "cwd": "/tmp", "status": "inProgress"}
				notify("item/started", with(map[string]any{"item": cmd}))
				notify("item/commandExecution/outputDelta", with(map[string]any{"itemId": "cmd-" + turn, "delta": "hi\n"}))
				if strings.Contains(prompt, "SLOW") {
					select {
					case <-interrupted:
						done("interrupted", "")
						return
					case text := <-steered:
						if strings.Contains(prompt, "STEERABLE") {
							reply2("STEERED: " + strings.TrimSpace(text[strings.LastIndex(text, "\n")+1:]))
							return
						}
					case <-time.After(20 * time.Second):
					}
				}
				code := 0
				cmd["status"], cmd["exitCode"], cmd["aggregatedOutput"], cmd["durationMs"] = "completed", code, "hi\n", 5
				notify("item/completed", with(map[string]any{"item": cmd}))
				if strings.Contains(prompt, "FAIL") {
					done("failed", "model said no")
					return
				}
				msgID := "msg-" + turn
				notify("item/started", with(map[string]any{"item": map[string]any{"type": "agentMessage", "id": msgID, "text": "", "phase": "final_answer"}}))
				first := strings.SplitN(prompt, "\n", 2)[0]
				answer := "DONE: " + first
				for _, part := range []string{answer} {
					notify("item/agentMessage/delta", with(map[string]any{"itemId": msgID, "delta": part}))
				}
				notify("item/completed", with(map[string]any{"item": map[string]any{"type": "agentMessage", "id": msgID, "text": answer, "phase": "final_answer"}}))
				done("completed", "")
			}()
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "unknown"}})
		}
	}
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	os.Setenv("FAKE_CODEX", "1")
	t.Cleanup(func() { os.Unsetenv("FAKE_CODEX") })
	exe, _ := os.Executable()
	e, err := New(Config{DataDir: dir + "/data", Workspace: dir + "/ws", CodexBin: exe})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func runIDOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v struct {
		Run struct {
			ID string `json:"id"`
		} `json:"run"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.Run.ID == "" {
		t.Fatalf("bad launch response %s: %v", raw, err)
	}
	return v.Run.ID
}

func (e *Engine) snapshot(id string) Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := *e.runs[id]
	steps := make([]*Step, len(r.Steps))
	for i, st := range r.Steps {
		cp := *st
		cp.Items = append([]*Item{}, st.Items...)
		steps[i] = &cp
	}
	r.Steps = steps
	return r
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitDone(t *testing.T, e *Engine, id string) Run {
	t.Helper()
	waitFor(t, "run "+id+" to finish", func() bool { return e.snapshot(id).Status.Terminal() })
	return e.snapshot(id)
}

func TestChatRunAndFollowUp(t *testing.T) {
	e := newTestEngine(t)
	s, err := e.CreateSession("", "proj", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "hello there"})
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	if r.Status != StatusCompleted || r.Output != "DONE: hello there" {
		t.Fatalf("status=%s output=%q err=%q", r.Status, r.Output, r.Error)
	}
	kinds := map[string]bool{}
	for _, it := range r.Steps[0].Items {
		kinds[it.Kind] = true
	}
	for _, k := range []string{ItemReasoning, ItemCommand, ItemMessage, ItemPlan} {
		if !kinds[k] {
			t.Errorf("missing item kind %s in %v", k, kinds)
		}
	}
	e.mu.Lock()
	thread := e.sessions[s.ID].ThreadID
	e.mu.Unlock()
	if thread == "" {
		t.Fatal("session thread not recorded")
	}
	if len(r.Steps[0].Processes) != 1 || r.Steps[0].Processes[0].Pid == 0 {
		t.Fatalf("process not recorded: %+v", r.Steps[0].Processes)
	}

	raw, err = e.StartChat(ChatRequest{SessionID: s.ID, Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	r2 := waitDone(t, e, runIDOf(t, raw))
	if r2.Steps[0].ThreadID != thread {
		t.Fatalf("follow-up did not resume thread: %s vs %s", r2.Steps[0].ThreadID, thread)
	}
	logs, _ := e.Logs(r2.ID, 0, 0)
	foundResume := false
	for _, l := range logs {
		if strings.Contains(l.Text, "thread/resume") {
			foundResume = true
		}
	}
	if !foundResume {
		t.Fatal("expected thread/resume in rpc log")
	}
}

func TestBusySession(t *testing.T) {
	e := newTestEngine(t)
	s, _ := e.CreateSession("", "", "")
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW"})
	if err != nil {
		t.Fatal(err)
	}
	// A message while it runs goes to the manager; another task is refused.
	raw2, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if m := waitDone(t, e, runIDOf(t, raw2)); m.Kind != KindManager {
		t.Fatalf("expected a manager reply, got %s", m.Kind)
	}
	if _, err := e.startChat(ChatRequest{SessionID: s.ID, Text: "again"}, ""); err != ErrBusy {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	id := runIDOf(t, raw)
	if err := e.Stop(id); err != nil {
		t.Fatal(err)
	}
	if r := waitDone(t, e, id); r.Status != StatusStopped {
		t.Fatalf("status %s", r.Status)
	}
}

func TestPauseResumeAgent(t *testing.T) {
	e := newTestEngine(t)
	s, _ := e.CreateSession("", "", "")
	raw, _ := e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW job"})
	id := runIDOf(t, raw)
	waitFor(t, "command to start", func() bool {
		r := e.snapshot(id)
		return len(r.Steps[0].Items) >= 2 && r.Steps[0].TurnID != ""
	})
	if err := e.Pause(id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "step paused", func() bool { return e.snapshot(id).Steps[0].Status == StatusPaused })
	if r := e.snapshot(id); r.Status != StatusPaused {
		t.Fatalf("run status %s", r.Status)
	}
	if err := e.Resume(id); err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, id)
	if r.Status != StatusCompleted || !strings.HasPrefix(r.Output, "DONE: 继续执行") {
		t.Fatalf("status=%s output=%q", r.Status, r.Output)
	}
	if len(r.Steps[0].Processes) != 2 {
		t.Fatalf("expected two codex processes (before/after pause), got %d", len(r.Steps[0].Processes))
	}
}

func TestRetry(t *testing.T) {
	e := newTestEngine(t)
	s, _ := e.CreateSession("", "", "")
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	raw, err = e.Retry(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	r2 := waitDone(t, e, runIDOf(t, raw))
	if r2.RetryOf != r.ID || r2.Status != StatusCompleted || r2.Prompt != r.Prompt || len(r2.Steps) != 1 {
		t.Fatalf("retry: %+v", r2)
	}
}

func TestPersistenceMarksInterruptedRuns(t *testing.T) {
	e := newTestEngine(t)
	s, _ := e.CreateSession("", "", "")
	raw, _ := e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW"})
	id := runIDOf(t, raw)
	waitFor(t, "running", func() bool { return e.snapshot(id).Status == StatusRunning })
	e.Flush()
	e2, err := New(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := e2.snapshot(id)
	if r.Status != StatusFailed || !strings.Contains(r.Error, "重启") {
		t.Fatalf("reloaded run: %s %q", r.Status, r.Error)
	}
	_ = e.Stop(id)
	waitDone(t, e, id)
}

// Tasks in a session bound to a remote server run there, over the node connection.
func TestRemoteServer(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("FAKE_CODEX", "1")
	t.Cleanup(func() { os.Unsetenv("FAKE_CODEX") })
	exe, _ := os.Executable()
	reg := node.NewRegistry(node.NewLocal(node.Info{Workspace: dir + "/hubws", CodexBin: exe}), dir, "tok")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/nodes/connect", reg.HandleConnect)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	remoteWS := dir + "/box2ws"
	os.MkdirAll(remoteWS, 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	agentDone := make(chan struct{})
	go func() {
		node.RunAgent(ctx, node.AgentConfig{Hub: srv.URL, Token: "tok", Local: node.NewLocal(node.Info{Name: "box2", Workspace: remoteWS, CodexBin: exe, DataDir: dir + "/box2data"})})
		close(agentDone)
	}()
	defer func() { cancel(); <-agentDone }()

	e, err := New(Config{DataDir: dir + "/data", Workspace: dir + "/hubws", Nodes: reg})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "box2 online", func() bool { n, err := reg.Get("box2"); return err == nil && n.Info().Online })

	s, err := e.CreateSession("", "proj", "box2")
	if err != nil {
		t.Fatal(err)
	}
	if s.NodeID != "box2" || s.Cwd != remoteWS+"/proj" {
		t.Fatalf("session %+v", s)
	}
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "hi remote"})
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	if r.Status != StatusCompleted || r.Output != "DONE: hi remote" || r.NodeID != "box2" || r.Steps[0].Processes[0].Node != "box2" {
		t.Fatalf("remote chat: %s %q node=%s", r.Status, r.Output, r.NodeID)
	}
	// The connection drops in the middle of a task.
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW"})
	id := runIDOf(t, raw)
	waitFor(t, "turn started", func() bool { return e.snapshot(id).Steps[0].TurnID != "" })
	cancel()
	<-agentDone
	r = waitDone(t, e, id)
	if r.Status != StatusFailed || !strings.Contains(r.Steps[0].Error, "退出") {
		t.Fatalf("lost node: %s %q", r.Status, r.Steps[0].Error)
	}
	if _, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "again"}); err == nil || !strings.Contains(err.Error(), "不在线") {
		t.Fatalf("offline server should refuse new tasks: %v", err)
	}
	if _, err := e.CreateSession("", "", "nope"); err == nil {
		t.Fatal("unknown server must be rejected")
	}
}

func TestSSHKeyFormat(t *testing.T) {
	priv, pub, err := newSSHKey("agenthost")
	if err != nil {
		t.Fatal(err)
	}
	if got := publicKeyOf(priv); got+" agenthost" != pub {
		t.Fatalf("publicKeyOf mismatch:\n%s\n%s", got, pub)
	}
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Skip("ssh-keygen not installed")
	}
	f := t.TempDir() + "/k"
	os.WriteFile(f, []byte(priv), 0o600)
	out, err := exec.Command(keygen, "-y", "-f", f).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen rejected the key: %v %s", err, out)
	}
	if strings.Fields(string(out))[1] != strings.Fields(pub)[1] {
		t.Fatalf("ssh-keygen derived a different public key:\n%s\n%s", out, pub)
	}
}

func TestSettingsApplied(t *testing.T) {
	e := newTestEngine(t)
	if v := e.GetSettings(); v.Codex.APIKeySet || v.Git.SSHKeySet {
		t.Fatal("fresh settings should be empty")
	}
	nd, _ := e.cfg.Nodes.Get("")
	if args := strings.Join(e.codexArgs(nd), " "); args != `-c sandbox_mode="danger-full-access" -c approval_policy="never"` {
		t.Fatalf("no settings must mean only the full-access overrides: %v", args)
	}
	url, key, model, net := "https://llm.example.com/v1", "sk-test-123456789", "gpt-x", true
	user, tok, host, name, mail := "bot", "ghp_abcdef123456", "github.com", "AgentHost Bot", "bot@example.com"
	var p SettingsPatch
	json.Unmarshal([]byte(`{"codex":{},"git":{}}`), &p)
	p.Codex.BaseURL, p.Codex.APIKey, p.Codex.Model, p.Codex.NetworkAccess = &url, &key, &model, &net
	p.Git.TokenUser, p.Git.Token, p.Git.TokenHost, p.Git.UserName, p.Git.UserEmail = &user, &tok, &host, &name, &mail
	v, err := e.UpdateSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Codex.APIKeySet || v.Codex.APIKey != "" || v.Codex.APIKeyHint != "sk-…6789" || !v.Git.TokenSet || v.Git.Token != "" {
		t.Fatalf("secrets leaked or not marked: %+v", v)
	}
	b, _ := os.ReadFile(e.settings.path)
	if fi, _ := os.Stat(e.settings.path); fi.Mode().Perm() != 0o600 || !strings.Contains(string(b), key) {
		t.Fatalf("settings file mode %v", fi.Mode())
	}
	args := strings.Join(e.codexArgs(nd), " ")
	for _, want := range []string{`model_provider="agenthost"`, `base_url="https://llm.example.com/v1"`, `env_key="AGENTHOST_CODEX_API_KEY"`, `model="gpt-x"`} {
		if !strings.Contains(args, want) {
			t.Fatalf("codex args missing %s: %s", want, args)
		}
	}
	if e.defaultModel() != "gpt-x" {
		t.Fatal("settings model should be the default")
	}

	// A nil secret keeps the stored value; other fields still update.
	var p2 SettingsPatch
	json.Unmarshal([]byte(`{"codex":{"model":"gpt-y"}}`), &p2)
	e.UpdateSettings(p2)
	if s := e.settings.get(); s.Codex.APIKey != key || s.Codex.Model != "gpt-y" {
		t.Fatalf("partial update: %+v", s.Codex)
	}
	// Bad values are rejected.
	bad := "ftp://x"
	var p3 SettingsPatch
	json.Unmarshal([]byte(`{"codex":{}}`), &p3)
	p3.Codex.BaseURL = &bad
	if _, err := e.UpdateSettings(p3); err == nil {
		t.Fatal("bad url accepted")
	}

	// The credential helper really answers git, for the configured host only.
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	env := e.extraEnv(nd)
	// Hermetic: no system/global git config, except a competing helper that
	// must not win for the configured host.
	home := t.TempDir()
	os.WriteFile(home+"/gitconfig", []byte("[credential]\n\thelper = \"!f() { echo username=other; echo password=other-pass; }; f\"\n"), 0o644)
	fill := func(h string) string {
		cmd := exec.Command(gitBin, "credential", "fill")
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=" + home + "/gitconfig", "GIT_TERMINAL_PROMPT=0"}, env...)
		cmd.Stdin = strings.NewReader("protocol=https\nhost=" + h + "\n\n")
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	redact := func(s string) string {
		return regexp.MustCompile(`password=.*`).ReplaceAllString(s, "password=<redacted>")
	}
	if out := fill("github.com"); !strings.Contains(out, "username=bot\n") || !strings.Contains(out, "password=ghp_abcdef123456") {
		t.Fatalf("configured token must win for its host: %s", redact(out))
	}
	if out := fill("gitlab.com"); strings.Contains(out, "ghp_abcdef123456") || !strings.Contains(out, "username=other") {
		t.Fatalf("other hosts must keep their own helpers: %s", redact(out))
	}
	if !strings.Contains(strings.Join(env, "\n"), "GIT_AUTHOR_EMAIL=bot@example.com") {
		t.Fatal("git identity missing")
	}

	// A generated key is written to the server, private, and used by ssh.
	v, err = e.GenerateSSHKey()
	if err != nil || !v.Git.SSHKeySet || !strings.HasPrefix(v.Git.SSHPublicKey, "ssh-ed25519 ") {
		t.Fatalf("generate: %v %+v", err, v.Git)
	}
	env = e.extraEnv(nd)
	var sshCmd string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			sshCmd = kv
		}
	}
	keyPath := e.cfg.DataDir + "/private/git_ssh_key"
	fi, err := os.Stat(keyPath)
	if err != nil || fi.Mode().Perm() != 0o600 || !strings.Contains(sshCmd, keyPath) {
		t.Fatalf("key file: %v %v %q", err, fi, sshCmd)
	}
}

func TestFilesAndGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	e := newTestEngine(t)
	s, _ := e.CreateSession("", "repo", "")
	run := func(args ...string) {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = s.Cwd
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	g, _ := e.GitStatus(s.ID)
	if g.Repo {
		t.Fatal("not a repo yet")
	}
	run("init", "-q", "-b", "main")
	os.WriteFile(s.Cwd+"/a.txt", []byte("hello 中文\n"), 0o644)
	run("add", "a.txt")
	run("commit", "-q", "-m", "first commit")
	os.WriteFile(s.Cwd+"/a.txt", []byte("changed\n"), 0o644)
	os.WriteFile(s.Cwd+"/new file.txt", []byte("x"), 0o644)
	os.WriteFile(s.Cwd+"/img.png", []byte("\x89PNG\r\n\x1a\n\x00\x00"), 0o644)
	g, err = e.GitStatus(s.ID)
	if err != nil || !g.Repo || g.Branch != "main" || !strings.HasPrefix(g.Last, "") || !strings.Contains(g.Last, "first commit") {
		t.Fatalf("status %+v %v", g, err)
	}
	got := map[string]string{}
	for _, f := range g.Files {
		got[f.Path] = f.Status
	}
	if got["a.txt"] != "M" || got["new file.txt"] != "?" {
		t.Fatalf("files %+v", g.Files)
	}

	l, err := e.ListFiles("", s.Cwd)
	if err != nil || len(l.Entries) < 3 || !l.Entries[0].Dir || l.Entries[0].Name != ".git" {
		t.Fatalf("list %+v %v", l, err)
	}
	v, err := e.ReadFileView("", s.Cwd+"/a.txt")
	if err != nil || v.Kind != "text" || v.Text != "changed\n" {
		t.Fatalf("text view %+v %v", v, err)
	}
	if v, _ := e.ReadFileView("", s.Cwd+"/img.png"); v.Kind != "image" {
		t.Fatalf("image kind %+v", v)
	}
	os.WriteFile(s.Cwd+"/bin", []byte{1, 2, 0, 3}, 0o644)
	if v, _ := e.ReadFileView("", s.Cwd+"/bin"); v.Kind != "binary" || v.Text != "" {
		t.Fatalf("binary kind %+v", v)
	}
	// Relative paths resolve against the server's workspace.
	if v, err := e.ReadFileView("", "repo/a.txt"); err != nil || v.Path != s.Cwd+"/a.txt" {
		t.Fatalf("relative %+v %v", v, err)
	}

	// Git sync is a chat run with a fixed title.
	raw, err := e.GitSync(s.ID, "只提交 a.txt")
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	if r.Title != "同步到 Git 远端" || !strings.Contains(r.Prompt, "不要使用 --force") || !strings.Contains(r.Prompt, "只提交 a.txt") {
		t.Fatalf("sync run %+v", r)
	}
	if r.Sandbox != CodexSandbox {
		t.Fatalf("sync run sandbox %q", r.Sandbox)
	}
	notRepo, _ := e.CreateSession("", "plain", "")
	if _, err := e.GitSync(notRepo.ID, ""); err == nil {
		t.Fatal("git sync outside a repo should be refused")
	}
}
