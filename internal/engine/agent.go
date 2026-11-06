package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agenthost/internal/codex"
	"agenthost/internal/node"
	"agenthost/internal/workflow"
)

// runAgent runs an agent step as one or more Codex turns. Pausing interrupts
// the current turn; resuming starts a new turn on the same Codex thread.
func (e *Engine) runAgent(ctx context.Context, r *Run, rc *runCtl, st *Step, ctl *stepCtl, attempt int, prevErr error) (string, error) {
	e.mu.Lock()
	ctl.pause = func() {
		select {
		case ctl.pauseCh <- struct{}{}:
		default:
		}
	}
	ctl.resume = func() {
		select {
		case ctl.resumeCh <- struct{}{}:
		default:
		}
	}
	threadID := st.ThreadID
	var prompt string
	if attempt > 1 && threadID != "" {
		prompt = fmt.Sprintf("上一次尝试失败（%s）。请重新尝试完成上面的任务。", truncate(errText(prevErr), 500))
	} else {
		switch r.Kind {
		case KindWorkflow:
			prompt = e.buildAgentPrompt(r, st)
			st.Prompt = prompt
		case KindManager:
			prompt = e.managerPrompt(r, r.Prompt) // the task's state as of now
			st.Prompt = prompt
		default:
			prompt = st.Prompt
		}
		if threadID == "" {
			threadID = e.initialThread(r, st)
		}
	}
	model, sandbox := r.Model, r.Sandbox
	if st.def != nil {
		model = firstNonEmpty(st.def.Model, model)
		sandbox = firstNonEmpty(st.def.Sandbox, sandbox)
	}
	paused := rc.paused
	e.touchRun(r)
	e.mu.Unlock()

	if paused {
		// The run was paused before this step got going.
		e.setStepStatus(r, st, StatusPaused, "已暂停")
		select {
		case <-ctl.pauseCh:
		default:
		}
		if err := waitResume(ctx, ctl); err != nil {
			return "", err
		}
		e.setStepStatus(r, st, StatusRunning, "")
	}

	first := attempt == 1
	for {
		out, err := e.agentTurn(ctx, r, st, ctl, threadID, prompt, model, sandbox, first)
		first = false // a resumed turn continues with what Codex already read
		if !errors.Is(err, errPaused) {
			return out, err
		}
		e.setStepStatus(r, st, StatusPaused, "已暂停")
		e.logf(r.ID, st.ID, "host", "info", "Codex 回合已中断，步骤暂停，等待继续")
		if err := waitResume(ctx, ctl); err != nil {
			return "", err
		}
		e.setStepStatus(r, st, StatusRunning, "继续执行")
		e.mu.Lock()
		threadID = st.ThreadID
		e.addItemLocked(r, st, &Item{ID: newID("resume"), Kind: ItemNotice, Status: "completed", Title: "已继续执行", StartedAt: now()})
		e.mu.Unlock()
		prompt = "继续执行刚才被暂停的任务，从中断的地方接着完成。"
	}
}

func (e *Engine) setStepStatus(r *Run, st *Step, s Status, activity string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st.Status = s
	st.Activity = activity
	r.Activity = e.runActivity(r)
	e.touchRun(r)
	e.publishSessionOf(r)
}

// initialThread picks the Codex thread an agent step continues. Call with e.mu held.
func (e *Engine) initialThread(r *Run, st *Step) string {
	if r.Kind == KindChat || r.Kind == KindManager {
		if s := e.sessions[r.SessionID]; s != nil {
			if r.Kind == KindManager {
				return s.ManagerThreadID
			}
			return s.ThreadID
		}
		return ""
	}
	if st.def != nil && st.def.Session != "" {
		for _, other := range r.Steps {
			if other.ID == st.def.Session {
				return other.ThreadID
			}
		}
	}
	return ""
}

// lookup resolves {{...}} template paths. Call with e.mu held.
func (e *Engine) lookup(r *Run) func(string) (string, bool) {
	return func(p string) (string, bool) {
		parts := strings.SplitN(p, ".", 3)
		switch parts[0] {
		case "vars", "inputs", "env":
			if len(parts) >= 2 {
				v, ok := r.Vars[strings.Join(parts[1:], ".")]
				return v, ok
			}
		case "steps", "步骤":
			// steps.<id | 编号 | 标题>.<field>; the reference may itself contain dots.
			if len(parts) < 3 {
				return "", false
			}
			rest := strings.Join(parts[1:], ".")
			i := strings.LastIndex(rest, ".")
			ref, field := rest[:i], rest[i+1:]
			st := e.findStepRef(r, ref)
			if st == nil {
				return "", false
			}
			switch field {
			case "output", "result", "结果", "输出":
				return st.Output, true
			case "status", "状态":
				return string(st.Status), true
			case "error", "错误":
				return st.Error, true
			case "exit_code", "退出码":
				if st.ExitCode != nil {
					return fmt.Sprint(*st.ExitCode), true
				}
				return "", true
			}
		case "run":
			if len(parts) == 2 {
				switch parts[1] {
				case "id":
					return r.ID, true
				case "cwd":
					return r.Cwd, true
				case "title":
					return r.Title, true
				}
			}
		case "workflow":
			if len(parts) == 2 && parts[1] == "name" {
				return r.Title, true
			}
		case "workspace":
			if nd, err := e.cfg.Nodes.Get(r.NodeID); err == nil {
				return workspaceOf(nd), true
			}
			return "", false
		}
		return "", false
	}
}

// codexBin is the Codex executable on a node.
func (e *Engine) codexBin(nd node.Node) string {
	if b := nd.Info().CodexBin; b != "" {
		return b
	}
	return "codex"
}

// findStepRef resolves a step by id, number or title. Call with e.mu held.
func (e *Engine) findStepRef(r *Run, ref string) *Step {
	for _, st := range r.Steps {
		if st.ID == ref {
			return st
		}
	}
	if r.Workflow == nil {
		return nil
	}
	d, err := r.Workflow.ResolveRef(ref)
	if err != nil {
		return nil
	}
	for _, st := range r.Steps {
		if st.ID == d.ID {
			return st
		}
	}
	return nil
}

// buildAgentPrompt assembles the prompt for a workflow agent step. Call with e.mu held.
func (e *Engine) buildAgentPrompt(r *Run, st *Step) string {
	look := e.lookup(r)
	if st.def != nil && st.def.Orchestrate {
		return orchestratorPrompt + workflow.Render(st.def.Prompt, look) + "\n"
	}
	var b strings.Builder
	if r.Workflow != nil && r.Workflow.Instructions != "" {
		fmt.Fprintf(&b, "# Workflow「%s」的总体说明\n\n%s\n\n", r.Title, workflow.Render(r.Workflow.Instructions, look))
	}
	raw := st.Prompt
	if st.def != nil {
		raw = st.def.Prompt
	}
	body := workflow.Render(raw, look)
	fmt.Fprintf(&b, "# 当前步骤：%s\n\n%s\n", st.Title, body)
	var deps []string
	for _, d := range st.DependsOn {
		for _, dep := range r.Steps {
			if dep.ID != d || (strings.TrimSpace(dep.Output) == "" && dep.Error == "") {
				continue
			}
			res := strings.TrimSpace(dep.Output)
			// Skip results the author already placed in the prompt with a template.
			if res != "" && strings.Contains(body, res) {
				continue
			}
			if res == "" {
				res = "（无输出）"
			}
			if dep.Error != "" && dep.Status != StatusCompleted {
				res += "\n\n错误：" + dep.Error
			}
			deps = append(deps, fmt.Sprintf("## 「%s」（%s）\n\n%s", dep.Title, statusText(dep.Status), truncate(res, 6000)))
		}
	}
	if len(deps) > 0 {
		fmt.Fprintf(&b, "\n# 上游步骤的结果\n\n%s\n", strings.Join(deps, "\n\n"))
	}
	b.WriteString("\n---\n你正在由 AgentHost 按 workflow 调度执行这个步骤。完成后，请在最后一条回复中直接给出本步骤的结果。\n")
	return b.String()
}

func statusText(s Status) string {
	switch s {
	case StatusCompleted:
		return "已完成"
	case StatusFailed:
		return "失败"
	case StatusSkipped:
		return "已跳过"
	case StatusStopped:
		return "已停止"
	}
	return string(s)
}

const orchestratorPrompt = `# 你的任务

下面是一份由用户编写的 workflow 文档，可能比较口语化。请通读全文，把要做的事拆成清晰的步骤（尽量沿用文档里已有的划分和叫法，一般 3–8 步），然后依次执行完。

## 进度汇报（必须）

AgentHost 会把你的步骤显示给用户看，所以请用 report_progress 工具汇报：
- 开始动手前调用一次，列出全部步骤，状态都是 pending；
- 每个步骤开始时把它标为 running，结束时标为 completed（失败标 failed，不需要做的标 skipped），在 note 里用一句话写结果或原因；
- 每次都传完整的步骤列表；执行中需要增删或调整步骤时，直接改列表。

全部完成后，用最后一条回复给出整体结果。

# workflow 文档

`

type turnResult struct {
	status string
	err    string
}

// agentTurn starts `codex app-server`, opens or resumes a thread, runs one
// turn and streams everything into the step timeline.
// first is the run's first turn: it reports the injected skills and attaches
// the ones the task names.
func (e *Engine) agentTurn(ctx context.Context, r *Run, st *Step, ctl *stepCtl, threadID, prompt, model, sandbox string, first bool) (string, error) {
	turnDone := make(chan turnResult, 1)
	var idMu sync.Mutex
	mainThread, mainTurn := "", ""
	ids := func() (string, string) {
		idMu.Lock()
		defer idMu.Unlock()
		return mainThread, mainTurn
	}

	h := codex.Handlers{
		Notification: func(method string, params json.RawMessage) {
			th, tu := ids()
			e.onCodexNotification(r, st, method, params, th, tu, turnDone)
		},
		Request: func(method string, params json.RawMessage) (any, error) {
			return e.onCodexRequest(r, st, method, params)
		},
		Stderr: func(line string) {
			if strings.TrimSpace(line) != "" {
				e.logf(r.ID, st.ID, "codex", "", "%s", line)
			}
		},
		Trace: func(dir, method string, payload json.RawMessage) {
			if strings.HasSuffix(method, "Delta") || strings.HasSuffix(method, "/delta") || method == "account/rateLimits/updated" ||
				method == "thread/tokenUsage/updated" || method == "item/started" || method == "item/completed" {
				return
			}
			e.logf(r.ID, st.ID, "rpc", "", "%s %s %s", dir, method, truncate(string(payload), 3000))
		},
	}

	e.setStepActivity2(r, st, "启动 Codex CLI")
	nd, err := e.node(r.NodeID)
	if err != nil {
		return "", err
	}
	args := e.codexArgsFor(nd, r.Network, r.WritableRoots...)
	if n := r.Parallel; n > 1 && r.Kind != KindManager {
		// Codex's own sub-agents; the limit counts them, not the coordinating agent.
		args = append(args, "-c", "features.multi_agent=true", "-c", fmt.Sprintf("agents.max_concurrent_threads_per_session=%d", n))
	}
	client, err := codex.Start(ctx, codex.Options{Node: nd, Bin: e.codexBin(nd), ExtraArgs: args, Env: e.extraEnv(nd), Cwd: st.Cwd, Experimental: true}, h)
	if err != nil {
		e.logf(r.ID, st.ID, "host", "error", "启动 Codex 失败：%v", err)
		return "", fmt.Errorf("启动 Codex CLI 失败：%w", err)
	}
	proc := &Process{Node: nd.Info().Name, Argv: client.Argv, Cwd: st.Cwd, Pid: client.Pid, StartedAt: time.Now()}
	e.mu.Lock()
	st.Processes = append(st.Processes, proc)
	e.touchRun(r)
	e.mu.Unlock()
	e.logf(r.ID, st.ID, "host", "info", "已在服务器「%s」启动 Codex CLI：%s（pid %d，工作目录 %s）", nd.Info().Name, strings.Join(client.Argv, " "), client.Pid, st.Cwd)
	defer func() {
		client.Close()
		<-client.Done()
		e.mu.Lock()
		proc.EndedAt = now()
		if ps := client.ExitErr(); ps != nil {
			proc.Error = ps.Error()
		}
		code := client.ExitCode()
		proc.ExitCode = &code
		e.touchRun(r)
		e.mu.Unlock()
		e.logf(r.ID, st.ID, "host", "info", "Codex CLI 进程已退出（pid %d）", proc.Pid)
	}()

	params := map[string]any{"cwd": st.Cwd, "approvalPolicy": e.cfg.ApprovalPolicy, "sandbox": sandbox}
	if model != "" {
		params["model"] = model
	}
	manager := r.Kind == KindManager
	// Skills come from the task's own code and are given to this process only.
	inj := &injected{known: map[string]string{}}
	if !manager {
		e.setStepActivity2(r, st, "查找技能")
		inj = e.injectSkills(ctx, r, st, nd, client)
	}
	// New threads get the progress tool (the manager: its own tools); Codex keeps them for later turns.
	startParams := map[string]any{"dynamicTools": []any{progressTool}, "developerInstructions": progressGuide}
	if manager {
		startParams = map[string]any{"dynamicTools": managerTools, "developerInstructions": managerGuide}
	}
	var thr struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	if threadID != "" {
		params["threadId"] = threadID
		err = client.Call(callCtx, "thread/resume", params, &thr)
		if err != nil {
			e.logf(r.ID, st.ID, "host", "warn", "恢复 Codex 线程 %s 失败（%v），改为新建线程", threadID, err)
			delete(params, "threadId")
			e.mu.Lock()
			e.addItemLocked(r, st, &Item{ID: newID("notice"), Kind: ItemNotice, Status: "completed", Title: "无法恢复之前的 Codex 会话，已开启新会话", Text: err.Error(), StartedAt: now()})
			e.mu.Unlock()
			threadID = ""
		}
	}
	if threadID == "" {
		for k, v := range startParams {
			params[k] = v
		}
		err = client.Call(callCtx, "thread/start", params, &thr)
	}
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("创建 Codex 会话失败：%w", err)
	}
	idMu.Lock()
	mainThread = thr.Thread.ID
	idMu.Unlock()
	e.mu.Lock()
	st.ThreadID = thr.Thread.ID
	e.touchRun(r)
	e.mu.Unlock()

	input := []map[string]any{}
	if first && !manager {
		// "$name" in the task asks for a skill explicitly.
		items, names := mentionedSkills(prompt, inj.known)
		input = append(input, items...)
		if it := skillsNotice(inj, names); it != nil {
			e.mu.Lock()
			e.addItemLocked(r, st, it)
			e.mu.Unlock()
		}
	}
	input = append(input, map[string]any{"type": "text", "text": prompt, "text_elements": []any{}})
	if first && !manager && r.Parallel > 1 {
		input = append(input, map[string]any{"type": "text", "text": parallelHint(r.Parallel), "text_elements": []any{}})
	}
	turnParams := map[string]any{"threadId": thr.Thread.ID, "input": input}
	if e.cfg.ReasoningSummary != "" {
		turnParams["summary"] = e.cfg.ReasoningSummary
	}
	var tr struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	callCtx, cancel = context.WithTimeout(ctx, 90*time.Second)
	err = client.Call(callCtx, "turn/start", turnParams, &tr)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("启动 Codex 回合失败：%w", err)
	}
	idMu.Lock()
	mainTurn = tr.Turn.ID
	idMu.Unlock()
	// While the turn runs, the manager can add the user's words to it.
	steer := func(text string) error {
		_, turn := ids()
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var res struct {
			TurnID string `json:"turnId"`
		}
		in := []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}
		err := client.Call(sctx, "turn/steer", map[string]any{"threadId": thr.Thread.ID, "expectedTurnId": turn, "input": in}, &res)
		if err == nil && res.TurnID != "" && res.TurnID != turn {
			idMu.Lock()
			mainTurn = res.TurnID
			idMu.Unlock()
		}
		return err
	}
	e.mu.Lock()
	st.TurnID = tr.Turn.ID
	if !manager {
		ctl.steer = steer
	}
	e.touchRun(r)
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		ctl.steer = nil
		e.mu.Unlock()
	}()
	e.setStepActivity2(r, st, "思考中")

	interrupt := func(wait time.Duration) {
		_, turn := ids()
		ictx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Call(ictx, "turn/interrupt", map[string]any{"threadId": thr.Thread.ID, "turnId": turn}, nil)
		cancel()
		if err != nil {
			e.logf(r.ID, st.ID, "host", "warn", "中断 Codex 回合失败：%v", err)
			return
		}
		select {
		case <-turnDone:
		case <-client.Done():
		case <-time.After(wait):
		}
	}

	select {
	case res := <-turnDone:
		switch res.status {
		case "completed":
			e.mu.Lock()
			out := finalMessage(st)
			e.mu.Unlock()
			return out, nil
		case "interrupted":
			return "", errors.New("Codex 回合被中断")
		default:
			msg := res.err
			if msg == "" {
				msg = "Codex 回合失败"
			}
			return "", errors.New(msg)
		}
	case <-client.Done():
		return "", fmt.Errorf("Codex CLI 进程意外退出：%v", client.ExitErr())
	case <-ctl.pauseCh:
		e.setStepActivity2(r, st, "正在暂停…")
		interrupt(20 * time.Second)
		return "", errPaused
	case <-ctx.Done():
		e.setStepActivity2(r, st, "正在停止…")
		interrupt(8 * time.Second)
		return "", ctx.Err()
	}
}

func (e *Engine) setStepActivity2(r *Run, st *Step, a string) {
	e.mu.Lock()
	e.setStepActivity(r, st, a)
	e.mu.Unlock()
}

// finalMessage returns the agent's final answer. Call with e.mu held.
func finalMessage(st *Step) string {
	for i := len(st.Items) - 1; i >= 0; i-- {
		it := st.Items[i]
		if it.Kind == ItemMessage && it.Phase == "final_answer" && it.ThreadID == "" {
			return it.Text
		}
	}
	for i := len(st.Items) - 1; i >= 0; i-- {
		it := st.Items[i]
		if it.Kind == ItemMessage && it.ThreadID == "" {
			return it.Text
		}
	}
	return ""
}

func (e *Engine) onCodexNotification(r *Run, st *Step, method string, params json.RawMessage, mainThread, mainTurn string, turnDone chan turnResult) {
	var p struct {
		ThreadID    string          `json:"threadId"`
		TurnID      string          `json:"turnId"`
		ItemID      string          `json:"itemId"`
		Delta       string          `json:"delta"`
		Item        json.RawMessage `json:"item"`
		Turn        json.RawMessage `json:"turn"`
		Diff        string          `json:"diff"`
		Explanation *string         `json:"explanation"`
		Plan        []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
		TokenUsage json.RawMessage `json:"tokenUsage"`
		Error      *struct {
			Message           string  `json:"message"`
			AdditionalDetails *string `json:"additionalDetails"`
		} `json:"error"`
		WillRetry bool   `json:"willRetry"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	foreign := mainThread != "" && p.ThreadID != "" && p.ThreadID != mainThread
	foreignID := ""
	if foreign {
		foreignID = p.ThreadID
	}

	switch method {
	case "turn/completed":
		var t struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(p.Turn, &t)
		if foreign || (mainTurn != "" && t.ID != "" && t.ID != mainTurn) {
			return
		}
		res := turnResult{status: t.Status}
		if t.Error != nil {
			res.err = t.Error.Message
		}
		select {
		case turnDone <- res:
		default:
		}
		return
	case "turn/started":
		if !foreign {
			e.setStepActivity2(r, st, "思考中")
		}
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	switch method {
	case "item/started", "item/completed":
		it := convertItem(p.Item, method == "item/completed")
		if it == nil {
			return
		}
		if foreign {
			it.ThreadID = p.ThreadID
		}
		e.upsertItem(r, st, it)
		if it.Kind == ItemAgent && it.AgentThread != "" && !foreign {
			e.trackAgent(r, st, it)
		} else if foreign {
			e.agentDid(r, st, p.ThreadID, it)
		}
		if method == "item/completed" && it.Kind == ItemCommand {
			code := "?"
			if it.ExitCode != nil {
				code = fmt.Sprint(*it.ExitCode)
			}
			go e.logf(r.ID, st.ID, "exec", "", "$ %s  [cwd %s, exit %s, %dms]", it.Command, it.Cwd, code, it.DurationMs)
		}
	case "item/agentMessage/delta":
		e.appendDeltaOn(r, st, foreignID, p.ItemID, ItemMessage, "text", p.Delta)
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		e.appendDeltaOn(r, st, foreignID, p.ItemID, ItemReasoning, "text", p.Delta)
	case "item/reasoning/summaryPartAdded":
		if it := findItem(st, p.ItemID); it != nil && it.Text != "" {
			e.appendDeltaOn(r, st, foreignID, p.ItemID, ItemReasoning, "text", "\n\n")
		}
	case "item/commandExecution/outputDelta":
		e.appendDeltaOn(r, st, foreignID, p.ItemID, ItemCommand, "output", p.Delta)
	case "item/plan/delta":
		e.appendDeltaOn(r, st, foreignID, p.ItemID, ItemPlan, "text", p.Delta)
	case "turn/plan/updated":
		it := &Item{ID: "plan-" + p.TurnID, Kind: ItemPlan, Status: "running", Title: "计划"}
		if p.Explanation != nil {
			it.Text = *p.Explanation
		}
		done := len(p.Plan) > 0
		for _, ps := range p.Plan {
			s := normStatus(ps.Status)
			if s == "" {
				s = ps.Status
			}
			it.Plan = append(it.Plan, PlanEntry{Step: ps.Step, Status: s})
			if s != "completed" {
				done = false
			}
		}
		if done {
			it.Status = "completed"
		}
		if foreign {
			it.ThreadID = p.ThreadID
		}
		e.upsertItem(r, st, it)
	case "turn/diff/updated":
		if !foreign {
			st.Diff = p.Diff
			e.dirtyRuns[r.ID] = true
			e.hub.Publish("diff", map[string]string{"runId": r.ID, "stepId": st.ID, "diff": p.Diff})
		}
	case "thread/tokenUsage/updated":
		if !foreign && len(p.TokenUsage) > 0 {
			st.Usage = append(json.RawMessage{}, p.TokenUsage...)
			e.touchRun(r)
		} else if foreign {
			e.agentTokens(r, st, p.ThreadID, p.TokenUsage)
		}
	case "error":
		if p.Error == nil {
			return
		}
		kind, title := ItemError, "错误"
		if p.WillRetry {
			kind, title = ItemNotice, "Codex 正在重试"
		}
		text := p.Error.Message
		if p.Error.AdditionalDetails != nil && *p.Error.AdditionalDetails != "" {
			text += "\n" + *p.Error.AdditionalDetails
		}
		e.addItemLocked(r, st, &Item{ID: newID("err"), Kind: kind, Status: "completed", Title: title, Text: text, StartedAt: now()})
	}
}

// progressGuide tells Codex when to report progress.
const progressGuide = `你由 AgentHost 远程调度，用户多半在手机上看进度。任务需要多个步骤时：动手前调用一次 report_progress 列出全部步骤（简短的动词短语，3–8 步，状态 pending），之后每个步骤开始时标 running、结束时标 completed（失败标 failed，不需要做的标 skipped），可以在 note 里写一句结果。每次传完整列表。简单的一步就能完成的事不需要汇报。`

// progressTool lets the agent show its steps in the UI.
var progressTool = map[string]any{
	"type":        "function",
	"name":        "report_progress",
	"description": "向 AgentHost 汇报你拆出的步骤及每个步骤的状态，用户会在界面上看到。开始前调用一次列出全部步骤，之后每个步骤开始和结束时调用，每次都传完整列表。",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"steps": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title":  map[string]any{"type": "string", "description": "步骤名称，简短的动词短语"},
						"status": map[string]any{"type": "string", "enum": []string{"pending", "running", "completed", "failed", "skipped"}},
						"note":   map[string]any{"type": "string", "description": "一句话结果或原因（可选）"},
					},
					"required": []string{"title", "status"},
				},
			},
		},
		"required":             []string{"steps"},
		"additionalProperties": false,
	},
}

func toolReply(ok bool, text string) map[string]any {
	return map[string]any{"success": ok, "contentItems": []any{map[string]any{"type": "inputText", "text": text}}}
}

func (e *Engine) onToolCall(r *Run, st *Step, params json.RawMessage) (any, error) {
	var p struct {
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return toolReply(false, "参数格式错误"), nil
	}
	if r.Kind == KindManager {
		return e.onManagerTool(r, p.Tool, p.Arguments)
	}
	if p.Tool != "report_progress" {
		return toolReply(false, "AgentHost 不认识这个工具："+p.Tool), nil
	}
	var a struct {
		Steps []struct {
			Title  string `json:"title"`
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(p.Arguments, &a); err != nil || len(a.Steps) == 0 {
		return toolReply(false, "需要传入 steps：[{title, status, note?}]"), nil
	}
	plan := make([]PlanEntry, 0, len(a.Steps))
	running, done := "", 0
	for _, s := range a.Steps {
		status := normStatus(s.Status)
		switch status {
		case "pending", "running", "completed", "failed", "skipped":
		default:
			status = "pending"
		}
		plan = append(plan, PlanEntry{Step: strings.TrimSpace(s.Title), Status: status, Note: strings.TrimSpace(s.Note)})
		if status == "running" && running == "" {
			running = s.Title
		}
		if status == "completed" || status == "failed" || status == "skipped" {
			done++
		}
	}
	e.mu.Lock()
	st.Plan = plan
	allDone := done == len(plan)
	item := findItem(st, "progress")
	if item == nil {
		item = &Item{ID: "progress", Kind: ItemPlan, Title: "执行步骤", StartedAt: now()}
	}
	cp := *item
	cp.Plan = plan
	cp.Status = "running"
	if allDone {
		cp.Status = "completed"
	}
	e.upsertItem(r, st, &cp)
	if running != "" {
		e.setStepActivity(r, st, fmt.Sprintf("步骤 %d/%d：%s", done+1, len(plan), running))
	} else {
		e.touchRun(r)
	}
	e.mu.Unlock()
	return toolReply(true, "已更新"), nil
}

func (e *Engine) onCodexRequest(r *Run, st *Step, method string, params json.RawMessage) (any, error) {
	accept := e.cfg.ApprovalDecision == "accept"
	note := func(title string) {
		var p struct {
			Command any    `json:"command"`
			Reason  string `json:"reason"`
		}
		_ = json.Unmarshal(params, &p)
		text := p.Reason
		if p.Command != nil {
			text = strings.TrimSpace(fmt.Sprint(p.Command) + "\n" + text)
		}
		e.mu.Lock()
		e.addItemLocked(r, st, &Item{ID: newID("approval"), Kind: ItemNotice, Status: "completed", Title: title, Text: text, StartedAt: now()})
		e.mu.Unlock()
	}
	verdict := "已自动拒绝"
	if accept {
		verdict = "已自动批准"
	}
	switch method {
	case "item/tool/call":
		return e.onToolCall(r, st, params)
	case "item/commandExecution/requestApproval":
		note("Codex 请求执行命令的审批，" + verdict)
		if accept {
			return map[string]any{"decision": "accept"}, nil
		}
		return map[string]any{"decision": "decline"}, nil
	case "item/fileChange/requestApproval":
		note("Codex 请求修改文件的审批，" + verdict)
		if accept {
			return map[string]any{"decision": "accept"}, nil
		}
		return map[string]any{"decision": "decline"}, nil
	case "execCommandApproval", "applyPatchApproval":
		note("Codex 请求审批（" + method + "），" + verdict)
		if accept {
			return map[string]any{"decision": "approved"}, nil
		}
		return map[string]any{"decision": "denied"}, nil
	}
	e.logf(r.ID, st.ID, "host", "warn", "收到不支持的 Codex 请求 %s，已拒绝", method)
	return nil, fmt.Errorf("AgentHost does not support %s", method)
}
