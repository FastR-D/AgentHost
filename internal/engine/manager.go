package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agenthost/internal/workflow"
)

// While a task runs, the user's messages go to a manager: a separate,
// read-only Codex with its own thread in the session. It sees the task's
// current state and can pass instructions to the Codex doing the work
// (turn/steer), or pause, resume and stop it. The working Codex is not
// disturbed by questions.

const managerGuide = `你是 AgentHost 里的“任务管家”。同一个目录里，另一个 Codex（下面叫“干活的 Codex”）正在执行用户交给的任务。用户在任务进行中找你，一般是想知道进度，或者想调整做法。你不亲自做这个任务，也不修改任何文件。

- 问进度：根据每条消息附带的“任务现状”回答；需要最新情况时调用 get_task_status。必要时可以用只读命令看看文件、日志、git diff。先说结论：完成了几步、正在做什么、还剩什么、有没有遇到问题。简短，用中文。
- 要改做法、补充要求、提供信息：调用 send_to_task，把要求写成直接对干活的 Codex 说的话，然后告诉用户已经转告。
- 要暂停、继续、停止：调用 pause_task / resume_task / stop_task。用户没说清楚是否要停止时，先问一句再停。`

func toolDef(name, desc string, props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	if required == nil {
		required = []string{}
	}
	return map[string]any{
		"type": "function", "name": name, "description": desc,
		"inputSchema": map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false},
	}
}

var managerTools = []any{
	toolDef("get_task_status", "查看正在执行的任务的最新状态：执行步骤、正在做什么、最近的命令和输出。", nil),
	toolDef("send_to_task", "把一段话转告给干活的 Codex，它会立刻看到并按此调整。用于用户要改做法、补充要求或提供信息。",
		map[string]any{"message": map[string]any{"type": "string", "description": "直接对干活的 Codex 说的话"}}, "message"),
	toolDef("pause_task", "暂停任务（会中断干活的 Codex 当前的回合，继续时接着做）。", nil),
	toolDef("resume_task", "继续已暂停的任务。", nil),
	toolDef("stop_task", "停止任务。", nil),
}

// activeWorker is the session's running task, if any. Call with e.mu held.
func (e *Engine) activeWorker(s *Session) *Run {
	for i := len(s.RunIDs) - 1; i >= 0; i-- {
		if r := e.runs[s.RunIDs[i]]; r != nil && r.Kind != KindManager && r.Status.Active() {
			return r
		}
	}
	return nil
}

// askManager starts a manager reply about the session's running task.
// Call with e.mu held.
func (e *Engine) askManager(s *Session, target *Run, text string) (json.RawMessage, error) {
	for _, id := range s.RunIDs {
		if r := e.runs[id]; r != nil && r.Kind == KindManager && r.Status.Active() {
			return nil, errors.New("管家还在回答上一条消息，请稍等")
		}
	}
	r := &Run{
		ID: newID("r"), SessionID: s.ID, Kind: KindManager, Title: truncate(firstLine(text), 80), Prompt: text, Target: target.ID,
		Cwd: s.Cwd, NodeID: s.NodeID, Model: e.defaultModel(), Sandbox: "read-only", Status: StatusQueued, CreatedAt: time.Now(),
	}
	r.Steps = []*Step{{
		ID: "manager", Title: "管家", Type: workflow.TypeAgent, DependsOn: []string{}, If: workflow.IfSuccess,
		Status: StatusPending, MaxAttempts: 1, Cwd: s.Cwd, Prompt: text, Items: []*Item{},
	}}
	return e.launch(s, r, nil, 0)
}

// managerPrompt is the question with the task's current state. Call with e.mu held.
func (e *Engine) managerPrompt(r *Run, question string) string {
	var b strings.Builder
	b.WriteString("【任务现状（AgentHost 提供，" + time.Now().Format("15:04:05") + "）】\n")
	if t := e.runs[r.Target]; t != nil {
		b.WriteString(e.taskSnapshot(t))
	} else {
		b.WriteString("（找不到这个任务）\n")
	}
	b.WriteString("\n【用户的问题】\n" + question + "\n")
	return b.String()
}

func planMark(s string) string {
	switch s {
	case "completed":
		return "已完成"
	case "running":
		return "进行中"
	case "failed":
		return "失败"
	case "skipped":
		return "跳过"
	}
	return "未开始"
}

// taskSnapshot describes a run for the manager. Call with e.mu held.
func (e *Engine) taskSnapshot(r *Run) string {
	var b strings.Builder
	state := statusText(r.Status)
	switch r.Status {
	case StatusQueued:
		state = "排队中"
	case StatusRunning:
		state = "执行中"
	case StatusPaused:
		state = "已暂停"
	}
	fmt.Fprintf(&b, "任务：%s\n状态：%s", r.Title, state)
	if r.StartedAt != nil {
		end := time.Now()
		if r.EndedAt != nil {
			end = *r.EndedAt
		}
		fmt.Fprintf(&b, "，已用时 %s", end.Sub(*r.StartedAt).Round(time.Second))
	}
	b.WriteString("\n")
	if r.Activity != "" {
		fmt.Fprintf(&b, "正在做：%s\n", r.Activity)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "错误：%s\n", r.Error)
	}
	fmt.Fprintf(&b, "\n任务要求：\n%s\n", truncate(r.Prompt, 2000))
	commands, files := 0, map[string]bool{}
	for _, st := range r.Steps {
		if len(st.Plan) > 0 {
			b.WriteString("\n执行步骤（干活的 Codex 汇报的）：\n")
			for i, p := range st.Plan {
				fmt.Fprintf(&b, "%d. [%s] %s", i+1, planMark(p.Status), p.Step)
				if p.Note != "" {
					b.WriteString(" — " + p.Note)
				}
				b.WriteString("\n")
			}
		}
		for _, it := range st.Items {
			switch it.Kind {
			case ItemCommand:
				commands++
			case ItemFileChange:
				for _, c := range it.Changes {
					files[c.Path] = true
				}
			}
		}
	}
	fmt.Fprintf(&b, "\n到目前为止：执行了 %d 条命令，改动了 %d 个文件。\n", commands, len(files))
	// The latest events, oldest first.
	var recent []string
	for _, st := range r.Steps {
		for i := len(st.Items) - 1; i >= 0 && len(recent) < 15; i-- {
			if line := snapshotLine(st.Items[i]); line != "" {
				recent = append(recent, line)
			}
		}
	}
	if len(recent) > 0 {
		b.WriteString("\n最近的动作（从早到晚）：\n")
		for i := len(recent) - 1; i >= 0; i-- {
			b.WriteString("- " + recent[i] + "\n")
		}
	}
	if r.Output != "" {
		fmt.Fprintf(&b, "\n最终结果：\n%s\n", truncate(r.Output, 3000))
	}
	return b.String()
}

func snapshotLine(it *Item) string {
	switch it.Kind {
	case ItemCommand:
		line := "运行命令 " + truncate(it.Command, 200)
		switch {
		case it.Status == "running":
			line += "（还在运行）"
		case it.ExitCode != nil:
			line += fmt.Sprintf("（退出码 %d）", *it.ExitCode)
		}
		if out := strings.TrimSpace(it.Output); out != "" && (it.Status == "running" || (it.ExitCode != nil && *it.ExitCode != 0)) {
			line += "\n  输出末尾：" + strings.ReplaceAll(tail(out, 600), "\n", "\n  ")
		}
		return line
	case ItemMessage:
		if it.ThreadID == "" && strings.TrimSpace(it.Text) != "" {
			return "Codex 说：" + truncate(strings.TrimSpace(it.Text), 400)
		}
	case ItemFileChange:
		var paths []string
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
		}
		return "修改文件：" + truncate(strings.Join(paths, "、"), 300)
	case ItemError, ItemNotice:
		return it.Title + "：" + truncate(it.Text, 300)
	case ItemWebSearch:
		return "搜索网页：" + it.Text
	}
	return ""
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n:])
}

// onManagerTool runs one of the manager's tools against its task.
func (e *Engine) onManagerTool(r *Run, tool string, args json.RawMessage) (any, error) {
	e.mu.Lock()
	target := e.runs[r.Target]
	snap := ""
	if target != nil && tool == "get_task_status" {
		snap = e.taskSnapshot(target)
	}
	e.mu.Unlock()
	if target == nil {
		return toolReply(false, "找不到这个任务"), nil
	}
	var err error
	ok := "好的"
	switch tool {
	case "get_task_status":
		return toolReply(true, snap), nil
	case "send_to_task":
		var a struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(args, &a)
		if strings.TrimSpace(a.Message) == "" {
			return toolReply(false, "需要 message"), nil
		}
		err, ok = e.Steer(target.ID, a.Message), "已转告，干活的 Codex 会立刻看到"
	case "pause_task":
		err, ok = e.Pause(target.ID), "任务已暂停"
	case "resume_task":
		err, ok = e.Resume(target.ID), "任务已继续"
	case "stop_task":
		err, ok = e.Stop(target.ID), "任务已停止"
	default:
		return toolReply(false, "AgentHost 不认识这个工具："+tool), nil
	}
	if err != nil {
		return toolReply(false, err.Error()), nil
	}
	return toolReply(true, ok), nil
}

// Steer adds the user's words to the turn a task's Codex is working on.
func (e *Engine) Steer(runID, text string) error {
	text = strings.TrimSpace(text)
	e.mu.Lock()
	r, rc := e.runs[runID], e.ctls[runID]
	if r == nil {
		e.mu.Unlock()
		return ErrNotFound
	}
	if rc == nil || !r.Status.Active() {
		e.mu.Unlock()
		return errors.New("任务已经结束了")
	}
	if rc.paused {
		e.mu.Unlock()
		return errors.New("任务已暂停：先继续任务，再转告")
	}
	var steer func(string) error
	var st *Step
	for _, s := range r.Steps {
		if c := rc.steps[s.ID]; c != nil && c.steer != nil {
			steer, st = c.steer, s
			break
		}
	}
	e.mu.Unlock()
	if steer == nil {
		return errors.New("干活的 Codex 正在启动或切换，过几秒再转告")
	}
	if err := steer("（用户在任务进行中通过 AgentHost 补充）\n" + text); err != nil {
		return fmt.Errorf("转告失败：%v", err)
	}
	e.mu.Lock()
	e.addItemLocked(r, st, &Item{ID: newID("steer"), Kind: ItemNotice, Status: "completed", Title: "管家转告", Text: text, StartedAt: now()})
	e.touchRun(r)
	e.mu.Unlock()
	e.logf(r.ID, st.ID, "host", "info", "管家转告 Codex：%s", text)
	return nil
}
