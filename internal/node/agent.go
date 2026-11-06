package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// AgentConfig configures `agenthost node`.
type AgentConfig struct {
	Hub   string // web server URL, e.g. https://agenthost.example.com
	Token string // node token shown on the web server's 服务器 page
	Local *Local // executes the work; its Info (name, workspace, codex…) is sent to the hub
	Logf  func(format string, args ...any)
}

// ConnectURL turns the web server address into the WebSocket endpoint.
func ConnectURL(hub string) (string, error) {
	if !strings.Contains(hub, "://") {
		hub = "http://" + hub
	}
	u, err := url.Parse(hub)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("无效的 Web 服务器地址 %q", hub)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("Web 服务器地址只能是 http:// 或 https://")
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/nodes/connect"
	u.RawQuery = ""
	return u.String(), nil
}

// RunAgent keeps a connection to the hub open, reconnecting with backoff,
// until ctx is cancelled.
func RunAgent(ctx context.Context, cfg AgentConfig) error {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	target, err := ConnectURL(cfg.Hub)
	if err != nil {
		return err
	}
	backoff := time.Second
	for {
		start := time.Now()
		err := agentSession(ctx, target, cfg)
		if ctx.Err() != nil {
			return nil
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		cfg.Logf("与 Web 服务器的连接断开：%v；%s 后重连", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

type agentConn struct {
	ws     *websocket.Conn
	local  *Local
	sendCh chan msg
	done   chan struct{}
	mu     sync.Mutex
	procs  map[int64]Proc
	stdin  map[int64]chan []byte // ordered stdin per process; nil entry = EOF
}

func agentSession(ctx context.Context, target string, cfg AgentConfig) error {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+cfg.Token)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 20 * time.Second
	ws, resp, err := dialer.DialContext(ctx, target, h)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("%s %s", resp.Status, strings.TrimSpace(string(body)))
		}
		return err
	}
	info := cfg.Local.Info()
	a := &agentConn{ws: ws, local: cfg.Local, sendCh: make(chan msg, 1024), done: make(chan struct{}), procs: map[int64]Proc{}, stdin: map[int64]chan []byte{}}
	defer a.shutdown()
	if err := ws.WriteJSON(msg{T: "hello", Info: &info}); err != nil {
		return err
	}
	cfg.Logf("已连接 Web 服务器 %s（服务器名：%s）", target, info.Name)

	ws.SetReadDeadline(time.Now().Add(readTimeout))
	ws.SetPingHandler(func(data string) error {
		ws.SetReadDeadline(time.Now().Add(readTimeout))
		return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
	})
	go a.writer()
	go func() {
		select {
		case <-ctx.Done():
			ws.Close()
		case <-a.done:
		}
	}()
	for {
		var m msg
		if err := ws.ReadJSON(&m); err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) && ce.Text != "" {
				return errors.New(ce.Text)
			}
			return err
		}
		ws.SetReadDeadline(time.Now().Add(readTimeout))
		a.handle(m)
	}
}

func (a *agentConn) writer() {
	for {
		select {
		case m := <-a.sendCh:
			a.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if err := a.ws.WriteJSON(m); err != nil {
				a.ws.Close()
				return
			}
		case <-a.done:
			return
		}
	}
}

func (a *agentConn) send(m msg) {
	select {
	case a.sendCh <- m:
	case <-a.done:
	}
}

func (a *agentConn) reply(id int64, err error, fill func(*msg)) {
	m := msg{T: "res", ID: id, OK: err == nil}
	if err != nil {
		m.Err = err.Error()
	} else if fill != nil {
		fill(&m)
	}
	a.send(m)
}

// shutdown kills everything this connection started: the hub can no longer see it.
func (a *agentConn) shutdown() {
	select {
	case <-a.done:
		return
	default:
	}
	close(a.done)
	a.ws.Close()
	a.mu.Lock()
	procs := a.procs
	a.procs = map[int64]Proc{}
	a.mu.Unlock()
	for _, p := range procs {
		p.Signal(syscall.SIGTERM)
		p.Signal(syscall.SIGCONT)
	}
	if len(procs) > 0 {
		time.AfterFunc(3*time.Second, func() {
			for _, p := range procs {
				p.Signal(syscall.SIGKILL)
			}
		})
	}
}

func (a *agentConn) proc(h int64) Proc {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.procs[h]
}

func (a *agentConn) handle(m msg) {
	switch m.T {
	case "in", "eof":
		a.mu.Lock()
		q := a.stdin[m.H]
		a.mu.Unlock()
		if q == nil {
			return
		}
		data := m.Data
		if m.T == "eof" {
			data = nil
		} else if data == nil {
			data = []byte{}
		}
		select {
		case q <- data:
		case <-a.done:
		}
	case "req":
		switch m.Op {
		case "start":
			if m.Spec == nil {
				a.reply(m.ID, errors.New("缺少进程参数"), nil)
				return
			}
			p, err := a.local.Start(*m.Spec)
			if err != nil {
				a.reply(m.ID, err, nil)
				return
			}
			q := make(chan []byte, 256)
			a.mu.Lock()
			a.procs[m.H] = p
			a.stdin[m.H] = q
			a.mu.Unlock()
			exited := make(chan struct{})
			go func() { // write stdin in order without blocking the connection
				for {
					select {
					case data := <-q:
						if data == nil {
							p.Stdin().Close()
							return
						}
						if _, err := p.Stdin().Write(data); err != nil {
							return
						}
					case <-exited:
						return
					case <-a.done:
						return
					}
				}
			}()
			a.reply(m.ID, nil, func(r *msg) { r.Pid, r.Argv = p.Pid(), p.Argv() })
			go a.pump(m.H, p, exited)
		case "signal":
			p := a.proc(m.H)
			if p == nil {
				a.reply(m.ID, nil, nil) // already gone
				return
			}
			a.reply(m.ID, p.Signal(syscall.Signal(m.Sig)), nil)
		case "list":
			l, err := a.local.ListDir(m.Path)
			a.reply(m.ID, err, func(r *msg) { r.List = &l })
		case "mkdir":
			a.reply(m.ID, a.local.MkdirAll(m.Path), nil)
		case "readdir":
			ents, err := a.local.ReadDir(m.Path)
			a.reply(m.ID, err, func(r *msg) { r.Ents = ents })
		case "readfile":
			f, err := a.local.ReadFile(m.Path, m.Max)
			a.reply(m.ID, err, func(r *msg) { r.File = &f })
		case "write":
			a.reply(m.ID, a.local.WriteFile(m.Path, m.Data, m.Mode), nil)
		case "isdir":
			var err error
			if !a.local.IsDir(m.Path) {
				err = errors.New("不是目录")
			}
			a.reply(m.ID, err, nil)
		default:
			a.reply(m.ID, fmt.Errorf("不支持的操作 %q（请升级 agenthost node）", m.Op), nil)
		}
	}
}

// pump forwards a process's output, then its exit status.
func (a *agentConn) pump(h int64, p Proc, exited chan struct{}) {
	defer close(exited)
	var wg sync.WaitGroup
	forward := func(fd int, r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				a.send(msg{T: "out", H: h, Fd: fd, Data: append([]byte{}, buf[:n]...)})
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(2)
	go forward(1, p.Stdout())
	go forward(2, p.Stderr())
	wg.Wait()
	code, err := p.Wait()
	m := msg{T: "exit", H: h, Code: code}
	if err != nil {
		m.Err = err.Error()
	}
	a.send(m)
	a.mu.Lock()
	delete(a.procs, h)
	delete(a.stdin, h) // the writer goroutine exits when the process' stdin fails
	a.mu.Unlock()
}
