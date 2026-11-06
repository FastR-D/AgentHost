// Package codex talks to the Codex CLI through `codex app-server`, a
// JSON-RPC 2.0 protocol over stdio (one JSON object per line).
package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"agenthost/internal/node"
)

// Options configure how the Codex CLI process is launched.
type Options struct {
	Node      node.Node // where to run; required
	Bin       string    // codex binary, default "codex"
	ExtraArgs []string  // extra args placed before "app-server", e.g. -c overrides
	Env       []string  // extra environment variables (KEY=VALUE)
	Cwd       string
	// Experimental opts into experimental app-server API (needed for dynamic tools).
	Experimental bool
}

// Handlers receive traffic from the app-server. All callbacks run on the
// reader goroutine and must not block for long.
type Handlers struct {
	// Notification is called for every server notification.
	Notification func(method string, params json.RawMessage)
	// Request is called for server -> client requests (approvals etc.).
	// Returning an error sends a JSON-RPC error response.
	Request func(method string, params json.RawMessage) (any, error)
	// Stderr receives each line the codex process writes to stderr.
	Stderr func(line string)
	// Trace receives a short description of outgoing/incoming RPC messages.
	Trace func(direction, method string, payload json.RawMessage)
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("codex rpc error %d: %s", e.Code, e.Message) }

// Client is one running `codex app-server` process.
type Client struct {
	proc     node.Proc
	stdin    io.WriteCloser
	writeMu  sync.Mutex
	nextID   atomic.Int64
	mu       sync.Mutex
	pending  map[int64]chan rpcMessage
	h        Handlers
	done     chan struct{}
	exitErr  error
	exitCode int
	Argv     []string
	Pid      int
	closeOne sync.Once
}

// Start launches the app-server and performs the initialize handshake.
func Start(ctx context.Context, opts Options, h Handlers) (*Client, error) {
	bin := opts.Bin
	if bin == "" {
		bin = "codex"
	}
	args := append(append([]string{}, opts.ExtraArgs...), "app-server")
	if opts.Node == nil {
		return nil, errors.New("codex: no node to run on")
	}
	proc, err := opts.Node.Start(node.Spec{Argv: append([]string{bin}, args...), Dir: opts.Cwd, Env: opts.Env})
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	c := &Client{
		proc:    proc,
		stdin:   proc.Stdin(),
		pending: map[int64]chan rpcMessage{},
		h:       h,
		done:    make(chan struct{}),
		Argv:    append([]string{bin}, args...),
		Pid:     proc.Pid(),
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.readStdout(proc.Stdout()) }()
	go func() { defer wg.Done(); c.readStderr(proc.Stderr()) }()
	go func() {
		wg.Wait()
		c.exitCode, c.exitErr = proc.Wait()
		if c.exitErr == nil && c.exitCode != 0 {
			c.exitErr = fmt.Errorf("exit status %d", c.exitCode)
		}
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		close(c.done)
	}()

	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var initRes json.RawMessage
	initParams := map[string]any{
		"clientInfo": map[string]any{"name": "agenthost", "title": "AgentHost", "version": "0.1.0"},
	}
	if opts.Experimental {
		initParams["capabilities"] = map[string]any{"experimentalApi": true}
	}
	err = c.Call(initCtx, "initialize", initParams, &initRes)
	if err != nil {
		c.Kill()
		return nil, fmt.Errorf("codex initialize: %w", err)
	}
	if err := c.Notify("initialized", nil); err != nil {
		c.Kill()
		return nil, err
	}
	return c, nil
}

func (c *Client) readStdout(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var m rpcMessage
		if err := json.Unmarshal(line, &m); err != nil {
			if c.h.Stderr != nil {
				c.h.Stderr("[stdout] " + string(line))
			}
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			c.trace("<-req", m.Method, m.Params)
			go c.answer(m)
		case m.Method != "":
			c.trace("<-", m.Method, m.Params)
			if c.h.Notification != nil {
				c.h.Notification(m.Method, m.Params)
			}
		case len(m.ID) > 0:
			var id int64
			if err := json.Unmarshal(m.ID, &id); err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
				close(ch)
			}
		}
	}
}

func (c *Client) readStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if c.h.Stderr != nil {
			c.h.Stderr(stripANSI(sc.Text()))
		}
	}
}

func (c *Client) answer(m rpcMessage) {
	var result any
	var err error
	if c.h.Request != nil {
		result, err = c.h.Request(m.Method, m.Params)
	} else {
		err = errors.New("not supported by AgentHost")
	}
	resp := map[string]any{"jsonrpc": "2.0", "id": m.ID}
	if err != nil {
		resp["error"] = map[string]any{"code": -32601, "message": err.Error()}
	} else {
		resp["result"] = result
	}
	_ = c.write(resp)
}

func (c *Client) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return errors.New("codex app-server has exited")
	default:
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

// Call sends a request and decodes the result into out (may be nil).
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan rpcMessage, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	raw, _ := json.Marshal(params)
	c.trace("->", method, raw)
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.write(msg); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return fmt.Errorf("codex app-server exited before answering %s", method)
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	}
}

// Notify sends a notification (no response expected).
func (c *Client) Notify(method string, params any) error {
	raw, _ := json.Marshal(params)
	c.trace("->", method, raw)
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.write(msg)
}

func (c *Client) trace(dir, method string, payload json.RawMessage) {
	if c.h.Trace != nil {
		c.h.Trace(dir, method, payload)
	}
}

// Done is closed once the process has exited.
func (c *Client) Done() <-chan struct{} { return c.done }

// ExitErr is the process exit error, valid after Done is closed.
func (c *Client) ExitErr() error { return c.exitErr }

// ExitCode is valid after Done is closed (-1 if killed or lost).
func (c *Client) ExitCode() int { return c.exitCode }

// Close asks the process to exit (closing stdin), then kills the whole
// process group if it has not exited within a grace period.
func (c *Client) Close() {
	c.closeOne.Do(func() {
		c.writeMu.Lock()
		_ = c.stdin.Close()
		c.writeMu.Unlock()
		select {
		case <-c.done:
			return
		case <-time.After(3 * time.Second):
		}
		_ = c.proc.Signal(syscall.SIGTERM)
		select {
		case <-c.done:
		case <-time.After(3 * time.Second):
			c.Kill()
		}
	})
}

// Kill kills the process group immediately.
func (c *Client) Kill() {
	_ = c.proc.Signal(syscall.SIGKILL)
}

func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
