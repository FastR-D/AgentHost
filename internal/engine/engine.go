// Package engine schedules chat turns and Markdown workflows on top of the
// Codex CLI, records everything that happens, and broadcasts live updates.
package engine

import (
	"agenthost/internal/node"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agenthost/internal/workflow"
)

type Config struct {
	DataDir           string
	Workspace         string         // working directory root of the local node
	Nodes             *node.Registry // servers tasks can run on
	CodexBin          string         // codex executable on the local node
	CodexArgs         []string       // extra args before "app-server" (e.g. -c key=value)
	DefaultModel      string
	DefaultSandbox    string // read-only | workspace-write | danger-full-access
	ApprovalPolicy    string // never | on-request | untrusted
	ApprovalDecision  string // what to answer when codex asks for approval: accept | decline
	ReasoningSummary  string // auto | concise | detailed | none | "" (codex default)
	MaxConcurrentRuns int
	MaxDepth          int      // nesting limit for sub-workflows
	Tasks             *TaskLib // saved task descriptions
}

var (
	ErrNotFound = errors.New("not found")
	ErrBusy     = errors.New("会话中已有正在执行的任务，请先等待完成、暂停或停止")
	errPaused   = errors.New("paused")
)

type Engine struct {
	cfg Config
	lib *Library
	hub *Hub
	st  *store

	mu            sync.Mutex
	sessions      map[string]*Session
	runs          map[string]*Run
	ctls          map[string]*runCtl
	dirtyRuns     map[string]bool
	dirtySessions map[string]bool
	logSeq        int64
	slots         chan struct{}
	settings      *settingsStore
	tasks         *TaskLib
}

type runCtl struct {
	ctx     context.Context
	cancel  context.CancelFunc
	wake    chan struct{}
	done    chan struct{}
	steps   map[string]*stepCtl
	paused  bool
	depth   int
	topLvl  bool
	stopped bool
}

type stepCtl struct {
	pause    func()
	resume   func()
	steer    func(text string) error // adds input to the running Codex turn; nil between turns
	pauseCh  chan struct{}
	resumeCh chan struct{}
}

func New(cfg Config, lib *Library) (*Engine, error) {
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	if cfg.DefaultSandbox == "" {
		cfg.DefaultSandbox = "workspace-write"
	}
	if cfg.ApprovalPolicy == "" {
		cfg.ApprovalPolicy = "never"
	}
	if cfg.ApprovalDecision == "" {
		cfg.ApprovalDecision = "decline"
	}
	if cfg.MaxConcurrentRuns <= 0 {
		cfg.MaxConcurrentRuns = 4
	}
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 5
	}
	if cfg.Workspace != "" {
		if err := os.MkdirAll(cfg.Workspace, 0o755); err != nil {
			return nil, err
		}
	}
	if cfg.Nodes == nil {
		cfg.Nodes = node.NewRegistry(node.NewLocal(node.Info{Workspace: cfg.Workspace, CodexBin: cfg.CodexBin}), cfg.DataDir, "")
	}
	st, err := newStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		cfg:           cfg,
		lib:           lib,
		hub:           NewHub(),
		st:            st,
		sessions:      map[string]*Session{},
		runs:          map[string]*Run{},
		ctls:          map[string]*runCtl{},
		dirtyRuns:     map[string]bool{},
		dirtySessions: map[string]bool{},
		slots:         make(chan struct{}, cfg.MaxConcurrentRuns),
		settings:      loadSettings(cfg.DataDir),
		tasks:         cfg.Tasks,
	}
	if e.tasks == nil {
		lib, err := OpenTaskLib(filepath.Join(cfg.DataDir, "tasks"), nil, LegacyDirs{})
		if err != nil {
			return nil, err
		}
		e.tasks = lib
	}
	if err := e.load(); err != nil {
		return nil, err
	}
	go e.persistLoop()
	go e.retentionLoop()
	return e, nil
}

func (e *Engine) Hub() *Hub         { return e.hub }
func (e *Engine) Config() Config    { return e.cfg }
func (e *Engine) Library() *Library { return e.lib }
func (e *Engine) Tasks() *TaskLib   { return e.tasks }

func newID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + time.Now().UTC().Format("060102150405") + "-" + hex.EncodeToString(b)
}

// ---------------------------------------------------------------- loading & persistence

func (e *Engine) load() error {
	sessions, err := loadAll[Session](e.st, "sessions")
	if err != nil {
		return err
	}
	runs, err := loadAll[Run](e.st, "runs")
	if err != nil {
		return err
	}
	for _, s := range sessions {
		if s.RunIDs == nil {
			s.RunIDs = []string{}
		}
		if s.NodeID == "" {
			s.NodeID = node.LocalID
		}
		e.sessions[s.ID] = s
	}
	for _, r := range runs {
		if r.Status.Active() {
			// The process that ran it is gone; record that honestly.
			r.Status = StatusFailed
			r.Error = "AgentHost 重启，任务被中断（可点击“重新运行”）"
			r.EndedAt = now()
			r.Activity = ""
			for _, st := range r.Steps {
				if st.Status.Active() || st.Status == StatusPending {
					if st.Status == StatusPending {
						st.Status = StatusSkipped
					} else {
						st.Status = StatusFailed
						st.Error = "AgentHost 重启，步骤被中断"
						st.EndedAt = r.EndedAt
					}
					st.Activity = ""
				}
				for _, it := range st.Items {
					if it.Status == "running" {
						it.Status = "stopped"
					}
				}
			}
			e.dirtyRuns[r.ID] = true
		}
		for _, st := range r.Steps {
			if st.Items == nil {
				st.Items = []*Item{}
			}
		}
		e.runs[r.ID] = r
	}
	// Drop dangling references.
	for _, s := range e.sessions {
		kept := s.RunIDs[:0]
		for _, id := range s.RunIDs {
			if e.runs[id] != nil {
				kept = append(kept, id)
			}
		}
		s.RunIDs = kept
	}
	slog.Info("loaded state", "sessions", len(e.sessions), "runs", len(e.runs))
	return nil
}

func (e *Engine) persistLoop() {
	t := time.NewTicker(time.Second)
	for range t.C {
		e.Flush()
	}
}

// Flush writes dirty sessions and runs to disk.
func (e *Engine) Flush() {
	type rec struct {
		kind, id string
		data     []byte
	}
	var recs []rec
	e.mu.Lock()
	for id := range e.dirtyRuns {
		if r := e.runs[id]; r != nil {
			b, err := json.Marshal(r)
			if err == nil {
				recs = append(recs, rec{"runs", id, b})
			}
		}
		delete(e.dirtyRuns, id)
	}
	for id := range e.dirtySessions {
		if s := e.sessions[id]; s != nil {
			b, err := json.Marshal(s)
			if err == nil {
				recs = append(recs, rec{"sessions", id, b})
			}
		}
		delete(e.dirtySessions, id)
	}
	e.mu.Unlock()
	for _, r := range recs {
		if err := e.st.saveRaw(r.kind, r.id, r.data); err != nil {
			slog.Error("persist failed", "kind", r.kind, "id", r.id, "err", err)
		}
	}
}

// ---------------------------------------------------------------- events & views (call with e.mu held)

func (e *Engine) summary(r *Run) *RunSummary {
	s := &RunSummary{Run: *r}
	s.Source = ""
	s.Workflow = nil
	s.Steps = make([]*StepSummary, len(r.Steps))
	for i, st := range r.Steps {
		s.Steps[i] = &StepSummary{Step: *st, ItemCount: len(st.Items)}
		s.Steps[i].Prompt = truncate(st.Prompt, 4000)
		s.Steps[i].Output = truncate(st.Output, 4000)
		s.Steps[i].Diff = ""
	}
	s.Progress = progressOf(r)
	return s
}

func progressOf(r *Run) Progress {
	p := Progress{Total: len(r.Steps)}
	for _, st := range r.Steps {
		switch {
		case st.Status.Terminal():
			p.Done++
			if st.Status == StatusFailed {
				p.Failed++
			}
		case st.Status == StatusRunning || st.Status == StatusPaused:
			p.Running++
		}
	}
	return p
}

func (e *Engine) touchRun(r *Run) {
	e.dirtyRuns[r.ID] = true
	e.hub.Publish("run", e.summary(r))
}

func (e *Engine) touchSession(s *Session) {
	if s == nil {
		return
	}
	s.UpdatedAt = time.Now()
	e.dirtySessions[s.ID] = true
	e.hub.Publish("session", e.sessionInfo(s))
}

func (e *Engine) logf(runID, stepID, source, level, format string, args ...any) {
	text := format
	if len(args) > 0 {
		text = fmt.Sprintf(format, args...)
	}
	e.mu.Lock()
	e.logSeq++
	entry := &LogEntry{Seq: e.logSeq, Time: time.Now(), RunID: runID, StepID: stepID, Source: source, Level: level, Text: text}
	e.hub.Publish("log", entry)
	e.mu.Unlock()
	e.st.appendLog(entry)
}

// ---------------------------------------------------------------- sessions

func (e *Engine) resolveDir(base, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return base
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// node returns a session's server, refusing ones that are not connected.
func (e *Engine) node(id string) (node.Node, error) {
	nd, err := e.cfg.Nodes.Get(id)
	if err != nil {
		return nil, err
	}
	if i := nd.Info(); !i.Online {
		return nil, fmt.Errorf("服务器「%s」不在线：请在那台机器上启动 agenthost node，或换一台服务器", i.Name)
	}
	return nd, nil
}

// workspaceOf is a node's working-directory root.
func workspaceOf(nd node.Node) string {
	if w := nd.Info().Workspace; w != "" {
		return w
	}
	return "/"
}

func (e *Engine) CreateSession(title, cwd, nodeID string) (*Session, error) {
	if nodeID == "" {
		nodeID = e.cfg.Nodes.Default()
	}
	nd, err := e.node(nodeID)
	if err != nil {
		return nil, err
	}
	dir := e.resolveDir(workspaceOf(nd), cwd)
	if err := nd.MkdirAll(dir); err != nil {
		return nil, fmt.Errorf("无法创建工作目录 %s: %w", dir, err)
	}
	if strings.TrimSpace(title) == "" {
		title = "新会话"
	}
	s := &Session{ID: newID("s"), Title: title, Cwd: dir, NodeID: nd.Info().ID, RunIDs: []string{}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	e.mu.Lock()
	e.sessions[s.ID] = s
	e.touchSession(s)
	e.mu.Unlock()
	return s, nil
}

func (e *Engine) UpdateSession(id string, title, cwd, nodeID *string) (json.RawMessage, error) {
	e.mu.Lock()
	s := e.sessions[id]
	if s == nil {
		e.mu.Unlock()
		return nil, ErrNotFound
	}
	if (cwd != nil || nodeID != nil) && e.sessionBusy(s) {
		e.mu.Unlock()
		return nil, ErrBusy
	}
	curNode, curCwd := s.NodeID, s.Cwd
	e.mu.Unlock()

	newNode := curNode
	if nodeID != nil && *nodeID != "" {
		newNode = *nodeID
	}
	newCwd := curCwd
	if cwd != nil || newNode != curNode {
		nd, err := e.node(newNode)
		if err != nil {
			return nil, err
		}
		p := ""
		if cwd != nil {
			p = *cwd
		} else if newNode == curNode {
			p = curCwd
		}
		newCwd = e.resolveDir(workspaceOf(nd), p)
		if err := nd.MkdirAll(newCwd); err != nil {
			return nil, fmt.Errorf("无法创建工作目录 %s: %w", newCwd, err)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if title != nil && strings.TrimSpace(*title) != "" {
		s.Title = strings.TrimSpace(*title)
	}
	if newCwd != s.Cwd || newNode != s.NodeID {
		s.Cwd, s.NodeID = newCwd, newNode
		s.ThreadID = "" // another directory or server means a new Codex thread
	}
	e.touchSession(s)
	return json.Marshal(e.sessionInfo(s))
}

func (e *Engine) DeleteSession(id string) error {
	e.mu.Lock()
	s := e.sessions[id]
	if s == nil {
		e.mu.Unlock()
		return ErrNotFound
	}
	var runIDs []string
	var waits []chan struct{}
	for _, r := range e.runs {
		if r.SessionID == id {
			runIDs = append(runIDs, r.ID)
			if c := e.ctls[r.ID]; c != nil {
				c.stopped = true
				c.cancel()
				waits = append(waits, c.done)
			}
		}
	}
	e.mu.Unlock()
	for _, w := range waits {
		select {
		case <-w:
		case <-time.After(20 * time.Second):
		}
	}
	e.mu.Lock()
	delete(e.sessions, id)
	delete(e.dirtySessions, id)
	for _, rid := range runIDs {
		delete(e.runs, rid)
		delete(e.dirtyRuns, rid)
	}
	e.hub.Publish("session.deleted", map[string]string{"id": id})
	e.mu.Unlock()
	e.st.remove("sessions", id)
	for _, rid := range runIDs {
		e.st.remove("runs", rid)
	}
	return nil
}

type SessionInfo struct {
	*Session
	Status    Status `json:"status"` // status of the latest run, or "" when empty
	Activity  string `json:"activity,omitempty"`
	LastRunID string `json:"lastRunId,omitempty"`
}

func (e *Engine) sessionInfo(s *Session) *SessionInfo {
	info := &SessionInfo{Session: s}
	// The session shows its task, not the manager's replies.
	for i := len(s.RunIDs) - 1; i >= 0; i-- {
		if r := e.runs[s.RunIDs[i]]; r != nil && r.Kind != KindManager {
			info.Status = r.Status
			info.Activity = r.Activity
			info.LastRunID = r.ID
			break
		}
	}
	return info
}

func (e *Engine) ListSessions() json.RawMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := make([]*SessionInfo, 0, len(e.sessions))
	for _, s := range e.sessions {
		list = append(list, e.sessionInfo(s))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].UpdatedAt.After(list[j].UpdatedAt) })
	b, _ := json.Marshal(list)
	return b
}

// GetSession returns the session with every run (including sub-workflow runs) in full.
func (e *Engine) GetSession(id string) (json.RawMessage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[id]
	if s == nil {
		return nil, ErrNotFound
	}
	runs := []*RunSummary{}
	for _, r := range e.runs {
		if r.SessionID == id {
			rs := &RunSummary{Run: *r, Progress: progressOf(r)}
			rs.Steps = make([]*StepSummary, len(r.Steps))
			for i, st := range r.Steps {
				rs.Steps[i] = &StepSummary{Step: *st, Items: st.Items, ItemCount: len(st.Items)}
			}
			runs = append(runs, rs)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	return json.Marshal(map[string]any{"session": e.sessionInfo(s), "runs": runs})
}

// ---------------------------------------------------------------- runs: queries

type RunFilter struct {
	Status    string
	SessionID string
	NodeID    string
	Before    time.Time // only runs created before this (paging)
	TasksOnly bool      // leave out manager replies and sub-workflows
	Limit     int
}

func (e *Engine) ListRuns(f RunFilter) json.RawMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	list := []*RunSummary{}
	for _, r := range e.runs {
		if f.SessionID != "" && r.SessionID != f.SessionID {
			continue
		}
		if (f.NodeID != "" && r.NodeID != f.NodeID) || (f.TasksOnly && !isTaskRun(r)) || (!f.Before.IsZero() && !r.CreatedAt.Before(f.Before)) {
			continue
		}
		if f.Status != "" {
			if f.Status == "active" {
				if !r.Status.Active() {
					continue
				}
			} else if string(r.Status) != f.Status {
				continue
			}
		}
		list = append(list, e.summary(r))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	if f.Limit > 0 && len(list) > f.Limit {
		list = list[:f.Limit]
	}
	b, _ := json.Marshal(list)
	return b
}

func (e *Engine) GetRun(id string) (json.RawMessage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.runs[id]
	if r == nil {
		return nil, ErrNotFound
	}
	rs := &RunSummary{Run: *r, Progress: progressOf(r)}
	rs.Steps = make([]*StepSummary, len(r.Steps))
	for i, st := range r.Steps {
		rs.Steps[i] = &StepSummary{Step: *st, Items: st.Items, ItemCount: len(st.Items)}
	}
	return json.Marshal(rs)
}

func (e *Engine) Logs(runID string, after int64, limit int) ([]*LogEntry, error) {
	e.mu.Lock()
	_, ok := e.runs[runID]
	e.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return e.st.readLogs(runID, after, limit)
}

// ---------------------------------------------------------------- runs: creation

type ChatRequest struct {
	SessionID string `json:"sessionId"`
	Text      string `json:"text"`     // the message; with a task, extra instructions
	Task      string `json:"task"`     // run a task from the library
	TaskFile  string `json:"taskFile"` // or a Markdown file on the session's server
	Parallel  int    `json:"parallel"` // Codex at once; 0 = the task's own setting, else the session's last task
	Model     string `json:"model"`
	Sandbox   string `json:"sandbox"`
}

type WorkflowRequest struct {
	SessionID string            `json:"sessionId"`
	Name      string            `json:"name"`   // workflow file in the library
	Source    string            `json:"source"` // or inline markdown
	Cwd       string            `json:"cwd"`
	Vars      map[string]string `json:"vars"`
	Model     string            `json:"model"`
	Sandbox   string            `json:"sandbox"`
	Mode      string            `json:"mode"` // "" (auto) | steps | agent
}

// sessionBusy reports whether a task is running in the session (manager
// replies do not count).
func (e *Engine) sessionBusy(s *Session) bool {
	for _, id := range s.RunIDs {
		if r := e.runs[id]; r != nil && r.Kind != KindManager && r.Status.Active() {
			return true
		}
	}
	return false
}

// StartChat sends the user's message: it continues the task in the session,
// or, while a task is running, goes to the manager.
func (e *Engine) StartChat(req ChatRequest) (json.RawMessage, error) {
	if req.Task == "" && req.TaskFile == "" {
		e.mu.Lock()
		if s := e.sessions[req.SessionID]; s != nil {
			if w := e.activeWorker(s); w != nil {
				defer e.mu.Unlock()
				text := strings.TrimSpace(req.Text)
				if text == "" {
					return nil, errors.New("消息内容不能为空")
				}
				return e.askManager(s, w, text)
			}
		}
		e.mu.Unlock()
	}
	return e.startChat(req, "", false)
}

// taskSpec is what a task contributes to a run.
type taskSpec struct {
	name, file, title, body string
	parallel                int
}

// resolveTask loads the task a request names, if any. Call without e.mu.
func (e *Engine) resolveTask(req ChatRequest) (*taskSpec, error) {
	switch {
	case strings.TrimSpace(req.Task) != "":
		t, err := e.tasks.Get(req.Task)
		if err != nil {
			return nil, fmt.Errorf("找不到任务「%s」", req.Task)
		}
		return &taskSpec{name: t.Name, title: t.Title, body: t.Body, parallel: t.Parallel}, nil
	case strings.TrimSpace(req.TaskFile) != "":
		e.mu.Lock()
		s := e.sessions[req.SessionID]
		var nodeID, cwd string
		if s != nil {
			nodeID, cwd = s.NodeID, s.Cwd
		}
		e.mu.Unlock()
		if s == nil {
			return nil, ErrNotFound
		}
		nd, err := e.node(nodeID)
		if err != nil {
			return nil, err
		}
		p := e.resolveDir(cwd, req.TaskFile)
		f, err := nd.ReadFile(p, 1<<20)
		if err != nil {
			return nil, fmt.Errorf("无法读取任务文件 %s：%v", p, err)
		}
		if f.Truncated {
			return nil, errors.New("任务文件超过 1 MB，太大了")
		}
		body := strings.TrimSpace(strings.ToValidUTF8(string(f.Data), ""))
		if body == "" {
			return nil, fmt.Errorf("任务文件 %s 是空的", p)
		}
		base := filepath.Base(p)
		title := parseTask(base, f.Data).Title
		return &taskSpec{file: p, title: title, body: body}, nil
	}
	return nil, nil
}

// taskPrompt is a task's text with the user's extra instructions after it.
func taskPrompt(body, note string) string {
	if note == "" {
		return body
	}
	return body + "\n\n---\n\n补充说明：" + note
}

// startChat starts a chat run; title overrides the one taken from the text,
// network lets this run's agent commands use the network.
func (e *Engine) startChat(req ChatRequest, title string, network bool, writable ...string) (json.RawMessage, error) {
	note := strings.TrimSpace(req.Text)
	text := note
	ts, err := e.resolveTask(req)
	if err != nil {
		return nil, err
	}
	if ts != nil {
		text = taskPrompt(ts.body, note)
		if title == "" {
			title = ts.title
			if note != "" {
				title += "：" + truncate(firstLine(note), 60)
			}
		}
	}
	if text == "" {
		return nil, errors.New("消息内容不能为空")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[req.SessionID]
	if s == nil {
		return nil, ErrNotFound
	}
	if e.sessionBusy(s) {
		return nil, ErrBusy
	}
	r := &Run{
		ID: newID("r"), SessionID: s.ID, Kind: KindChat, Title: truncate(firstLine(text), 80), Prompt: text,
		Cwd: s.Cwd, NodeID: s.NodeID, Model: firstNonEmpty(req.Model, e.defaultModel()), Sandbox: firstNonEmpty(req.Sandbox, e.cfg.DefaultSandbox),
		Status: StatusQueued, CreatedAt: time.Now(), Network: network, WritableRoots: writable,
	}
	if ts != nil {
		r.Task, r.TaskFile, r.TaskTitle, r.Note = ts.name, ts.file, ts.title, note
	}
	r.Parallel = req.Parallel
	if r.Parallel == 0 && ts != nil {
		r.Parallel = ts.parallel
	}
	if r.Parallel == 0 && ts == nil {
		// A follow-up keeps working the way the task did.
		for i := len(s.RunIDs) - 1; i >= 0; i-- {
			if prev := e.runs[s.RunIDs[i]]; prev != nil && isTaskRun(prev) {
				r.Parallel = prev.Parallel
				break
			}
		}
	}
	r.Parallel = clampParallel(r.Parallel)
	if title != "" {
		r.Title = title
	}
	r.Steps = []*Step{{
		ID: "codex", Title: "Codex", Type: workflow.TypeAgent, DependsOn: []string{}, If: workflow.IfSuccess,
		Status: StatusPending, MaxAttempts: 1, Cwd: s.Cwd, Prompt: text, Items: []*Item{},
	}}
	if len(s.RunIDs) == 0 && (s.Title == "" || s.Title == "新会话") {
		s.Title = truncate(firstLine(r.Title), 60)
	}
	return e.launch(s, r, nil, 0)
}

func (e *Engine) StartWorkflow(req WorkflowRequest) (json.RawMessage, error) {
	source, file := req.Source, ""
	if strings.TrimSpace(source) == "" {
		if req.Name == "" {
			return nil, errors.New("需要提供 workflow 名称或 Markdown 内容")
		}
		src, err := e.lib.Read(req.Name)
		if err != nil {
			return nil, err
		}
		source, file = src, req.Name
	}
	wf, err := workflow.ParseMode(source, strings.TrimSuffix(file, ".md"), req.Mode)
	if err != nil {
		return nil, fmt.Errorf("workflow 解析失败: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.sessions[req.SessionID]
	if s == nil {
		return nil, ErrNotFound
	}
	if e.sessionBusy(s) {
		return nil, ErrBusy
	}
	base := s.Cwd
	cwd := e.resolveDir(base, wf.Cwd)
	if strings.TrimSpace(req.Cwd) != "" {
		nd, err := e.node(s.NodeID)
		if err != nil {
			return nil, err
		}
		cwd = e.resolveDir(workspaceOf(nd), req.Cwd)
	}
	r := e.newWorkflowRun(s, wf, source, file, cwd, req.Vars, req.Model, req.Sandbox)
	r.Mode = req.Mode
	if len(s.RunIDs) == 0 && (s.Title == "" || s.Title == "新会话") {
		s.Title = "Workflow: " + wf.Name
	}
	return e.launch(s, r, nil, 0)
}

func (e *Engine) newWorkflowRun(s *Session, wf *workflow.Workflow, source, file, cwd string, vars map[string]string, model, sandbox string) *Run {
	merged := map[string]string{}
	for k, v := range wf.Vars {
		merged[k] = v
	}
	for k, v := range vars {
		merged[k] = v
	}
	r := &Run{
		ID: newID("r"), SessionID: s.ID, NodeID: s.NodeID, Kind: KindWorkflow, Title: wf.Name, Source: source, WorkflowFile: file,
		Workflow: wf, Vars: merged, Cwd: cwd,
		Model:   firstNonEmpty(model, wf.Model, e.defaultModel()),
		Sandbox: firstNonEmpty(sandbox, wf.Sandbox, e.cfg.DefaultSandbox),
		Status:  StatusQueued, CreatedAt: time.Now(), Network: wf.Network,
	}
	r.Prompt = fmt.Sprintf("运行 workflow「%s」", wf.Name)
	if file != "" {
		r.Prompt += fmt.Sprintf("（%s）", file)
	}
	for _, d := range wf.Steps {
		r.Steps = append(r.Steps, &Step{
			ID: d.ID, Title: d.Title, Type: d.Type, DependsOn: d.DependsOn, If: d.If, ContinueOnError: d.ContinueOnError,
			Status: StatusPending, MaxAttempts: d.Retry + 1, Cwd: e.resolveDir(cwd, d.Cwd), Workflow: d.Workflow,
			Prompt: d.Prompt, Run: d.Run, Items: []*Item{}, def: d, Orchestrate: d.Orchestrate,
		})
	}
	return r
}

// launch registers a run and starts executing it. Call with e.mu held.
func (e *Engine) launch(s *Session, r *Run, parentCtx context.Context, depth int) (json.RawMessage, error) {
	// Only a local check here (e.mu is held); the directory is created by execRun.
	if _, err := e.node(r.NodeID); err != nil {
		return nil, err
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)
	rc := &runCtl{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{}), steps: map[string]*stepCtl{}, depth: depth, topLvl: r.ParentRunID == ""}
	e.runs[r.ID] = r
	e.ctls[r.ID] = rc
	if r.ParentRunID == "" {
		s.RunIDs = append(s.RunIDs, r.ID)
	}
	e.touchRun(r)
	e.hub.Publish("run.full", r) // full content (source, workflow) once at creation
	e.touchSession(s)
	go e.execRun(r, rc)
	return json.Marshal(map[string]any{"session": e.sessionInfo(s), "run": e.summary(r)})
}

// Retry starts a new run with the same input as an earlier one.
func (e *Engine) Retry(runID string) (json.RawMessage, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.runs[runID]
	if old == nil {
		return nil, ErrNotFound
	}
	if old.ParentRunID != "" {
		return nil, errors.New("子 workflow 不能单独重新运行，请重新运行父任务")
	}
	if old.Kind == KindManager {
		return nil, errors.New("管家的回答不能重新运行，直接再问一次就行")
	}
	s := e.sessions[old.SessionID]
	if s == nil {
		return nil, ErrNotFound
	}
	if e.sessionBusy(s) {
		return nil, ErrBusy
	}
	var r *Run
	if old.Kind == KindChat {
		r = &Run{
			ID: newID("r"), SessionID: s.ID, Kind: KindChat, Title: old.Title, Prompt: old.Prompt, Cwd: s.Cwd, NodeID: s.NodeID,
			Model: old.Model, Sandbox: old.Sandbox, Status: StatusQueued, CreatedAt: time.Now(), Network: old.Network,
			WritableRoots: old.WritableRoots, Task: old.Task, TaskFile: old.TaskFile, TaskTitle: old.TaskTitle, Note: old.Note, Parallel: old.Parallel,
		}
		r.Steps = []*Step{{ID: "codex", Title: "Codex", Type: workflow.TypeAgent, DependsOn: []string{}, If: workflow.IfSuccess,
			Status: StatusPending, MaxAttempts: 1, Cwd: s.Cwd, Prompt: old.Prompt, Items: []*Item{}}}
	} else {
		wf, err := workflow.ParseMode(old.Source, strings.TrimSuffix(old.WorkflowFile, ".md"), old.Mode)
		if err != nil {
			return nil, err
		}
		r = e.newWorkflowRun(s, wf, old.Source, old.WorkflowFile, old.Cwd, old.Vars, old.Model, old.Sandbox)
		r.Mode = old.Mode
	}
	r.RetryOf = old.ID
	return e.launch(s, r, nil, 0)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ---------------------------------------------------------------- control

func (e *Engine) Pause(runID string) error {
	e.mu.Lock()
	r, rc := e.runs[runID], e.ctls[runID]
	if r == nil {
		e.mu.Unlock()
		return ErrNotFound
	}
	if rc == nil || !r.Status.Active() {
		e.mu.Unlock()
		return errors.New("任务未在运行，无法暂停")
	}
	if rc.paused {
		e.mu.Unlock()
		return nil
	}
	rc.paused = true
	r.Status = StatusPaused
	r.Activity = "已暂停"
	var fns []func()
	for _, st := range r.Steps {
		if c := rc.steps[st.ID]; c != nil && c.pause != nil && st.Status == StatusRunning {
			fns = append(fns, c.pause)
		}
	}
	e.touchRun(r)
	e.touchSession(e.sessions[r.SessionID])
	e.mu.Unlock()
	e.logf(runID, "", "host", "info", "用户暂停任务")
	for _, f := range fns {
		f()
	}
	return nil
}

func (e *Engine) Resume(runID string) error {
	e.mu.Lock()
	r, rc := e.runs[runID], e.ctls[runID]
	if r == nil {
		e.mu.Unlock()
		return ErrNotFound
	}
	if rc == nil || !rc.paused {
		e.mu.Unlock()
		return errors.New("任务未处于暂停状态")
	}
	rc.paused = false
	if r.StartedAt == nil {
		r.Status = StatusQueued
	} else {
		r.Status = StatusRunning
	}
	r.Activity = ""
	var fns []func()
	for _, st := range r.Steps {
		if c := rc.steps[st.ID]; c != nil && c.resume != nil && (st.Status == StatusPaused || st.Status == StatusRunning) {
			fns = append(fns, c.resume)
		}
	}
	e.touchRun(r)
	e.touchSession(e.sessions[r.SessionID])
	e.mu.Unlock()
	e.logf(runID, "", "host", "info", "用户继续任务")
	for _, f := range fns {
		f()
	}
	e.wake(rc)
	return nil
}

func (e *Engine) Stop(runID string) error {
	e.mu.Lock()
	r, rc := e.runs[runID], e.ctls[runID]
	if r == nil {
		e.mu.Unlock()
		return ErrNotFound
	}
	if rc == nil || !r.Status.Active() {
		e.mu.Unlock()
		return errors.New("任务未在运行")
	}
	rc.stopped = true
	r.Activity = "正在停止…"
	e.touchRun(r)
	e.mu.Unlock()
	e.logf(runID, "", "host", "info", "用户停止任务")
	rc.cancel()
	e.wake(rc)
	return nil
}

func (e *Engine) wake(rc *runCtl) {
	select {
	case rc.wake <- struct{}{}:
	default:
	}
}

// StopAll stops every active run; used on shutdown.
func (e *Engine) StopAll(timeout time.Duration) {
	e.mu.Lock()
	var waits []chan struct{}
	for id, rc := range e.ctls {
		if r := e.runs[id]; r != nil && r.Status.Active() {
			rc.stopped = true
			rc.cancel()
			waits = append(waits, rc.done)
		}
	}
	e.mu.Unlock()
	deadline := time.After(timeout)
	for _, w := range waits {
		select {
		case <-w:
		case <-deadline:
			e.Flush()
			return
		}
	}
	e.Flush()
}
