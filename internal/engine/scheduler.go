package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// execRun drives one run from queued to a terminal state: it waits for a
// slot, then lets its Codex do the work.
func (e *Engine) execRun(r *Run, rc *runCtl) {
	defer close(rc.done)
	defer e.st.closeLog(r.ID)

	// Manager replies never wait for a slot: they are how you check on a busy server.
	if r.Kind != KindManager {
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
	st := r.Steps[0]
	r.StartedAt = now()
	if !rc.paused {
		r.Status = StatusRunning
	}
	st.Status, st.StartedAt = StatusRunning, now()
	ctl := &stepCtl{pauseCh: make(chan struct{}, 1), resumeCh: make(chan struct{}, 1)}
	rc.step = ctl
	r.Activity = e.runActivity(r)
	e.touchRun(r)
	e.touchSession(e.sessions[r.SessionID])
	e.mu.Unlock()
	if r.Kind == KindManager {
		e.logf(r.ID, "", "host", "info", "管家开始回答（关于任务 %s）", r.Target)
	} else {
		e.logf(r.ID, "", "host", "info", "开始执行任务，工作目录 %s", r.Cwd)
	}

	out, err := e.runAgent(rc.ctx, r, rc, st, ctl)

	e.mu.Lock()
	st.EndedAt = now()
	st.Activity = ""
	switch {
	case rc.shutdown:
		st.Status, st.Error = StatusInterrupted, "AgentHost 重启，任务被中断，可以点“继续”接着做"
	case rc.ctx.Err() != nil:
		st.Status, st.Error = StatusStopped, "已停止"
	case errors.Is(err, errInterrupted):
		st.Status, st.Error = StatusInterrupted, err.Error()
	case err != nil:
		st.Status, st.Error, st.Output = StatusFailed, err.Error(), out
	default:
		st.Status, st.Output = StatusCompleted, out
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
	for _, a := range st.Agents {
		if a.Status == "running" {
			a.Status, a.EndedAt, a.Activity = "stopped", st.EndedAt, ""
		}
	}
	rc.step = nil
	e.mu.Unlock()
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

func (e *Engine) finishRun(r *Run, rc *runCtl) {
	e.mu.Lock()
	st := r.Steps[0]
	if st.Status == StatusPending {
		st.Status = StatusStopped
	}
	switch {
	case st.Status == StatusInterrupted:
		r.Status, r.Error = StatusInterrupted, st.Error
	case rc.ctx.Err() != nil || st.Status == StatusStopped:
		r.Status, r.Error = StatusStopped, "任务已被停止"
	case st.Status == StatusFailed:
		r.Status, r.Error = StatusFailed, st.Error
	default:
		r.Status = StatusCompleted
	}
	if strings.TrimSpace(st.Output) != "" {
		r.Output = st.Output
	}
	r.EndedAt = now()
	r.Activity = ""
	// Follow-up chat continues this Codex thread.
	if s := e.sessions[r.SessionID]; s != nil {
		if st.ThreadID != "" {
			if r.Kind == KindManager {
				s.ManagerThreadID = st.ThreadID
			} else {
				s.ThreadID = st.ThreadID
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
	st := r.Steps[0]
	if st.Status != StatusRunning && st.Status != StatusPaused {
		return ""
	}
	if st.Activity == "" {
		return "执行中"
	}
	return truncate(st.Activity, 300)
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
	if s := e.sessions[r.SessionID]; s != nil {
		e.hub.Publish("session", e.sessionInfo(s))
	}
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
	if it.Status == "running" && it.ThreadID == "" {
		e.setStepActivity(r, st, withAgents(st, activityFor(it)))
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
	e.appendDeltaOn(r, st, "", itemID, kind, field, delta)
}

// appendDeltaOn is appendDelta for an item of a thread ("" = the step's own).
func (e *Engine) appendDeltaOn(r *Run, st *Step, thread, itemID, kind, field, delta string) {
	it := findItem(st, itemID)
	if it == nil {
		it = &Item{ID: itemID, Kind: kind, Status: "running", StartedAt: now(), ThreadID: thread}
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
	if it.Status == "running" && it.ThreadID == "" {
		e.setStepActivity(r, st, withAgents(st, activityFor(it)))
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
