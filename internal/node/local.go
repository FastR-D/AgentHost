package node

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Local runs processes on this machine.
type Local struct {
	mu   sync.Mutex
	info Info
	n    int
}

func NewLocal(info Info) *Local {
	info.ID, info.Local, info.Online = LocalID, true, true
	if info.Name == "" {
		info.Name = "本机"
	}
	return &Local{info: info}
}

func (l *Local) Info() Info {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := l.info
	i.Procs = l.n
	return i
}

func (l *Local) SetInfo(fn func(*Info)) {
	l.mu.Lock()
	fn(&l.info)
	l.mu.Unlock()
}

func shell() string {
	for _, p := range []string{"/bin/bash", "/usr/bin/bash", "/usr/local/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "sh"
}

type localProc struct {
	cmd            *exec.Cmd
	argv           []string
	stdin          io.WriteCloser
	stdout, stderr *io.PipeReader
	done           chan struct{}
	code           int
	err            error
	onExit         func()
}

func (l *Local) Start(spec Spec) (Proc, error) {
	argv := spec.Argv
	if spec.Script != "" {
		argv = []string{shell(), "-c", spec.Script}
	}
	if len(argv) == 0 {
		return nil, errors.New("没有要执行的命令")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// io.Pipe instead of cmd.StdoutPipe: readers see EOF after Wait has
	// collected everything, so they never race with Wait.
	or, ow := io.Pipe()
	er, ew := io.Pipe()
	cmd.Stdout, cmd.Stderr = ow, ew
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &localProc{cmd: cmd, argv: argv, stdin: stdin, stdout: or, stderr: er, done: make(chan struct{})}
	l.mu.Lock()
	l.n++
	l.mu.Unlock()
	go func() {
		p.err = cmd.Wait()
		p.code = -1
		if cmd.ProcessState != nil {
			p.code = cmd.ProcessState.ExitCode()
		}
		ow.Close()
		ew.Close()
		l.mu.Lock()
		l.n--
		l.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *localProc) Pid() int              { return p.cmd.Process.Pid }
func (p *localProc) Argv() []string        { return p.argv }
func (p *localProc) Stdin() io.WriteCloser { return p.stdin }
func (p *localProc) Stdout() io.Reader     { return p.stdout }
func (p *localProc) Stderr() io.Reader     { return p.stderr }
func (p *localProc) Wait() (int, error) {
	<-p.done
	var ee *exec.ExitError
	if errors.As(p.err, &ee) {
		return p.code, nil // a non-zero exit is reported through code
	}
	return p.code, p.err
}
func (p *localProc) Signal(sig syscall.Signal) error {
	select {
	case <-p.done:
		return nil
	default:
	}
	if err := syscall.Kill(-p.cmd.Process.Pid, sig); err != nil {
		return p.cmd.Process.Signal(sig)
	}
	return nil
}

func (l *Local) ListDir(path string) (DirList, error) { return listDir(path, l.Info().Workspace) }
func (l *Local) MkdirAll(path string) error           { return os.MkdirAll(path, 0o755) }
func (l *Local) IsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func (l *Local) WriteFile(path string, data []byte, mode uint32) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, os.FileMode(mode)); err != nil {
		return err
	}
	if err := os.Chmod(tmp, os.FileMode(mode)); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (l *Local) ReadDir(path string) ([]FileEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		fe := FileEntry{Name: e.Name(), Dir: e.IsDir(), Link: e.Type()&os.ModeSymlink != 0}
		if fi, err := os.Stat(filepath.Join(path, e.Name())); err == nil { // follow links
			fe.Dir, fe.Size, fe.ModTime = fi.IsDir(), fi.Size(), fi.ModTime()
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (l *Local) ReadFile(path string, max int64) (FileData, error) {
	f, err := os.Open(path)
	if err != nil {
		return FileData{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return FileData{}, err
	}
	if fi.IsDir() {
		return FileData{}, errors.New("这是一个目录")
	}
	if max <= 0 || max > 32<<20 {
		max = 32 << 20
	}
	data, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return FileData{}, err
	}
	return FileData{Path: path, Size: fi.Size(), ModTime: fi.ModTime(), Data: data, Truncated: fi.Size() > int64(len(data))}, nil
}

func listDir(path, def string) (DirList, error) {
	if path == "" {
		path = def
	}
	if strings.HasPrefix(path, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return DirList{}, err
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return DirList{Path: path, Parent: filepath.Dir(path), Dirs: dirs}, nil
}
