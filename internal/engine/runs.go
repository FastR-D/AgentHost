package engine

import (
	"errors"
	"log/slog"
	"time"
)

// Run records grow without end; these let the user page through them,
// delete them one by one, clean up old ones, or have that done automatically.

// RunStats counts a server's task runs by status, for the filter tabs.
type RunStats struct {
	Total  int            `json:"total"`
	Active int            `json:"active"`
	Counts map[string]int `json:"counts"`
}

// isTaskRun is a run the user started (not a manager reply).
func isTaskRun(r *Run) bool { return r.Kind != KindManager }

func (e *Engine) RunStats(nodeID string) RunStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := RunStats{Counts: map[string]int{}}
	for _, r := range e.runs {
		if !isTaskRun(r) || (nodeID != "" && r.NodeID != nodeID) {
			continue
		}
		st.Total++
		st.Counts[string(r.Status)]++
		if r.Status.Active() {
			st.Active++
		}
	}
	return st
}

// DeleteRun removes a finished task run with everything that belongs to it:
// the manager's answers about it and its logs. A session
// left without runs goes too.
func (e *Engine) DeleteRun(id string) error {
	e.mu.Lock()
	r := e.runs[id]
	switch {
	case r == nil:
		e.mu.Unlock()
		return ErrNotFound
	case r.Status.Active():
		e.mu.Unlock()
		return errors.New("任务还在运行，先停止它再删除")
	}
	runs, sessions := e.removeRunsLocked([]*Run{r})
	e.mu.Unlock()
	e.removeFiles(runs, sessions)
	return nil
}

// CleanupRuns deletes finished task runs on a server (all servers when
// nodeID is empty) that ended more than olderThan ago; 0 means every
// finished run. With dryRun it only counts them.
func (e *Engine) CleanupRuns(nodeID string, olderThan time.Duration, dryRun bool) int {
	cutoff := time.Now().Add(-olderThan)
	e.mu.Lock()
	var pick []*Run
	for _, r := range e.runs {
		if !isTaskRun(r) || !r.Status.Terminal() || (nodeID != "" && r.NodeID != nodeID) {
			continue
		}
		end := r.CreatedAt
		if r.EndedAt != nil {
			end = *r.EndedAt
		}
		if olderThan > 0 && end.After(cutoff) {
			continue
		}
		pick = append(pick, r)
	}
	if dryRun || len(pick) == 0 {
		e.mu.Unlock()
		return len(pick)
	}
	runs, sessions := e.removeRunsLocked(pick)
	e.mu.Unlock()
	e.removeFiles(runs, sessions)
	return len(pick)
}

// removeRunsLocked takes runs (and what belongs to them) out of memory and
// tells the UI. It returns the ids whose files must be deleted. Call with e.mu held.
func (e *Engine) removeRunsLocked(top []*Run) (runIDs, sessionIDs []string) {
	gone := map[string]bool{}
	for _, r := range top {
		gone[r.ID] = true
	}
	// The manager's answers about these runs.
	for _, r := range e.runs {
		if !gone[r.ID] && !r.Status.Active() && r.Kind == KindManager && gone[r.Target] {
			gone[r.ID] = true
		}
	}
	touched := map[string]bool{}
	for id := range gone {
		r := e.runs[id]
		touched[r.SessionID] = true
		delete(e.runs, id)
		delete(e.dirtyRuns, id)
		runIDs = append(runIDs, id)
		e.hub.Publish("run.deleted", map[string]string{"id": id, "sessionId": r.SessionID})
	}
	for sid := range touched {
		s := e.sessions[sid]
		if s == nil {
			continue
		}
		kept := s.RunIDs[:0]
		for _, id := range s.RunIDs {
			if !gone[id] {
				kept = append(kept, id)
			}
		}
		s.RunIDs = kept
		if len(kept) == 0 {
			delete(e.sessions, sid)
			delete(e.dirtySessions, sid)
			sessionIDs = append(sessionIDs, sid)
			e.hub.Publish("session.deleted", map[string]string{"id": sid})
			continue
		}
		e.touchSession(s)
	}
	return runIDs, sessionIDs
}

func (e *Engine) removeFiles(runIDs, sessionIDs []string) {
	for _, id := range runIDs {
		e.st.remove("runs", id)
	}
	for _, id := range sessionIDs {
		e.st.remove("sessions", id)
	}
}

// retentionLoop deletes finished runs older than the configured number of days.
func (e *Engine) retentionLoop() {
	t := time.NewTimer(time.Minute)
	for range t.C {
		if days := e.settings.get().Runs.RetentionDays; days > 0 {
			if n := e.CleanupRuns("", time.Duration(days)*24*time.Hour, false); n > 0 {
				slog.Info("deleted old run records", "count", n, "olderThanDays", days)
			}
		}
		t.Reset(time.Hour)
	}
}
