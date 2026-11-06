package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"agenthost/internal/node"
	"agenthost/internal/workflow"
)

var reEnvName = regexp.MustCompile(`[^A-Za-z0-9_]`)

// utf8Chunks turns a byte stream into valid UTF-8 pieces, holding back an
// incomplete character at the end of a chunk until the next one arrives.
type utf8Chunks struct{ tail []byte }

func (u *utf8Chunks) next(p []byte) string {
	b := append(u.tail, p...)
	cut := len(b)
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if !utf8.FullRune(b[i:]) {
				cut = i
			}
			break
		}
	}
	u.tail = append([]byte{}, b[cut:]...)
	return string(b[:cut])
}

func (u *utf8Chunks) flush() string {
	s := string(u.tail)
	u.tail = nil
	return s
}

// runShell executes a shell step directly on the session's server (not
// through Codex), capturing stdout and stderr separately.
func (e *Engine) runShell(ctx context.Context, r *Run, rc *runCtl, st *Step, ctl *stepCtl) (string, error) {
	nd, err := e.node(r.NodeID)
	if err != nil {
		return "", err
	}
	extra := e.extraEnv(nd) // may write the ssh key to the node: outside e.mu
	e.mu.Lock()
	script := st.Run
	if st.def != nil {
		script = workflow.Render(st.def.Run, e.lookup(r))
	}
	st.Run = script
	env := append(extra,
		"AGENTHOST_RUN_ID="+r.ID,
		"AGENTHOST_STEP_ID="+st.ID,
		"AGENTHOST_WORKSPACE="+workspaceOf(nd),
	)
	for k, v := range r.Vars {
		env = append(env, "WF_"+strings.ToUpper(reEnvName.ReplaceAllString(k, "_"))+"="+v)
	}
	for _, d := range st.DependsOn {
		for _, dep := range r.Steps {
			if dep.ID == d {
				env = append(env, "STEP_"+strings.ToUpper(reEnvName.ReplaceAllString(d, "_"))+"_OUTPUT="+truncate(dep.Output, 64*1024))
			}
		}
	}
	paused := rc.paused
	e.mu.Unlock()

	if paused {
		e.mu.Lock()
		ctl.resume = func() {
			select {
			case ctl.resumeCh <- struct{}{}:
			default:
			}
		}
		e.mu.Unlock()
		e.setStepStatus(r, st, StatusPaused, "已暂停")
		if err := waitResume(ctx, ctl); err != nil {
			return "", err
		}
		e.setStepStatus(r, st, StatusRunning, "")
	}

	if err := nd.MkdirAll(st.Cwd); err != nil {
		return "", fmt.Errorf("无法创建工作目录：%w", err)
	}
	itemID := fmt.Sprintf("shell-%d", st.Attempt)
	started := time.Now()
	item := &Item{ID: itemID, Kind: ItemCommand, Status: "running", Command: script, Cwd: st.Cwd, StartedAt: &started}
	e.mu.Lock()
	e.upsertItem(r, st, item)
	e.mu.Unlock()

	p, err := nd.Start(node.Spec{Script: script, Dir: st.Cwd, Env: env})
	if err != nil {
		e.mu.Lock()
		item.Status = "failed"
		item.Stderr = err.Error()
		item.Output = err.Error()
		item.EndedAt = now()
		e.upsertItem(r, st, item)
		e.mu.Unlock()
		return "", fmt.Errorf("启动命令失败：%w", err)
	}
	argv, pid := p.Argv(), p.Pid()
	proc := &Process{Node: nd.Info().Name, Argv: argv, Cwd: st.Cwd, Pid: pid, StartedAt: started}
	e.mu.Lock()
	item.Argv = argv
	st.Processes = append(st.Processes, proc)
	e.upsertItem(r, st, item)
	e.touchRun(r)
	e.mu.Unlock()
	if len(argv) > 0 {
		e.logf(r.ID, st.ID, "exec", "", "$ %s -c %q  [服务器 %s, cwd %s, pid %d]", argv[0], truncate(script, 2000), nd.Info().Name, st.Cwd, pid)
	}

	var stdout strings.Builder
	var wg sync.WaitGroup
	drain := func(rd io.Reader, field string) {
		defer wg.Done()
		var dec utf8Chunks
		buf := make([]byte, 32*1024)
		emit := func(s string) {
			if s == "" {
				return
			}
			e.mu.Lock()
			if field == "stdout" && stdout.Len() < maxOutput {
				stdout.WriteString(s)
			}
			e.appendDelta(r, st, itemID, ItemCommand, field, s)
			e.mu.Unlock()
		}
		for {
			n, err := rd.Read(buf)
			if n > 0 {
				emit(dec.next(buf[:n]))
			}
			if err != nil {
				emit(dec.flush())
				return
			}
		}
	}
	wg.Add(2)
	go drain(p.Stdout(), "stdout")
	go drain(p.Stderr(), "stderr")

	e.mu.Lock()
	ctl.pause = func() {
		_ = p.Signal(syscall.SIGSTOP)
		e.setStepStatus(r, st, StatusPaused, "已暂停（进程已挂起）")
		e.logf(r.ID, st.ID, "host", "info", "已挂起进程组 %d (SIGSTOP)", pid)
	}
	ctl.resume = func() {
		_ = p.Signal(syscall.SIGCONT)
		e.setStepStatus(r, st, StatusRunning, "执行命令: "+truncate(firstLine(script), 120))
		e.logf(r.ID, st.ID, "host", "info", "已恢复进程组 %d (SIGCONT)", pid)
	}
	e.mu.Unlock()

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		wg.Wait()
		c, err := p.Wait()
		done <- result{c, err}
	}()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		_ = p.Signal(syscall.SIGTERM)
		_ = p.Signal(syscall.SIGCONT)
		select {
		case res = <-done:
		case <-time.After(5 * time.Second):
			_ = p.Signal(syscall.SIGKILL)
			res = <-done
		}
	}
	code := res.code
	e.mu.Lock()
	ctl.pause, ctl.resume = nil, nil
	item.ExitCode = &code
	item.EndedAt = now()
	item.DurationMs = time.Since(started).Milliseconds()
	switch {
	case ctx.Err() != nil:
		item.Status = "stopped"
	case code == 0 && res.err == nil:
		item.Status = "completed"
	default:
		item.Status = "failed"
	}
	e.upsertItem(r, st, item)
	st.ExitCode = &code
	proc.EndedAt = item.EndedAt
	proc.ExitCode = &code
	if res.err != nil {
		proc.Error = res.err.Error()
	}
	stderrTail := strings.TrimSpace(item.Stderr)
	out := strings.TrimSpace(stdout.String())
	e.touchRun(r)
	e.mu.Unlock()
	e.logf(r.ID, st.ID, "exec", "", "进程 %d 退出，退出码 %d，耗时 %dms", pid, code, item.DurationMs)

	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if res.err != nil {
		return out, fmt.Errorf("命令执行中断：%v", res.err)
	}
	if code != 0 {
		msg := fmt.Sprintf("命令退出码 %d", code)
		if stderrTail != "" {
			lines := strings.Split(stderrTail, "\n")
			msg += "：" + truncate(lines[len(lines)-1], 300)
		}
		return out, errors.New(msg)
	}
	return out, nil
}

// runSubWorkflow runs another workflow from the library as a child run.
func (e *Engine) runSubWorkflow(ctx context.Context, r *Run, rc *runCtl, st *Step, ctl *stepCtl) (string, error) {
	if rc.depth+1 > e.cfg.MaxDepth {
		return "", fmt.Errorf("子 workflow 嵌套层数超过上限 %d", e.cfg.MaxDepth)
	}
	e.mu.Lock()
	look := e.lookup(r)
	name := workflow.Render(st.def.Workflow, look)
	with := map[string]string{}
	for k, v := range st.def.With {
		with[k] = workflow.Render(v, look)
	}
	e.mu.Unlock()

	src, err := e.lib.Read(name)
	if err != nil {
		return "", fmt.Errorf("找不到子 workflow「%s」：%w", name, err)
	}
	wf, err := workflow.Parse(src, strings.TrimSuffix(name, ".md"))
	if err != nil {
		return "", fmt.Errorf("子 workflow「%s」解析失败：%w", name, err)
	}

	e.mu.Lock()
	s := e.sessions[r.SessionID]
	if s == nil {
		e.mu.Unlock()
		return "", ErrNotFound
	}
	child := e.newWorkflowRun(s, wf, src, name, e.resolveDir(st.Cwd, wf.Cwd), with, firstNonEmpty(wf.Model, r.Model), firstNonEmpty(wf.Sandbox, r.Sandbox))
	child.ParentRunID = r.ID
	child.ParentStepID = st.ID
	child.Prompt = fmt.Sprintf("由「%s」调用", st.Title)
	st.ChildRunID = child.ID
	e.addItemLocked(r, st, &Item{ID: "call-" + child.ID, Kind: ItemAgent, Status: "completed", Tool: "workflow",
		Title: "调用子 workflow「" + wf.Name + "」", Text: child.ID, StartedAt: now()})
	if _, err := e.launch(s, child, ctx, rc.depth+1); err != nil {
		e.mu.Unlock()
		return "", err
	}
	childRC := e.ctls[child.ID]
	if rc.paused {
		childRC.paused = true
		child.Status = StatusPaused
	}
	e.touchRun(r)
	e.mu.Unlock()
	e.logf(r.ID, st.ID, "host", "info", "调用子 workflow「%s」→ 子任务 %s", wf.Name, child.ID)

	e.mu.Lock()
	ctl.pause = func() {
		_ = e.Pause(child.ID)
		e.setStepStatus(r, st, StatusPaused, "子 workflow 已暂停")
	}
	ctl.resume = func() {
		_ = e.Resume(child.ID)
		e.setStepStatus(r, st, StatusRunning, "")
	}
	e.mu.Unlock()

	<-childRC.done
	e.mu.Lock()
	status, out, cerr := child.Status, child.Output, child.Error
	e.mu.Unlock()
	switch status {
	case StatusCompleted:
		return out, nil
	case StatusStopped:
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("子 workflow 被停止")
	default:
		return out, fmt.Errorf("子 workflow 失败：%s", cerr)
	}
}
