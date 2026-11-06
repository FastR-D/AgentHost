package node

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

type fixture struct {
	reg    *Registry
	srv    *httptest.Server
	cancel context.CancelFunc
	done   chan struct{}
	ws     string
}

func startHub(t *testing.T) (*Registry, *httptest.Server) {
	reg := NewRegistry(nil, t.TempDir(), "secret")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/nodes/connect", reg.HandleConnect)
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { reg.Close(); srv.Close() })
	return reg, srv
}

func startAgent(t *testing.T, srv *httptest.Server, name, token string) (context.CancelFunc, chan struct{}, string) {
	ws := t.TempDir()
	local := NewLocal(Info{Name: name, Workspace: ws, Hostname: "box"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunAgent(ctx, AgentConfig{Hub: srv.URL, Token: token, Local: local, Logf: t.Logf})
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return cancel, done, ws
}

func online(reg *Registry, id string) bool {
	for _, i := range reg.List() {
		if i.ID == id && i.Online {
			return true
		}
	}
	return false
}

func readAll(t *testing.T, p Proc) (string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&errb, p.Stderr()); close(done) }()
	io.Copy(&out, p.Stdout())
	<-done
	return out.String(), errb.String()
}

func TestRemoteNode(t *testing.T) {
	reg, srv := startHub(t)
	_, _, ws := startAgent(t, srv, "gpu-1", "secret")
	waitUntil(t, "node online", func() bool { return online(reg, "gpu-1") })
	n, err := reg.Get("gpu-1")
	if err != nil {
		t.Fatal(err)
	}
	if i := n.Info(); i.Workspace != ws || i.Hostname != "box" || i.Local || i.Addr == "" {
		t.Fatalf("info %+v", i)
	}

	// stdin -> stdout/stderr, exit code
	p, err := n.Start(Spec{Argv: []string{"sh", "-c", "read x; echo got $x; echo oops >&2; exit 3"}, Dir: ws})
	if err != nil {
		t.Fatal(err)
	}
	if p.Pid() == 0 || len(p.Argv()) != 3 {
		t.Fatalf("pid %d argv %v", p.Pid(), p.Argv())
	}
	io.WriteString(p.Stdin(), "hel")
	io.WriteString(p.Stdin(), "lo\n")
	out, errs := readAll(t, p)
	code, werr := p.Wait()
	if out != "got hello\n" || errs != "oops\n" || code != 3 || werr != nil {
		t.Fatalf("out=%q err=%q code=%d werr=%v", out, errs, code, werr)
	}

	// script + env + cwd
	p, _ = n.Start(Spec{Script: "echo $FOO; pwd", Dir: ws, Env: []string{"FOO=bar"}})
	out, _ = readAll(t, p)
	if code, _ := p.Wait(); out != "bar\n"+ws+"\n" || code != 0 {
		t.Fatalf("script out %q code %d", out, code)
	}

	// filesystem
	sub := filepath.Join(ws, "a", "b")
	if err := n.MkdirAll(sub); err != nil || !n.IsDir(sub) || n.IsDir(filepath.Join(ws, "nope")) {
		t.Fatalf("mkdir/isdir: %v", err)
	}
	l, err := n.ListDir(filepath.Join(ws, "a"))
	if err != nil || len(l.Dirs) != 1 || l.Dirs[0] != "b" {
		t.Fatalf("list %+v %v", l, err)
	}
	if _, err := n.ListDir(filepath.Join(ws, "missing")); err == nil {
		t.Fatal("listing a missing dir should fail")
	}

	// large multi-byte output survives chunking intact
	p, _ = n.Start(Spec{Script: "i=0; while [ $i -lt 20000 ]; do printf '中文输出%05d\\n' $i; i=$((i+1)); done", Dir: ws})
	out, _ = readAll(t, p)
	p.Wait()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 20000 || lines[19999] != "中文输出19999" || !strings.HasPrefix(out, "中文输出00000\n") {
		t.Fatalf("big output: %d lines, last %q", len(lines), lines[len(lines)-1])
	}

	// signals reach the process group
	p, _ = n.Start(Spec{Script: "sleep 30 & wait", Dir: ws})
	time.Sleep(200 * time.Millisecond)
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	go readAll(t, p)
	if code, _ := p.Wait(); code != -1 {
		t.Fatalf("killed process code %d", code)
	}

	// a bad spec is reported, not hung
	if _, err := n.Start(Spec{Argv: []string{"/definitely/missing"}, Dir: ws}); err == nil {
		t.Fatal("expected start error")
	}
}

func TestDisconnectKillsAndReports(t *testing.T) {
	reg, srv := startHub(t)
	cancel, done, ws := startAgent(t, srv, "n1", "secret")
	waitUntil(t, "online", func() bool { return online(reg, "n1") })
	n, _ := reg.Get("n1")
	p, err := n.Start(Spec{Script: "echo started; sleep 60", Dir: ws})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	nr, _ := p.Stdout().Read(buf)
	if !strings.Contains(string(buf[:nr]), "started") {
		t.Fatalf("first output %q", buf[:nr])
	}
	pid := p.Pid()
	cancel()
	<-done
	go io.Copy(io.Discard, p.Stdout())
	go io.Copy(io.Discard, p.Stderr())
	code, werr := p.Wait()
	if code != -1 || werr == nil || !strings.Contains(werr.Error(), ErrLost.Error()) {
		t.Fatalf("lost process: code %d err %v", code, werr)
	}
	waitUntil(t, "offline", func() bool { return !online(reg, "n1") })
	// The node killed what it had started.
	waitUntil(t, "process killed on the node", func() bool { return syscall.Kill(pid, 0) != nil })
	// Offline nodes stay listed and refuse work.
	n, err = reg.Get("n1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Start(Spec{Script: "true"}); err != ErrOffline {
		t.Fatalf("offline start: %v", err)
	}
	// Known nodes survive a hub restart.
	reg2 := NewRegistry(nil, filepath.Dir(reg.path), "secret")
	if l := reg2.List(); len(l) != 1 || l[0].ID != "n1" || l[0].Online {
		t.Fatalf("persisted list %+v", l)
	}
	if err := reg.Forget("n1"); err != nil || len(reg.List()) != 0 {
		t.Fatalf("forget: %v %+v", err, reg.List())
	}
}

func TestConnectRejections(t *testing.T) {
	reg, srv := startHub(t)
	startAgent(t, srv, "dup", "secret")
	waitUntil(t, "online", func() bool { return online(reg, "dup") })

	// Same name while the first is online: rejected, first stays.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target, _ := ConnectURL(srv.URL)
	err := agentSession(ctx, target, AgentConfig{Token: "secret", Local: NewLocal(Info{Name: "dup"}), Logf: t.Logf})
	if err == nil || !strings.Contains(err.Error(), "同名") {
		t.Fatalf("duplicate name: %v", err)
	}
	if !online(reg, "dup") {
		t.Fatal("original node was dropped")
	}
	err = agentSession(ctx, target, AgentConfig{Token: "wrong", Local: NewLocal(Info{Name: "x"}), Logf: t.Logf})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("bad token: %v", err)
	}
	err = agentSession(ctx, target, AgentConfig{Token: "secret", Local: NewLocal(Info{Name: "local"}), Logf: t.Logf})
	if err == nil || !strings.Contains(err.Error(), "local") {
		t.Fatalf("reserved name: %v", err)
	}
}

func TestConnectURL(t *testing.T) {
	cases := map[string]string{
		"http://a:8080":            "ws://a:8080/api/nodes/connect",
		"https://x.com/agenthost/": "wss://x.com/agenthost/api/nodes/connect",
		"10.0.0.2:8080":            "ws://10.0.0.2:8080/api/nodes/connect",
	}
	for in, want := range cases {
		if got, err := ConnectURL(in); err != nil || got != want {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
	if _, err := ConnectURL("ftp://x"); err == nil {
		t.Error("ftp should be rejected")
	}
	_ = os.Getenv
}
