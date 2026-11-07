package engine

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusStopped   Status = "stopped"
	StatusSkipped   Status = "skipped"
)

func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusStopped, StatusSkipped:
		return true
	}
	return false
}

func (s Status) Active() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusPaused:
		return true
	}
	return false
}

// Session is one conversation in the sidebar. It owns a sequence of runs.
type Session struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Cwd      string `json:"cwd"`
	NodeID   string `json:"nodeId"`             // server the session's tasks run on
	ThreadID string `json:"threadId,omitempty"` // Codex thread used for follow-up chat
	// The manager's own Codex thread: answers questions about the running task.
	ManagerThreadID string    `json:"managerThreadId,omitempty"`
	RunIDs          []string  `json:"runIds"` // top-level runs, in order
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

const (
	KindChat = "chat"
	// KindManager is a message to the manager while a task runs: a separate
	// read-only Codex that reports progress and passes instructions on.
	KindManager = "manager"
)

// Run is one task: the text (a message, or a task's Markdown) handed to one
// Codex, which does the work. AgentHost does not plan or orchestrate it.
type Run struct {
	ID        string     `json:"id"`
	SessionID string     `json:"sessionId"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Prompt    string     `json:"prompt"`
	Task      string     `json:"task,omitempty"`      // task from the library this run follows
	TaskFile  string     `json:"taskFile,omitempty"`  // or the Markdown file on the server it follows
	TaskTitle string     `json:"taskTitle,omitempty"` // the task's name, for display
	Note      string     `json:"note,omitempty"`      // what the user added to the task
	Target    string     `json:"target,omitempty"`    // manager runs: the task run asked about
	Parallel  int        `json:"parallel,omitempty"`  // Codex working on the task at once (sub-agents); 0/1 = one
	Cwd       string     `json:"cwd"`
	NodeID    string     `json:"nodeId"`
	Model     string     `json:"model,omitempty"`
	Sandbox   string     `json:"sandbox,omitempty"`
	Status    Status     `json:"status"`
	Steps     []*Step    `json:"steps"` // always one: the Codex doing the work
	Output    string     `json:"output,omitempty"`
	Error     string     `json:"error,omitempty"`
	Activity  string     `json:"activity,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	RetryOf   string     `json:"retryOf,omitempty"`
}

// Process describes an OS process AgentHost launched for a step.
type Process struct {
	Node      string     `json:"node,omitempty"` // server name
	Argv      []string   `json:"argv"`
	Cwd       string     `json:"cwd"`
	Pid       int        `json:"pid"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Step is the Codex working on a run: its thread, processes and timeline.
type Step struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Status    Status          `json:"status"`
	Cwd       string          `json:"cwd"`
	Prompt    string          `json:"prompt,omitempty"` // prompt as sent to Codex
	ThreadID  string          `json:"threadId,omitempty"`
	TurnID    string          `json:"turnId,omitempty"`
	Processes []*Process      `json:"processes,omitempty"`
	Items     []*Item         `json:"items"`
	Output    string          `json:"output,omitempty"`
	Error     string          `json:"error,omitempty"`
	Diff      string          `json:"diff,omitempty"`
	Plan      []PlanEntry     `json:"plan,omitempty"`   // steps Codex reported with report_progress
	Agents    []*SubAgent     `json:"agents,omitempty"` // sub-Codex it started
	Usage     json.RawMessage `json:"usage,omitempty"`
	Activity  string          `json:"activity,omitempty"`
	StartedAt *time.Time      `json:"startedAt,omitempty"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
}

// codexStep is the single step of a new run.
func codexStep(id, title, cwd, prompt string) []*Step {
	return []*Step{{ID: id, Title: title, Status: StatusPending, Cwd: cwd, Prompt: prompt, Items: []*Item{}}}
}

// SubAgent is a Codex sub-agent (its own thread) working on part of a step.
type SubAgent struct {
	ThreadID  string     `json:"threadId"`
	Name      string     `json:"name"`   // from the agent path, e.g. write_tests
	Status    string     `json:"status"` // running | completed | stopped
	Activity  string     `json:"activity,omitempty"`
	Tokens    int64      `json:"tokens,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
}

// Item kinds.
const (
	ItemMessage    = "message"
	ItemReasoning  = "reasoning"
	ItemCommand    = "command"
	ItemFileChange = "file_change"
	ItemTool       = "tool"
	ItemWebSearch  = "web_search"
	ItemPlan       = "plan"
	ItemAgent      = "agent"
	ItemError      = "error"
	ItemNotice     = "notice"
	ItemOther      = "other"
)

type FileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Diff string `json:"diff,omitempty"`
}

type PlanEntry struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// Item is one entry in a step's timeline.
type Item struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status,omitempty"` // running | completed | failed | declined
	Title       string          `json:"title,omitempty"`
	Phase       string          `json:"phase,omitempty"`
	Text        string          `json:"text,omitempty"`
	Command     string          `json:"command,omitempty"`
	Argv        []string        `json:"argv,omitempty"`
	Cwd         string          `json:"cwd,omitempty"`
	Output      string          `json:"output,omitempty"`
	Stdout      string          `json:"stdout,omitempty"`
	Stderr      string          `json:"stderr,omitempty"`
	ExitCode    *int            `json:"exitCode,omitempty"`
	DurationMs  int64           `json:"durationMs,omitempty"`
	Changes     []FileChange    `json:"changes,omitempty"`
	Plan        []PlanEntry     `json:"plan,omitempty"`
	Server      string          `json:"server,omitempty"`
	Tool        string          `json:"tool,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Result      string          `json:"result,omitempty"`
	ThreadID    string          `json:"threadId,omitempty"`    // set for items of a sub-agent's thread
	AgentThread string          `json:"agentThread,omitempty"` // sub-agent events: the thread of the sub-agent
	Raw         json.RawMessage `json:"raw,omitempty"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	EndedAt     *time.Time      `json:"endedAt,omitempty"`
}

// LogEntry is one line in a run's execution log.
type LogEntry struct {
	Seq    int64     `json:"seq"`
	Time   time.Time `json:"time"`
	RunID  string    `json:"runId"`
	StepID string    `json:"stepId,omitempty"`
	Source string    `json:"source"` // host | codex | rpc | stdout | stderr
	Level  string    `json:"level,omitempty"`
	Text   string    `json:"text"`
}

// RunSummary is a run without step timelines, used in lists and events.
type RunSummary struct {
	Run
	Steps    []*StepSummary `json:"steps"`
	Progress Progress       `json:"progress"`
}

type StepSummary struct {
	Step
	Items     []*Item `json:"items,omitempty"`
	ItemCount int     `json:"itemCount"`
}

type Progress struct {
	Done    int `json:"done"`
	Total   int `json:"total"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
}

func now() *time.Time {
	t := time.Now()
	return &t
}
