// Package server exposes the engine over HTTP: a JSON API, a Server-Sent
// Events stream for live updates, and the embedded web UI.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"agenthost/internal/engine"
	"agenthost/internal/workflow"
)

const cookieName = "agenthost_token"

type Server struct {
	eng    *engine.Engine
	token  string // empty: authentication disabled
	static fs.FS
	info   map[string]any
}

func New(eng *engine.Engine, token string, static fs.FS, info map[string]any) http.Handler {
	s := &Server{eng: eng, token: token, static: static, info: info}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

// ---------------------------------------------------------------- helpers

type httpError struct {
	code int
	msg  string
}

func (e *httpError) Error() string { return e.msg }

func bad(msg string) error { return &httpError{http.StatusBadRequest, msg} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	switch b := v.(type) {
	case json.RawMessage:
		_, _ = w.Write(b)
	default:
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var he *httpError
	var pe *workflow.ParseError
	switch {
	case errors.As(err, &he):
		code = he.code
	case errors.Is(err, engine.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, engine.ErrBusy):
		code = http.StatusConflict
	case errors.As(err, &pe):
		code = http.StatusBadRequest
	default:
		// Most engine errors are validation problems with a readable message.
		if !strings.Contains(err.Error(), "permission denied") {
			code = http.StatusBadRequest
		}
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 8<<20))
	if err := dec.Decode(v); err != nil && err != io.EOF {
		return bad("请求体不是合法的 JSON：" + err.Error())
	}
	return nil
}

func (s *Server) authed(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	cand := ""
	if c, err := r.Cookie(cookieName); err == nil {
		cand = c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		cand = strings.TrimPrefix(h, "Bearer ")
	}
	return subtle.ConstantTimeCompare([]byte(cand), []byte(s.token)) == 1
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil, MaxAge: maxAge,
	})
}

// ---------------------------------------------------------------- static UI

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	// One-click login links: /?token=...
	if tok := r.URL.Query().Get("token"); tok != "" && s.token != "" {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) == 1 {
			s.setCookie(w, r, tok, 60*60*24*30)
		}
		q := r.URL.Query()
		q.Del("token")
		u := url.URL{Path: r.URL.Path, RawQuery: q.Encode()}
		http.Redirect(w, r, u.String(), http.StatusFound)
		return
	}
	if s.static == nil {
		http.Error(w, "web UI not built", http.StatusNotFound)
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	f, err := s.static.Open(p)
	if err == nil {
		st, _ := f.Stat()
		f.Close()
		if st != nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			serveFS(w, r, s.static, p)
			return
		}
	}
	// SPA fallback.
	w.Header().Set("Cache-Control", "no-cache")
	serveFS(w, r, s.static, "index.html")
}

// ---------------------------------------------------------------- API routing

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/"), "/")
	for i, p := range parts {
		if u, err := url.PathUnescape(p); err == nil {
			parts[i] = u
		}
	}
	m := r.Method

	if parts[0] == "auth" {
		s.handleAuth(w, r)
		return
	}
	if parts[0] == "nodes" && len(parts) == 2 && parts[1] == "connect" {
		s.eng.Config().Nodes.HandleConnect(w, r) // authenticated with the node token
		return
	}
	if !s.authed(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录或访问令牌无效"})
		return
	}
	// Basic CSRF defence for cookie-authenticated mutations.
	if m != http.MethodGet && m != http.MethodHead {
		if origin := r.Header.Get("Origin"); origin != "" {
			if u, err := url.Parse(origin); err == nil && u.Host != r.Host {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "cross-origin request rejected"})
				return
			}
		}
	}

	var err error
	switch {
	case parts[0] == "info" && m == http.MethodGet:
		writeJSON(w, 200, s.info)
	case parts[0] == "events" && m == http.MethodGet:
		s.handleEvents(w, r)
	case parts[0] == "sessions":
		err = s.routeSessions(w, r, parts[1:])
	case parts[0] == "tasks" && len(parts) == 1 && m == http.MethodPost:
		err = s.createTask(w, r)
	case parts[0] == "runs":
		err = s.routeRuns(w, r, parts[1:])
	case parts[0] == "workflows":
		err = s.routeWorkflows(w, r, parts[1:])
	case parts[0] == "tasklib":
		err = s.routeTaskLib(w, r, parts[1:])
	case parts[0] == "skills" && len(parts) == 1 && m == http.MethodGet:
		err = s.listSkills(w, r)
	case parts[0] == "fs" && m == http.MethodGet:
		err = s.listDirs(w, r)
	case parts[0] == "nodes":
		err = s.routeNodes(w, r, parts[1:])
	case parts[0] == "settings":
		err = s.routeSettings(w, r, parts[1:])
	case parts[0] == "files" && m == http.MethodGet:
		err = s.routeFiles(w, r, parts[1:])
	default:
		err = &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	if err != nil {
		writeErr(w, err)
	}
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, map[string]bool{"required": s.token != "", "authenticated": s.authed(r)})
	case http.MethodPost:
		var body struct {
			Token string `json:"token"`
		}
		if err := readJSON(r, &body); err != nil {
			writeErr(w, err)
			return
		}
		if s.token != "" && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Token)), []byte(s.token)) != 1 {
			time.Sleep(500 * time.Millisecond)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "访问令牌不正确"})
			return
		}
		s.setCookie(w, r, s.token, 60*60*24*30)
		writeJSON(w, 200, map[string]bool{"authenticated": true})
	case http.MethodDelete:
		s.setCookie(w, r, "", -1)
		writeJSON(w, 200, map[string]bool{"authenticated": false})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	sub := s.eng.Hub().Subscribe()
	defer s.eng.Hub().Unsubscribe(sub)
	_, _ = io.WriteString(w, "retry: 2000\nevent: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = io.WriteString(w, ": ping\n\n")
			fl.Flush()
		case b, ok := <-sub.C:
			if !ok {
				_, _ = io.WriteString(w, "event: resync\ndata: {}\n\n")
				fl.Flush()
				return
			}
			writeEvent(w, b)
			// Drain whatever else is queued before flushing.
		drain:
			for i := 0; i < 256; i++ {
				select {
				case b2, ok := <-sub.C:
					if !ok {
						break drain
					}
					writeEvent(w, b2)
				default:
					break drain
				}
			}
			fl.Flush()
		}
	}
}

// serveFS serves one file from an fs.FS (http.ServeFileFS needs Go 1.22).
func serveFS(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) {
	f, err := fsys.Open(name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		b, _ := io.ReadAll(f)
		rs = strings.NewReader(string(b))
	}
	http.ServeContent(w, r, name, st.ModTime(), rs)
}

func writeEvent(w io.Writer, b []byte) {
	_, _ = io.WriteString(w, "data: ")
	_, _ = w.Write(b)
	_, _ = io.WriteString(w, "\n\n")
}

// ---------------------------------------------------------------- sessions

func (s *Server) routeSessions(w http.ResponseWriter, r *http.Request, parts []string) error {
	m := r.Method
	switch {
	case len(parts) == 0 && m == http.MethodGet:
		writeJSON(w, 200, s.eng.ListSessions())
	case len(parts) == 0 && m == http.MethodPost:
		var body struct {
			Title  string `json:"title"`
			Cwd    string `json:"cwd"`
			NodeID string `json:"nodeId"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		sess, err := s.eng.CreateSession(body.Title, body.Cwd, body.NodeID)
		if err != nil {
			return err
		}
		writeJSON(w, 201, sess)
	case len(parts) == 1 && m == http.MethodGet:
		b, err := s.eng.GetSession(parts[0])
		if err != nil {
			return err
		}
		writeJSON(w, 200, b)
	case len(parts) == 1 && m == http.MethodPatch:
		var body struct {
			Title  *string `json:"title"`
			Cwd    *string `json:"cwd"`
			NodeID *string `json:"nodeId"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		b, err := s.eng.UpdateSession(parts[0], body.Title, body.Cwd, body.NodeID)
		if err != nil {
			return err
		}
		writeJSON(w, 200, b)
	case len(parts) == 1 && m == http.MethodDelete:
		if err := s.eng.DeleteSession(parts[0]); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case len(parts) == 2 && parts[1] == "chat" && m == http.MethodPost:
		var req engine.ChatRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		req.SessionID = parts[0]
		b, err := s.eng.StartChat(req)
		if err != nil {
			return err
		}
		writeJSON(w, 201, b)
	case len(parts) == 2 && parts[1] == "git" && m == http.MethodGet:
		g, err := s.eng.GitStatus(parts[0])
		if err != nil {
			return err
		}
		writeJSON(w, 200, g)
	case len(parts) == 3 && parts[1] == "git" && parts[2] == "sync" && m == http.MethodPost:
		var body struct {
			Note string `json:"note"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		b, err := s.eng.GitSync(parts[0], body.Note)
		if err != nil {
			return err
		}
		writeJSON(w, 201, b)
	case len(parts) == 2 && parts[1] == "workflow" && m == http.MethodPost:
		var req engine.WorkflowRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		req.SessionID = parts[0]
		b, err := s.eng.StartWorkflow(req)
		if err != nil {
			return err
		}
		writeJSON(w, 201, b)
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}

// createTask creates a session when needed and starts a chat or workflow run in it.
func (s *Server) createTask(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		SessionID string            `json:"sessionId"`
		Title     string            `json:"title"`
		Cwd       string            `json:"cwd"`
		Text      string            `json:"text"`
		Workflow  string            `json:"workflow"`
		Source    string            `json:"source"`
		Vars      map[string]string `json:"vars"`
		Model     string            `json:"model"`
		Sandbox   string            `json:"sandbox"`
		Mode      string            `json:"mode"`
		NodeID    string            `json:"nodeId"`
		Task      string            `json:"task"`     // a task from the library
		TaskFile  string            `json:"taskFile"` // or a Markdown file on the server
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if body.Workflow == "" && strings.TrimSpace(body.Source) == "" && strings.TrimSpace(body.Text) == "" && body.Task == "" && body.TaskFile == "" {
		return bad("需要提供 text（任务描述）、task（任务库里的任务）或 taskFile（服务器上的 Markdown 文件）")
	}
	sid := body.SessionID
	created := false
	if sid == "" {
		title, cwd, nodeID := body.Title, body.Cwd, body.NodeID
		// A new session goes where the task says, unless the request says otherwise.
		if body.Task != "" && cwd == "" && nodeID == "" {
			t, err := s.eng.Tasks().Get(body.Task)
			if err != nil {
				return &httpError{http.StatusNotFound, "找不到任务「" + body.Task + "」"}
			}
			cwd, nodeID = t.Cwd, t.Node
		}
		if body.TaskFile != "" && cwd == "" {
			cwd = path.Dir(body.TaskFile)
		}
		sess, err := s.eng.CreateSession(title, cwd, nodeID)
		if err != nil {
			return err
		}
		sid, created = sess.ID, true
	}
	var b json.RawMessage
	var err error
	if body.Workflow != "" || strings.TrimSpace(body.Source) != "" {
		cwd := ""
		if !created {
			cwd = body.Cwd
		}
		b, err = s.eng.StartWorkflow(engine.WorkflowRequest{SessionID: sid, Name: body.Workflow, Source: body.Source, Cwd: cwd, Vars: body.Vars, Model: body.Model, Sandbox: body.Sandbox, Mode: body.Mode})
	} else {
		b, err = s.eng.StartChat(engine.ChatRequest{SessionID: sid, Text: body.Text, Task: body.Task, TaskFile: body.TaskFile, Model: body.Model, Sandbox: body.Sandbox})
	}
	if err != nil {
		if created {
			_ = s.eng.DeleteSession(sid)
		}
		return err
	}
	writeJSON(w, 201, b)
	return nil
}

// ---------------------------------------------------------------- runs

func (s *Server) routeRuns(w http.ResponseWriter, r *http.Request, parts []string) error {
	m := r.Method
	switch {
	case len(parts) == 0 && m == http.MethodGet:
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		writeJSON(w, 200, s.eng.ListRuns(engine.RunFilter{Status: q.Get("status"), SessionID: q.Get("sessionId"), Limit: limit}))
	case len(parts) == 1 && m == http.MethodGet:
		b, err := s.eng.GetRun(parts[0])
		if err != nil {
			return err
		}
		writeJSON(w, 200, b)
	case len(parts) == 2 && parts[1] == "logs" && m == http.MethodGet:
		q := r.URL.Query()
		after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 20000 {
			limit = 5000
		}
		logs, err := s.eng.Logs(parts[0], after, limit)
		if err != nil {
			return err
		}
		writeJSON(w, 200, logs)
	case len(parts) == 2 && m == http.MethodPost:
		id := parts[0]
		var err error
		switch parts[1] {
		case "pause":
			err = s.eng.Pause(id)
		case "resume":
			err = s.eng.Resume(id)
		case "stop":
			err = s.eng.Stop(id)
		case "retry":
			b, err := s.eng.Retry(id)
			if err != nil {
				return err
			}
			writeJSON(w, 201, b)
			return nil
		default:
			return &httpError{http.StatusNotFound, "unknown action"}
		}
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}

// ---------------------------------------------------------------- workflows

type parseResult struct {
	Workflow *workflow.Workflow `json:"workflow,omitempty"`
	Layers   [][]string         `json:"layers,omitempty"`
	Warnings []string           `json:"warnings,omitempty"`
	Error    string             `json:"error,omitempty"`
	Line     int                `json:"line,omitempty"`
}

func parseSource(src, name string, mode ...string) parseResult {
	m := ""
	if len(mode) > 0 {
		m = mode[0]
	}
	wf, err := workflow.ParseMode(src, name, m)
	if err != nil {
		res := parseResult{Error: err.Error()}
		var pe *workflow.ParseError
		if errors.As(err, &pe) {
			res.Line = pe.Line
		}
		return res
	}
	wf.Body = ""
	return parseResult{Workflow: wf, Layers: wf.Layers(), Warnings: workflow.Lint(wf)}
}

func (s *Server) routeWorkflows(w http.ResponseWriter, r *http.Request, parts []string) error {
	lib := s.eng.Library()
	m := r.Method
	switch {
	case len(parts) == 0 && m == http.MethodGet:
		writeJSON(w, 200, map[string]any{"dir": lib.Dir(), "workflows": lib.List()})
	case len(parts) == 1 && parts[0] == "parse" && m == http.MethodPost:
		var body struct {
			Source string `json:"source"`
			Name   string `json:"name"`
			Mode   string `json:"mode"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		writeJSON(w, 200, parseSource(body.Source, strings.TrimSuffix(body.Name, ".md"), body.Mode))
	case len(parts) == 1 && m == http.MethodGet:
		src, err := lib.Read(parts[0])
		if err != nil {
			return err
		}
		name, _ := engine.CleanName(parts[0])
		writeJSON(w, 200, map[string]any{"name": name, "source": src, "parsed": parseSource(src, strings.TrimSuffix(name, ".md"))})
	case len(parts) == 1 && m == http.MethodPut:
		var body struct {
			Source string `json:"source"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		name, err := lib.Save(parts[0], body.Source)
		if err != nil {
			return err
		}
		writeJSON(w, 200, map[string]any{"name": name, "parsed": parseSource(body.Source, strings.TrimSuffix(name, ".md"))})
	case len(parts) == 1 && m == http.MethodDelete:
		if err := lib.Delete(parts[0]); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}

// routeTaskLib serves the task library: saved task descriptions.
func (s *Server) routeTaskLib(w http.ResponseWriter, r *http.Request, parts []string) error {
	lib := s.eng.Tasks()
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		writeJSON(w, 200, lib.List())
	case len(parts) == 0 && r.Method == http.MethodPost, len(parts) == 1 && r.Method == http.MethodPut:
		// Only the editable fields; name and timestamps are the server's.
		var body struct {
			Title string `json:"title"`
			Node  string `json:"node"`
			Cwd   string `json:"cwd"`
			Body  string `json:"body"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		t := engine.Task{Title: body.Title, Node: body.Node, Cwd: body.Cwd, Body: body.Body}
		if len(parts) == 1 {
			old, err := lib.Get(parts[0])
			if err != nil {
				return err
			}
			t.Name = old.Name
		}
		saved, err := lib.Save(t)
		if err != nil {
			return bad(err.Error())
		}
		writeJSON(w, 200, saved)
	case len(parts) == 1 && r.Method == http.MethodGet:
		t, err := lib.Get(parts[0])
		if err != nil {
			return err
		}
		writeJSON(w, 200, t)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := lib.Delete(parts[0]); err != nil {
			return err
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}

// listSkills lists the skills found in a directory (and its repository) on a server.
func (s *Server) listSkills(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	scan, err := s.eng.DiscoverSkills(q.Get("node"), q.Get("path"))
	if err != nil {
		return bad(err.Error())
	}
	writeJSON(w, 200, scan)
	return nil
}

// listDirs lists sub-directories on a server, for the working-directory picker.
func (s *Server) listDirs(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	nd, err := s.eng.Config().Nodes.Get(q.Get("node"))
	if err != nil {
		return bad(err.Error())
	}
	l, err := nd.ListDir(q.Get("path"))
	if err != nil {
		return bad(fmt.Sprintf("无法读取目录：%v", err))
	}
	writeJSON(w, 200, l)
	return nil
}

func (s *Server) routeSettings(w http.ResponseWriter, r *http.Request, parts []string) error {
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		writeJSON(w, 200, s.eng.GetSettings())
	case len(parts) == 0 && r.Method == http.MethodPut:
		var p engine.SettingsPatch
		if err := readJSON(r, &p); err != nil {
			return err
		}
		v, err := s.eng.UpdateSettings(p)
		if err != nil {
			return bad(err.Error())
		}
		writeJSON(w, 200, v)
	case len(parts) == 1 && parts[0] == "ssh-key" && r.Method == http.MethodPost:
		v, err := s.eng.GenerateSSHKey()
		if err != nil {
			return err
		}
		writeJSON(w, 200, v)
	case len(parts) == 1 && (parts[0] == "test-git" || parts[0] == "test-codex") && r.Method == http.MethodPost:
		var body struct {
			NodeID string `json:"nodeId"`
			Repo   string `json:"repo"`
		}
		if err := readJSON(r, &body); err != nil {
			return err
		}
		var res engine.CheckResult
		var err error
		if parts[0] == "test-git" {
			res, err = s.eng.TestGit(body.NodeID, body.Repo)
		} else {
			res, err = s.eng.TestCodex(body.NodeID)
		}
		if err != nil {
			return bad(err.Error())
		}
		writeJSON(w, 200, res)
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}

func (s *Server) routeFiles(w http.ResponseWriter, r *http.Request, parts []string) error {
	q := r.URL.Query()
	nodeID, path := q.Get("node"), q.Get("path")
	switch {
	case len(parts) == 0:
		l, err := s.eng.ListFiles(nodeID, path)
		if err != nil {
			return bad(err.Error())
		}
		writeJSON(w, 200, l)
	case len(parts) == 1 && parts[0] == "content":
		v, err := s.eng.ReadFileView(nodeID, path)
		if err != nil {
			return bad(err.Error())
		}
		writeJSON(w, 200, v)
	case len(parts) == 1 && parts[0] == "raw":
		mt, data, err := s.eng.RawFile(nodeID, path)
		if err != nil {
			return bad(err.Error())
		}
		// Never let a served file run as a page on this origin.
		if !strings.HasPrefix(mt, "image/") || strings.Contains(mt, "svg") {
			mt = "application/octet-stream"
		}
		w.Header().Set("Content-Type", mt)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		if q.Get("download") != "" || mt == "application/octet-stream" {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.PathEscape(pathBase(path))))
		}
		_, _ = w.Write(data)
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}

func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (s *Server) routeNodes(w http.ResponseWriter, r *http.Request, parts []string) error {
	reg := s.eng.Config().Nodes
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]any{"nodes": reg.List(), "default": reg.Default()})
	case len(parts) == 1 && parts[0] == "join" && r.Method == http.MethodGet:
		// What to run on another server so it joins this one.
		hub := r.URL.Query().Get("hub")
		if hub == "" {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			hub = scheme + "://" + r.Host
		}
		writeJSON(w, 200, map[string]string{"hub": hub, "token": reg.Token()})
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := reg.Forget(parts[0]); err != nil {
			return bad(err.Error())
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	default:
		return &httpError{http.StatusNotFound, "unknown endpoint"}
	}
	return nil
}
