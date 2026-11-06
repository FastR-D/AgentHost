package engine

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// store persists sessions and runs as JSON files and logs as JSONL.
type store struct {
	dir   string
	logMu sync.Mutex
	logs  map[string]*os.File
}

func newStore(dir string) (*store, error) {
	for _, d := range []string{"sessions", "runs", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return nil, err
		}
	}
	return &store{dir: dir, logs: map[string]*os.File{}}, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *store) saveRaw(kind, id string, data []byte) error {
	return writeFileAtomic(filepath.Join(s.dir, kind, id+".json"), data)
}

func (s *store) remove(kind, id string) {
	_ = os.Remove(filepath.Join(s.dir, kind, id+".json"))
	if kind == "runs" {
		s.logMu.Lock()
		if f := s.logs[id]; f != nil {
			f.Close()
			delete(s.logs, id)
		}
		s.logMu.Unlock()
		_ = os.Remove(filepath.Join(s.dir, "logs", id+".jsonl"))
	}
}

func loadAll[T any](s *store, kind string) ([]*T, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, kind))
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out []*T
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, kind, e.Name()))
		if err != nil {
			continue
		}
		v := new(T)
		if err := json.Unmarshal(b, v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *store) appendLog(e *LogEntry) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	f := s.logs[e.RunID]
	if f == nil {
		var err error
		f, err = os.OpenFile(filepath.Join(s.dir, "logs", e.RunID+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		s.logs[e.RunID] = f
	}
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
}

func (s *store) closeLog(runID string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if f := s.logs[runID]; f != nil {
		f.Close()
		delete(s.logs, runID)
	}
}

// readLogs returns log entries with Seq > after, up to limit entries.
func (s *store) readLogs(runID string, after int64, limit int) ([]*LogEntry, error) {
	f, err := os.Open(filepath.Join(s.dir, "logs", runID+".jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return []*LogEntry{}, nil
		}
		return nil, err
	}
	defer f.Close()
	out := []*LogEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e LogEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Seq <= after {
			continue
		}
		out = append(out, &e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
