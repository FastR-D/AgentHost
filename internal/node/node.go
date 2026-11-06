// Package node abstracts the machines tasks run on. The web server always
// has a "local" node; other servers run `agenthost node`, which dials out to
// the web server over a WebSocket, so they work behind NAT or a firewall
// without any inbound port.
package node

import (
	"errors"
	"io"
	"syscall"
	"time"
)

// LocalID is the id of the machine the web server runs on.
const LocalID = "local"

var (
	ErrOffline = errors.New("服务器不在线")
	ErrUnknown = errors.New("没有这台服务器")
	ErrLost    = errors.New("与服务器的连接中断")
)

// Spec describes a process to start on a node.
type Spec struct {
	Argv []string `json:"argv,omitempty"`
	// Script, when set, runs with bash (or sh if bash is missing) on the node; Argv is ignored.
	Script string   `json:"script,omitempty"`
	Dir    string   `json:"dir"`
	Env    []string `json:"env,omitempty"` // added to the node's own environment
}

// Proc is a running process. Stdout and Stderr must be read until EOF.
type Proc interface {
	Pid() int
	Argv() []string // what actually runs, for display
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	// Signal sends sig to the process group.
	Signal(sig syscall.Signal) error
	// Wait blocks until the process exits. code is -1 if it was killed or lost.
	Wait() (code int, err error)
}

// DirList is the result of listing a directory.
type DirList struct {
	Path   string   `json:"path"`
	Parent string   `json:"parent"`
	Dirs   []string `json:"dirs"`
}

// FileEntry is one item of a directory listing.
type FileEntry struct {
	Name    string    `json:"name"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Link    bool      `json:"link,omitempty"`
}

// FileData is (the beginning of) a file.
type FileData struct {
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"modTime"`
	Data      []byte    `json:"data"`
	Truncated bool      `json:"truncated"`
}

// Info describes a node for the UI and for the engine.
type Info struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Local        bool      `json:"local"`
	Online       bool      `json:"online"`
	Hostname     string    `json:"hostname,omitempty"`
	OS           string    `json:"os,omitempty"`
	Arch         string    `json:"arch,omitempty"`
	Version      string    `json:"version,omitempty"`
	CodexBin     string    `json:"codexBin,omitempty"`
	CodexVersion string    `json:"codexVersion,omitempty"`
	Workspace    string    `json:"workspace,omitempty"`
	DataDir      string    `json:"dataDir,omitempty"`
	Addr         string    `json:"addr,omitempty"`
	ConnectedAt  time.Time `json:"connectedAt,omitempty"`
	LastSeen     time.Time `json:"lastSeen,omitempty"`
	Procs        int       `json:"procs"`
}

// Node runs processes and answers simple filesystem questions.
type Node interface {
	Info() Info
	Start(spec Spec) (Proc, error)
	ListDir(path string) (DirList, error)
	MkdirAll(path string) error
	IsDir(path string) bool
	WriteFile(path string, data []byte, mode uint32) error
	ReadDir(path string) ([]FileEntry, error)
	ReadFile(path string, max int64) (FileData, error)
}

// offline stands in for a known remote node that is not connected.
type offline struct{ info Info }

func (o *offline) Info() Info                               { i := o.info; i.Online = false; return i }
func (o *offline) Start(Spec) (Proc, error)                 { return nil, ErrOffline }
func (o *offline) ListDir(string) (DirList, error)          { return DirList{}, ErrOffline }
func (o *offline) MkdirAll(string) error                    { return ErrOffline }
func (o *offline) IsDir(string) bool                        { return false }
func (o *offline) WriteFile(string, []byte, uint32) error   { return ErrOffline }
func (o *offline) ReadDir(string) ([]FileEntry, error)      { return nil, ErrOffline }
func (o *offline) ReadFile(string, int64) (FileData, error) { return FileData{}, ErrOffline }
