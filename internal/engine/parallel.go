package engine

import (
	"encoding/json"
	"fmt"
)

// A task can ask for several Codex at once: the agent splits the work and
// hands parts to Codex sub-agents (spawn_agent), which run side by side in
// the same app-server process, each on its own thread. AgentHost limits how
// many run at once and shows each one's progress.

const maxParallel = 8

func clampParallel(n int) int {
	switch {
	case n <= 1:
		return 0
	case n > maxParallel:
		return maxParallel
	}
	return n
}

func parallelHint(n int) string {
	return fmt.Sprintf(`【AgentHost】这个任务可以多个 Codex 同时做：最多同时派 %d 个子 agent（spawn_agent）。
请先把任务拆成能独立完成、互不冲突的几部分（不要让两个子 agent 改同一个文件），交给子 agent 同时去做，子 agent 的名字（task_name）用简短的英文描述它负责的部分；你负责分派、等待（wait）、检查和汇总，最后给出整体结果。如果用了 report_progress，就用它列出你拆出的几部分。确实没法拆开的任务就自己做。`, n)
}

func (st *Step) agent(thread string) *SubAgent {
	for _, a := range st.Agents {
		if a.ThreadID == thread {
			return a
		}
	}
	return nil
}

func agentCounts(st *Step) (running, done int) {
	for _, a := range st.Agents {
		if a.Status == "running" {
			running++
		} else {
			done++
		}
	}
	return
}

// withAgents puts the sub-agents first in what the step is doing; the
// coordinating agent's own action follows.
func withAgents(st *Step, a string) string {
	if act := agentsActivity(st); act != "" && a != "" {
		return act + " · 主 Codex " + a
	}
	return a
}

func agentsActivity(st *Step) string {
	running, done := agentCounts(st)
	if running == 0 {
		return ""
	}
	s := fmt.Sprintf("%d 个子 Codex 并行中", running)
	if done > 0 {
		s += fmt.Sprintf("，%d 个已完成", done)
	}
	return s
}

// trackAgent records a sub-agent starting or finishing. Call with e.mu held.
func (e *Engine) trackAgent(r *Run, st *Step, it *Item) {
	a := st.agent(it.AgentThread)
	switch it.Tool {
	case "started":
		if a == nil {
			a = &SubAgent{ThreadID: it.AgentThread, Name: it.Title, StartedAt: it.StartedAt}
			if a.StartedAt == nil {
				a.StartedAt = now()
			}
			st.Agents = append(st.Agents, a)
		}
		a.Status, a.EndedAt = "running", nil
	case "completed":
		if a == nil {
			return
		}
		a.Status, a.EndedAt, a.Activity = "completed", now(), ""
	default:
		return
	}
	if act := agentsActivity(st); act != "" {
		e.setStepActivity(r, st, act)
	} else {
		e.touchRun(r)
	}
}

// agentDid notes what a sub-agent is doing. Call with e.mu held.
func (e *Engine) agentDid(r *Run, st *Step, thread string, it *Item) {
	a := st.agent(thread)
	if a == nil || it.Status != "running" {
		return
	}
	if act := activityFor(it); act != "" && act != a.Activity {
		a.Activity = act
		e.touchRun(r)
	}
}

// agentTokens records a sub-agent's token use. Call with e.mu held.
func (e *Engine) agentTokens(r *Run, st *Step, thread string, usage json.RawMessage) {
	a := st.agent(thread)
	if a == nil {
		return
	}
	var u struct {
		Total struct {
			TotalTokens int64 `json:"totalTokens"`
		} `json:"total"`
	}
	if json.Unmarshal(usage, &u) == nil && u.Total.TotalTokens != a.Tokens {
		a.Tokens = u.Total.TotalTokens
		e.dirtyRuns[r.ID] = true
	}
}
