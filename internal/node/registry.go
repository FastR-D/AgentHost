package node

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Registry tracks the local node and every remote node that has connected.
type Registry struct {
	mu       sync.Mutex
	local    *Local // nil when the web server must not run tasks itself
	remotes  map[string]*Remote
	known    map[string]Info // remote nodes seen before, persisted
	path     string
	token    string
	onChange func()
	conns    sync.WaitGroup // connection handlers still running
	closed   bool
}

// NewRegistry creates a registry. local may be nil. Known nodes are kept in dataDir/nodes.json.
func NewRegistry(local *Local, dataDir, token string) *Registry {
	g := &Registry{local: local, remotes: map[string]*Remote{}, known: map[string]Info{}, token: token,
		path: filepath.Join(dataDir, "nodes.json")}
	if b, err := os.ReadFile(g.path); err == nil {
		var list []Info
		if json.Unmarshal(b, &list) == nil {
			for _, i := range list {
				i.Online, i.Procs = false, 0
				g.known[i.ID] = i
			}
		}
	}
	return g
}

// OnChange registers a callback fired when a node connects or disconnects.
func (g *Registry) OnChange(fn func()) {
	g.mu.Lock()
	g.onChange = fn
	g.mu.Unlock()
}

func (g *Registry) changed() {
	g.mu.Lock()
	fn := g.onChange
	g.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (g *Registry) Token() string { return g.token }

// Default is the node new sessions use when none is chosen.
func (g *Registry) Default() string {
	if g.local != nil {
		return LocalID
	}
	for _, i := range g.List() {
		if i.Online {
			return i.ID
		}
	}
	return ""
}

// Get returns a node by id ("" = default). Known but disconnected nodes are
// returned too; their operations fail with ErrOffline.
func (g *Registry) Get(id string) (Node, error) {
	if id == "" {
		id = g.Default()
		if id == "" {
			return nil, fmt.Errorf("没有可用的服务器：本机已禁用执行任务，也没有远程服务器在线")
		}
	}
	if id == LocalID {
		if g.local == nil {
			return nil, fmt.Errorf("本机已禁用执行任务（AGENTHOST_LOCAL_NODE=off），请选择其他服务器")
		}
		return g.local, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if r := g.remotes[id]; r != nil {
		return r, nil
	}
	if i, ok := g.known[id]; ok {
		return &offline{info: i}, nil
	}
	return nil, fmt.Errorf("%w：%s", ErrUnknown, id)
}

// List returns all nodes: local first, then remotes by name.
func (g *Registry) List() []Info {
	out := make([]Info, 0)
	if g.local != nil {
		out = append(out, g.local.Info())
	}
	g.mu.Lock()
	var rs []Info
	for id, i := range g.known {
		if r := g.remotes[id]; r != nil {
			i = r.Info()
		}
		rs = append(rs, i)
	}
	g.mu.Unlock()
	sort.Slice(rs, func(a, b int) bool {
		if rs[a].Online != rs[b].Online {
			return rs[a].Online
		}
		return rs[a].Name < rs[b].Name
	})
	return append(out, rs...)
}

// Forget removes a disconnected node from the list.
func (g *Registry) Forget(id string) error {
	g.mu.Lock()
	if g.remotes[id] != nil {
		g.mu.Unlock()
		return fmt.Errorf("服务器在线，不能移除；请先在那台机器上停止 agenthost node")
	}
	if _, ok := g.known[id]; !ok {
		g.mu.Unlock()
		return ErrUnknown
	}
	delete(g.known, id)
	g.mu.Unlock()
	g.persist()
	g.changed()
	return nil
}

func (g *Registry) persist() {
	g.mu.Lock()
	list := make([]Info, 0, len(g.known))
	for _, i := range g.known {
		list = append(list, i)
	}
	g.mu.Unlock()
	sort.Slice(list, func(a, b int) bool { return list[a].ID < list[b].ID })
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := g.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, g.path)
	}
}

var reNodeName = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]{0,39}$`)

var upgrader = websocket.Upgrader{
	ReadBufferSize: 32 * 1024, WriteBufferSize: 32 * 1024,
	// Nodes are programs, not browsers; they authenticate with the node token.
	CheckOrigin: func(*http.Request) bool { return true },
}

// Close disconnects every remote node and waits until their connection
// handlers have recorded the disconnect.
func (g *Registry) Close() {
	g.mu.Lock()
	g.closed = true
	rs := make([]*Remote, 0, len(g.remotes))
	for _, r := range g.remotes {
		rs = append(rs, r)
	}
	g.mu.Unlock()
	for _, r := range rs {
		r.kick("Web 服务器正在关闭")
	}
	g.conns.Wait()
}

// HandleConnect is the WebSocket endpoint remote nodes dial.
func (g *Registry) HandleConnect(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		http.Error(w, "Web 服务器正在关闭", http.StatusServiceUnavailable)
		return
	}
	g.conns.Add(1)
	g.mu.Unlock()
	defer g.conns.Done()
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if g.token == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(g.token)) != 1 {
		time.Sleep(500 * time.Millisecond)
		http.Error(w, "节点令牌无效", http.StatusUnauthorized)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20)
	ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	var hello msg
	if err := ws.ReadJSON(&hello); err != nil || hello.T != "hello" || hello.Info == nil {
		ws.Close()
		return
	}
	info := *hello.Info
	info.Name = strings.TrimSpace(info.Name)
	info.ID = info.Name
	info.Local = false
	info.Addr = clientAddr(r)
	info.ConnectedAt, info.LastSeen = time.Now(), time.Now()

	// Validate the name before touching g.remotes.
	switch {
	case !reNodeName.MatchString(info.Name):
		ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation,
				"服务器名只能包含字母、数字、中文、点、下划线和连字符，最长 40 个字符"),
			time.Now().Add(time.Second))
		ws.Close()
		slog.Warn("rejected node: bad name", "name", info.Name)
		return
	case strings.EqualFold(info.Name, LocalID):
		ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation,
				"服务器名 local 已被本机占用，请换一个名字"),
			time.Now().Add(time.Second))
		ws.Close()
		slog.Warn("rejected node: reserved name", "name", info.Name)
		return
	}

	rem := newRemote(ws, info)

	// If a connection with the same name already exists, kick it and wait for
	// its serve() goroutine to clean up before registering the new one.
	g.mu.Lock()
	if old := g.remotes[info.ID]; old != nil {
		g.mu.Unlock()
		old.kick("被同名新连接取代")
		// Wait up to 1 s for the old handler to remove itself from g.remotes.
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
			g.mu.Lock()
			gone := g.remotes[info.ID] == nil
			g.mu.Unlock()
			if gone {
				break
			}
		}
		g.mu.Lock() // re-acquire for the registration below
	}
	// g.mu is locked here in all paths.
	g.remotes[info.ID] = rem
	g.known[info.ID] = info
	g.mu.Unlock()
	g.persist()
	slog.Info("node connected", "name", info.Name, "addr", info.Addr, "workspace", info.Workspace)
	g.changed()

	rem.serve() // until the connection ends

	g.mu.Lock()
	if g.remotes[info.ID] == rem {
		delete(g.remotes, info.ID)
	}
	last := rem.Info()
	last.Online, last.Procs, last.LastSeen = false, 0, time.Now()
	if _, ok := g.known[info.ID]; ok {
		g.known[info.ID] = last
	}
	g.mu.Unlock()
	g.persist()
	slog.Info("node disconnected", "name", info.Name)
	g.changed()
}

func clientAddr(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
