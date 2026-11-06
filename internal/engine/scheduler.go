package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agenthost/internal/workflow"
)

// execRun drives one run from queued to a terminal state.
func (e *Engine) execRun(r *Run, rc *runCtl) {
	defer close(rc.done)
	defer e.st.closeLog(r.ID)

	// Manager replies never wait for a slot: they are how you check on a busy server.
	if rc.topLvl && r.Kind != KindManager {
		e.logf(r.ID, "", "host", "info", "任务已排队，等待执行槽位（最多同时运行 %d 个任务）", e.cfg.MaxConcurrentRuns)
		select {
		case e.slots <- struct{}{}:
			defer func() { <-e.slots }()
		case <-rc.ctx.Done():
			e.finishRun(r, rc)
			return
		}
	}

	if nd, err := e.node(r.NodeID); err != nil {
		e.failRunEarly(r, rc, err.Error())
		return
	} else if err := nd.MkdirAll(r.Cwd); err != nil {
		e.failRunEarly(r, rc, fmt.Sprintf("无法在服务器「%s」上创建工作目录 %s：%v", nd.Info().Name, r.Cwd, err))
		return
	}

	e.mu.Lock()
	r.StartedAt = now()
	if !rc.paused {
		r.Status = StatusRunning
	}
	e.touchRun(r)
	e.touchSession(e.sessions[r.SessionID])
	e.mu.Unlock()
	if r.Kind == KindWorkflow {
		e.logf(r.ID, "", "host", "info", "开始执行 workflow「%s」，共 %d 个步骤，工作目录 %s", r.Title, len(r.Steps), r.Cwd)
	} else if r.Kind == KindManager {
		e.logf(r.ID, "", "host", "info", "管家开始回答（关于任务 %s）", r.Target)
	} else {
		e.logf(r.ID, "", "host", "info", "开始执行对话任务，工作目录 %s", r.Cwd)
	}

	maxPar := 1
	if r.Workflow != nil && r.Workflow.MaxParallel > 0 {
		maxPar = r.Workflow.MaxParallel
	}

	for {
		e.mu.Lock()
		stopping := rc.ctx.Err() != nil
		running, changed := 0, false
		for _, st := range r.Steps {
			if st.Status == StatusRunning || st.Status == StatusPaused {
				running++
			}
		}
		byID := map[string]*Step{}
		for _, st := range r.Steps {
			byID[st.ID] = st
		}
		for _, st := range r.Steps {
			if st.Status != StatusPending {
				continue
			}
			if stopping {
				st.Status = StatusStopped
				st.Error = "任务已停止"
				changed = true
				continue
			}
			ready, run, reason := evalDeps(st, byID)
			if !ready {
				continue
			}
			if !run {
				st.Status = StatusSkipped
				st.Error = reason
				st.EndedAt = now()
				changed = true
				continue
			}
			if rc.paused || running >= maxPar {
				continue
			}
			running++
			changed = true
			st.Status = StatusRunning
			st.StartedAt = now()
			ctl := &stepCtl{pauseCh: make(chan struct{}, 1), resumeCh: make(chan struct{}, 1)}
			rc.steps[st.ID] = ctl
			go e.execStep(r, rc, st, ctl)
		}
		allDone := true
		for _, st := range r.Steps {
			if !st.Status.Terminal() {
				allDone = false
			}
		}
		if changed {
			r.Activity = e.runActivity(r)
			e.touchRun(r)
			e.publishSessionOf(r)
		}
		e.mu.Unlock()
		if allDone {
			break
		}
		if changed {
			continue
		}
		<-rc.wake
	}
	e.finishRun(r, rc)
}

// failRunEarly ends a run that could not start at all.
func (e *Engine) failRunEarly(r *Run, rc *runCtl, msg string) {
	e.logf(r.ID, "", "host", "error", "%s", msg)
	e.mu.Lock()
	for _, st := range r.Steps {
		if st.Status == StatusPending {
			st.Status = StatusSkipped
		}
	}
	r.Status, r.Error, r.EndedAt, r.Activity = StatusFailed, msg, now(), ""
	delete(e.ctls, r.ID)
	e.touchRun(r)
	e.touchSession(e.sessions[r.SessionID])
	e.mu.Unlock()
	e.Flush()
}

// evalDeps reports whether all dependencies are finished and, if so,
// whether the step's condition says it should run.
func evalDeps(st *Step, byID map[string]*Step) (ready, run bool, reason string) {
	allOK, anyFailed := true, false
	for _, d := range st.DependsOn {
		dep := byID[d]
		if dep == nil {
			continue
		}
		if !dep.Status.Terminal() {
			return false, false, ""
		}
		switch dep.Status {
		case StatusCompleted:
		case StatusFailed:
			if !dep.ContinueOnError {
				anyFailed = true
				allOK = false
			}
		default:
			allOK = false
		}
	}
	switch st.If {
	case workflow.IfAlways:
		return true, true, ""
	case workflow.IfFailure:
		if anyFailed {
			return true, true, ""
		}
		return true, false, "跳过：仅在依赖步骤失败时执行"
	default:
		if allOK {
			return true, true, ""
		}
		return true, false, "跳过：依赖步骤未成功完成"
	}
}

func (e *Engine) finishRun(r *Run, rc *runCtl) {
	e.mu.Lock()
	failed, stopped := false, rc.ctx.Err() != nil && rc.stopped
	if rc.ctx.Err() != nil {
		stopped = true
	}
	var firstErr string
	for _, st := range r.Steps {
		if st.Status == StatusPending {
			st.Status = StatusStopped
		}
		if st.Status == StatusFailed && !st.ContinueOnError {
			failed = true
			if firstErr == "" {
				firstErr = fmt.Sprintf("步骤「%s」失败：%s", st.Title, st.Error)
			}
		}
		if st.Status == StatusStopped {
			stopped = true
		}
	}
	switch {
	case stopped:
		r.Status = StatusStopped
		r.Error = "任务已被停止"
	case failed:
		r.Status = StatusFailed
		r.Error = firstErr
	default:
		r.Status = StatusCompleted
	}
	// Final result: output of the last step (document order) that produced one.
	for i := len(r.Steps) - 1; i >= 0; i-- {
		st := r.Steps[i]
		if st.Status == StatusCompleted && strings.TrimSpace(st.Output) != "" {
			r.Output = st.Output
			break
		}
	}
	r.EndedAt = now()
	r.Activity = ""
	// Follow-up chat continues the Codex thread of the most recent agent step.
	if s := e.sessions[r.SessionID]; s != nil && r.ParentRunID == "" {
		var last *Step
		for _, st := range r.Steps {
			if st.Type == workflow.TypeAgent && st.ThreadID != "" && st.StartedAt != nil {
				if last == nil || st.StartedAt.After(*last.StartedAt) {
					last = st
				}
			}
		}
		if last != nil {
			if r.Kind == KindManager {
				s.ManagerThreadID = last.ThreadID
			} else {
				s.ThreadID = last.ThreadID
			}
		}
		e.touchSession(s)
	}
	e.touchRun(r)
	delete(e.ctls, r.ID)
	status, rerr := r.Status, r.Error
	e.mu.Unlock()
	e.logf(r.ID, "", "host", "info", "任务结束：%s %s", status, rerr)
	e.Flush()
}

func (e *Engine) runActivity(r *Run) string {
	if rc := e.ctls[r.ID]; rc != nil && rc.paused {
		return "已暂停"
	}
	var parts []string
	for _, st := range r.Steps {
		if st.Status == StatusRunning || st.Status == StatusPaused {
			a := st.Activity
			if a == "" {
				a = "执行中"
			}
			if r.Kind == KindWorkflow {
				a = st.Title + " · " + a
			}
			parts = append(parts, a)
		}
	}
	return truncate(strings.Join(parts, "；"), 300)
}

// setStepActivity updates "what is happening now". Call with e.mu held.
func (e *Engine) setStepActivity(r *Run, st *Step, a string) {
	if a == "" || st.Activity == a {
		return
	}
	st.Activity = a
	r.Activity = e.runActivity(r)
	e.touchRun(r)
	e.publishSessionOf(r)
}

// publishSessionOf pushes the sidebar row (status + activity) of a run's session. Call with e.mu held.
func (e *Engine) publishSessionOf(r *Run) {
	if s := e.sessions[r.SessionID]; s != nil && r.ParentRunID == "" {
		e.hub.Publish("session", e.sessionInfo(s))
	}
}

func (e *Engine) execStep(r *Run, rc *runCtl, st *Step, ctl *stepCtl) {
	defer e.wake(rc)
	e.logf(r.ID, st.ID, "host", "info", "开始步骤「%s」（%s）", st.Title, st.Type)
	var out string
	var err error
	for attempt := 1; attempt <= st.MaxAttempts; attempt++ {
		e.mu.Lock()
		st.Attempt = attempt
		st.Error = ""
		if attempt > 1 {
			st.Status = StatusRunning
			e.addItemLocked(r, st, &Item{ID: fmt.Sprintf("retry-%d", attempt), Kind: ItemNotice, Status: "completed",
				Title: fmt.Sprintf("第 %d 次尝试（共 %d 次）", attempt, st.MaxAttempts), Text: "上一次失败：" + truncate(errText(err), 500), StartedAt: now()})
		}
		e.touchRun(r)
		e.mu.Unlock()

		ctx := rc.ctx
		var cancel context.CancelFunc = func() {}
		if st.def != nil && st.def.Timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, st.def.Timeout)
		}
		switch st.Type {
		case workflow.TypeShell:
			out, err = e.runShell(ctx, r, rc, st, ctl)
		case workflow.TypeWorkflow:
			out, err = e.runSubWorkflow(ctx, r, rc, st, ctl)
		default:
			out, err = e.runAgent(ctx, r, rc, st, ctl, attempt, err)
		}
		timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded) && rc.ctx.Err() == nil
		cancel()
		if timedOut {
			err = fmt.Errorf("超时（%s）", st.def.Timeout)
		}
		if err == nil || rc.ctx.Err() != nil {
			break
		}
		e.logf(r.ID, st.ID, "host", "warn", "步骤「%s」第 %d 次执行失败：%v", st.Title, attempt, err)
	}

	e.mu.Lock()
	st.EndedAt = now()
	st.Activity = ""
	switch {
	case rc.ctx.Err() != nil:
		st.Status = StatusStopped
		st.Error = "已停止"
	case err != nil:
		st.Status = StatusFailed
		st.Error = err.Error()
		st.Output = out // partial output (e.g. stdout of a failed command) is still useful downstream
	default:
		st.Status = StatusCompleted
		st.Output = out
	}
	for _, it := range st.Items {
		if it.Status == "running" {
			if st.Status == StatusCompleted {
				it.Status = "completed"
			} else {
				it.Status = "stopped"
			}
			e.hub.Publish("item", itemEvent(r, st, it))
		}
	}
	delete(rc.steps, st.ID)
	r.Activity = e.runActivity(r)
	e.touchRun(r)
	status, serr := st.Status, st.Error
	e.mu.Unlock()
	if serr != "" {
		e.logf(r.ID, st.ID, "host", "info", "步骤「%s」结束：%s（%s）", st.Title, status, serr)
	} else {
		e.logf(r.ID, st.ID, "host", "info", "步骤「%s」结束：%s", st.Title, status)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---------------------------------------------------------------- items (call with e.mu held)

type itemPayload struct {
	RunID  string `json:"runId"`
	StepID string `json:"stepId"`
	Item   *Item  `json:"item"`
}

func itemEvent(r *Run, st *Step, it *Item) *itemPayload {
	return &itemPayload{RunID: r.ID, StepID: st.ID, Item: it}
}

func findItem(st *Step, id string) *Item {
	for i := len(st.Items) - 1; i >= 0; i-- {
		if st.Items[i].ID == id {
			return st.Items[i]
		}
	}
	return nil
}

const maxOutput = 512 * 1024

func (e *Engine) addItemLocked(r *Run, st *Step, it *Item) {
	st.Items = append(st.Items, it)
	e.dirtyRuns[r.ID] = true
	e.hub.Publish("item", itemEvent(r, st, it))
}

// upsertItem merges an item snapshot into the step timeline.
func (e *Engine) upsertItem(r *Run, st *Step, it *Item) {
	old := findItem(st, it.ID)
	if old == nil {
		if it.StartedAt == nil {
			it.StartedAt = now()
		}
		e.addItemLocked(r, st, it)
	} else {
		// Keep streamed content if the snapshot has none yet.
		if it.Text == "" {
			it.Text = old.Text
		}
		if it.Output == "" {
			it.Output = old.Output
		}
		if it.StartedAt == nil {
			it.StartedAt = old.StartedAt
		}
		if it.Kind == ItemPlan && it.Plan == nil {
			it.Plan = old.Plan
		}
		*old = *it
		it = old
		e.dirtyRuns[r.ID] = true
		e.hub.Publish("item", itemEvent(r, st, it))
	}
	it.Output = keepTail(it.Output, maxOutput)
	if it.Status == "running" {
		e.setStepActivity(r, st, activityFor(it))
	}
}

type deltaPayload struct {
	RunID  string `json:"runId"`
	StepID string `json:"stepId"`
	ItemID string `json:"itemId"`
	Field  string `json:"field"` // text | output | stdout | stderr
	Delta  string `json:"delta"`
}

// appendDelta streams text into an item field, creating the item if needed.
func (e *Engine) appendDelta(r *Run, st *Step, itemID, kind, field, delta string) {
	it := findItem(st, itemID)
	if it == nil {
		it = &Item{ID: itemID, Kind: kind, Status: "running", StartedAt: now()}
		e.addItemLocked(r, st, it)
	}
	switch field {
	case "text":
		it.Text += delta
	case "output":
		it.Output = keepTail(it.Output+delta, maxOutput)
	case "stdout":
		it.Stdout = keepTail(it.Stdout+delta, maxOutput)
		it.Output = keepTail(it.Output+delta, maxOutput)
	case "stderr":
		it.Stderr = keepTail(it.Stderr+delta, maxOutput)
		it.Output = keepTail(it.Output+delta, maxOutput)
	}
	e.dirtyRuns[r.ID] = true
	e.hub.Publish("delta", &deltaPayload{RunID: r.ID, StepID: st.ID, ItemID: itemID, Field: field, Delta: delta})
	if it.Status == "running" {
		e.setStepActivity(r, st, activityFor(it))
	}
}

// waitPausable blocks until resumed or cancelled. Used when a step must
// stay paused without a running process.
func waitResume(ctx context.Context, ctl *stepCtl) error {
	select {
	case <-ctl.resumeCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
