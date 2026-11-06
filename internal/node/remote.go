package node

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// msg is the single message type of the hub <-> node protocol (JSON over WebSocket).
//
//	node -> hub: hello, res, out, exit
//	hub -> node: req (start | signal | list | mkdir | isdir), in, eof
type msg struct {
	T    string      `json:"t"`
	ID   int64       `json:"id,omitempty"` // request id
	H    int64       `json:"h,omitempty"`  // process handle, chosen by the hub
	Op   string      `json:"op,omitempty"`
	Spec *Spec       `json:"spec,omitempty"`
	Path string      `json:"path,omitempty"`
	Sig  int         `json:"sig,omitempty"`
	Fd   int         `json:"fd,omitempty"` // 1 stdout, 2 stderr
	Data []byte      `json:"data,omitempty"`
	Code int         `json:"code,omitempty"`
	OK   bool        `json:"ok,omitempty"`
	Err  string      `json:"err,omitempty"`
	Pid  int         `json:"pid,omitempty"`
	Argv []string    `json:"argv,omitempty"`
	List *DirList    `json:"list,omitempty"`
	Info *Info       `json:"info,omitempty"`
	Mode uint32      `json:"mode,omitempty"`
	Max  int64       `json:"max,omitempty"`
	Ents []FileEntry `json:"ents,omitempty"`
	File *FileData   `json:"file,omitempty"`
}

const (
	pingEvery   = 20 * time.Second
	readTimeout = 60 * time.Second
	callTimeout = 30 * time.Second
)

// Remote is a connected node, seen from the hub.
type Remote struct {
	ws      *websocket.Conn
	sendCh  chan msg
	closed  chan struct{}
	closeMu sync.Once

	mu      sync.Mutex
	info    Info
	nextID  int64
	nextH   int64
	pending map[int64]chan msg
	procs   map[int64]*remoteProc
}

func newRemote(ws *websocket.Conn, info Info) *Remote {
	info.Online = true
	return &Remote{
		ws: ws, sendCh: make(chan msg, 1024), closed: make(chan struct{}), info: info,
		pending: map[int64]chan msg{}, procs: map[int64]*remoteProc{},
	}
}

func (r *Remote) Info() Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.info
	i.Procs = len(r.procs)
	return i
}

// serve runs the connection until it fails or close is called.
func (r *Remote) serve() {
	go r.writer()
	r.ws.SetReadDeadline(time.Now().Add(readTimeout))
	r.ws.SetPongHandler(func(string) error {
		r.ws.SetReadDeadline(time.Now().Add(readTimeout))
		r.mu.Lock()
		r.info.LastSeen = time.Now()
		r.mu.Unlock()
		return nil
	})
	for {
		var m msg
		if err := r.ws.ReadJSON(&m); err != nil {
			break
		}
		r.ws.SetReadDeadline(time.Now().Add(readTimeout))
		r.dispatch(m)
	}
	r.close()
}

func (r *Remote) writer() {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case m := <-r.sendCh:
			r.ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if err := r.ws.WriteJSON(m); err != nil {
				r.close()
				return
			}
		case <-t.C:
			if err := r.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				r.close()
				return
			}
		case <-r.closed:
			return
		}
	}
}

func (r *Remote) close() {
	r.closeMu.Do(func() {
		close(r.closed)
		r.ws.Close()
		r.mu.Lock()
		for id, ch := range r.pending {
			close(ch)
			delete(r.pending, id)
		}
		procs := r.procs
		r.procs = map[int64]*remoteProc{}
		r.info.Online = false
		r.mu.Unlock()
		for _, p := range procs {
			p.finish(-1, ErrLost.Error())
		}
	})
}

// kick closes the connection with a reason the node logs.
func (r *Remote) kick(reason string) {
	_ = r.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason), time.Now().Add(time.Second))
	r.close()
}

func (r *Remote) send(m msg) error {
	select {
	case <-r.closed:
		return ErrLost
	case r.sendCh <- m:
		return nil
	}
}

func (r *Remote) call(m msg) (msg, error) {
	ch := make(chan msg, 1)
	r.mu.Lock()
	r.nextID++
	m.ID = r.nextID
	r.pending[m.ID] = ch
	r.mu.Unlock()
	if err := r.send(m); err != nil {
		return msg{}, err
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return msg{}, ErrLost
		}
		if !res.OK {
			return res, errors.New(res.Err)
		}
		return res, nil
	case <-time.After(callTimeout):
		r.mu.Lock()
		delete(r.pending, m.ID)
		r.mu.Unlock()
		return msg{}, fmt.Errorf("服务器 %s 没有响应", r.Info().Name)
	}
}

func (r *Remote) dispatch(m msg) {
	switch m.T {
	case "res":
		r.mu.Lock()
		ch := r.pending[m.ID]
		delete(r.pending, m.ID)
		r.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case "out":
		r.mu.Lock()
		p := r.procs[m.H]
		r.mu.Unlock()
		if p != nil {
			p.push(chunk{fd: m.Fd, data: m.Data})
		}
	case "exit":
		r.mu.Lock()
		p := r.procs[m.H]
		delete(r.procs, m.H)
		r.mu.Unlock()
		if p != nil {
			p.finish(m.Code, m.Err)
		}
	}
}

func (r *Remote) Start(spec Spec) (Proc, error) {
	r.mu.Lock()
	r.nextH++
	h := r.nextH
	p := newRemoteProc(r, h)
	r.procs[h] = p
	r.mu.Unlock()
	res, err := r.call(msg{T: "req", Op: "start", H: h, Spec: &spec})
	if err != nil {
		r.mu.Lock()
		delete(r.procs, h)
		r.mu.Unlock()
		p.finish(-1, err.Error())
		return nil, err
	}
	p.pid, p.argv = res.Pid, res.Argv
	return p, nil
}

func (r *Remote) ListDir(path string) (DirList, error) {
	res, err := r.call(msg{T: "req", Op: "list", Path: path})
	if err != nil {
		return DirList{}, err
	}
	if res.List == nil {
		return DirList{}, errors.New("服务器没有返回目录")
	}
	return *res.List, nil
}

func (r *Remote) MkdirAll(path string) error {
	_, err := r.call(msg{T: "req", Op: "mkdir", Path: path})
	return err
}

func (r *Remote) WriteFile(path string, data []byte, mode uint32) error {
	_, err := r.call(msg{T: "req", Op: "write", Path: path, Data: data, Mode: mode})
	return err
}

func (r *Remote) ReadDir(path string) ([]FileEntry, error) {
	res, err := r.call(msg{T: "req", Op: "readdir", Path: path})
	if err != nil {
		return nil, err
	}
	if res.Ents == nil {
		return []FileEntry{}, nil
	}
	return res.Ents, nil
}

func (r *Remote) ReadFile(path string, max int64) (FileData, error) {
	res, err := r.call(msg{T: "req", Op: "readfile", Path: path, Max: max})
	if err != nil {
		return FileData{}, err
	}
	if res.File == nil {
		return FileData{}, errors.New("服务器没有返回文件")
	}
	return *res.File, nil
}

func (r *Remote) IsDir(path string) bool {
	_, err := r.call(msg{T: "req", Op: "isdir", Path: path})
	return err == nil
}

// ---------------------------------------------------------------- remote process

type chunk struct {
	fd   int
	data []byte
}

type remoteProc struct {
	r          *Remote
	h          int64
	pid        int
	argv       []string
	q          chan chunk
	outR, errR *io.PipeReader
	outW, errW *io.PipeWriter
	qmu        sync.Mutex
	qclosed    bool
	done       chan struct{}
	code       int
	err        error
	stdin      *remoteStdin
}

func newRemoteProc(r *Remote, h int64) *remoteProc {
	or, ow := io.Pipe()
	er, ew := io.Pipe()
	p := &remoteProc{r: r, h: h, q: make(chan chunk, 4096), outR: or, outW: ow, errR: er, errW: ew, done: make(chan struct{})}
	p.stdin = &remoteStdin{p: p}
	// Deliver output in order without blocking the connection reader.
	go func() {
		for c := range p.q {
			if c.fd == 2 {
				p.errW.Write(c.data)
			} else {
				p.outW.Write(c.data)
			}
		}
		p.outW.Close()
		p.errW.Close()
		close(p.done)
	}()
	return p
}

// push queues output; it is dropped once the process has finished.
func (p *remoteProc) push(c chunk) {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	if !p.qclosed {
		p.q <- c // the pump drains q independently, so this cannot deadlock
	}
}

func (p *remoteProc) finish(code int, errMsg string) {
	p.qmu.Lock()
	defer p.qmu.Unlock()
	if p.qclosed {
		return
	}
	p.qclosed = true
	p.code = code
	if errMsg != "" {
		p.err = errors.New(errMsg)
	}
	close(p.q)
}

func (p *remoteProc) Pid() int              { return p.pid }
func (p *remoteProc) Argv() []string        { return p.argv }
func (p *remoteProc) Stdin() io.WriteCloser { return p.stdin }
func (p *remoteProc) Stdout() io.Reader     { return p.outR }
func (p *remoteProc) Stderr() io.Reader     { return p.errR }
func (p *remoteProc) Wait() (int, error) {
	<-p.done
	return p.code, p.err
}
func (p *remoteProc) Signal(sig syscall.Signal) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	_, err := p.r.call(msg{T: "req", Op: "signal", H: p.h, Sig: int(sig)})
	return err
}

type remoteStdin struct {
	p      *remoteProc
	mu     sync.Mutex
	closed bool
}

func (s *remoteStdin) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	if err := s.p.r.send(msg{T: "in", H: s.p.h, Data: append([]byte{}, b...)}); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (s *remoteStdin) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.p.r.send(msg{T: "eof", H: s.p.h})
}
