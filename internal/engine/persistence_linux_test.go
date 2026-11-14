package engine

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These scenarios use the production loader and filesystem, including a FIFO
// to hold an actual WriteFile in flight without adding a storage test hook.
func persistenceEngine(t *testing.T) (*Engine, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "runs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sessions"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		"runs/r.json":     `{"id":"r","sessionId":"s","status":"running","steps":[]}`,
		"sessions/s.json": `{"id":"s","title":"before","runIds":["r"]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	e, err := New(Config{DataDir: dir, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return e, filepath.Join(dir, "sessions/s.json"), filepath.Join(dir, "runs/r.json")
}

func TestFlushRetriesSessionAndRun(t *testing.T) {
	e, sessionPath, runPath := persistenceEngine(t)
	title := "after"
	if _, err := e.UpdateSession("s", &title, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{sessionPath, runPath} {
		if err := os.Mkdir(path+".tmp", 0755); err != nil {
			t.Fatal(err)
		}
	}
	e.Flush()
	for _, path := range []string{sessionPath, runPath} {
		if err := os.Remove(path + ".tmp"); err != nil {
			t.Fatal(err)
		}
	}
	e.Flush() // no business mutation after recovery
	reloaded, err := New(e.Config())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(reloaded.ListSessions()), `"title":"after"`) {
		t.Error("failed session write was not retried")
	}
	data, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatal(err)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusInterrupted {
		t.Errorf("failed run write was not retried: %s", run.Status)
	}
}

func TestFlushOrdersUpdatesAndDeletion(t *testing.T) {
	for _, operation := range []string{"flush", "session", "run", "cleanup"} {
		t.Run(operation, func(t *testing.T) {
			e, path, runPath := persistenceEngine(t)
			e.Flush()
			title := strings.Repeat("x", 2<<20) // larger than the FIFO buffer
			if _, err := e.UpdateSession("s", &title, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(path+".tmp", 0600); err != nil {
				t.Fatal(err)
			}
			first := make(chan struct{})
			go func() { e.Flush(); close(first) }()
			reader, err := os.Open(path + ".tmp")
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if _, err := io.ReadFull(reader, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			// WriteFile has the FIFO open and is blocked on its full buffer.
			// Replace only the directory entry so its later rename remains real.
			if err := os.Remove(path + ".tmp"); err != nil {
				t.Fatal(err)
			}
			old, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".tmp", old, 0644); err != nil {
				t.Fatal(err)
			}
			title = "latest"
			if _, err := e.UpdateSession("s", &title, nil, nil); err != nil {
				t.Fatal(err)
			}
			second := make(chan error, 1)
			go func() {
				switch operation {
				case "flush":
					e.Flush()
				case "session":
					second <- e.DeleteSession("s")
					return
				case "run":
					second <- e.DeleteRun("r")
					return
				case "cleanup":
					if n := e.CleanupRuns("", 0, false); n != 1 {
						second <- io.ErrUnexpectedEOF
						return
					}
				}
				second <- nil
			}()
			var early bool
			select {
			case err := <-second:
				early = true
				if err != nil {
					t.Error(err)
				}
				t.Error("commit overtook an in-flight Flush")
			case <-time.After(100 * time.Millisecond):
			}
			if _, err := io.Copy(io.Discard, reader); err != nil {
				t.Fatal(err)
			}
			<-first
			if !early {
				if err := <-second; err != nil {
					t.Fatal(err)
				}
			}
			e.Flush()
			reloaded, err := New(e.Config())
			if err != nil {
				t.Fatal(err)
			}
			if operation == "flush" {
				if !strings.Contains(string(reloaded.ListSessions()), `"title":"latest"`) {
					t.Error("update during I/O lost on reload")
				}
			} else {
				for _, file := range []string{path, runPath} {
					if _, err := os.Stat(file); !os.IsNotExist(err) {
						t.Errorf("deleted file resurrected: %s (%v)", file, err)
					}
				}
				if string(reloaded.ListSessions()) != "[]" || string(reloaded.ListRuns(RunFilter{})) != "[]" {
					t.Error("deleted records returned on reload")
				}
			}
		})
	}
}

func TestDeleteSessionWaitsForActiveRun(t *testing.T) {
	e := newTestEngine(t)
	s, err := e.CreateSession("active", "", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "SLOW"})
	if err != nil {
		t.Fatal(err)
	}
	id := runIDOf(t, raw)
	waitFor(t, "active turn", func() bool { return e.snapshot(id).Steps[0].TurnID != "" })
	done := make(chan error, 1)
	go func() { done <- e.DeleteSession(s.ID) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deletion and finishing Flush deadlocked")
	}
	e.Flush()
	reloaded, err := New(e.Config())
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded.ListSessions()) != "[]" || string(reloaded.ListRuns(RunFilter{})) != "[]" {
		t.Fatal("active run survived session deletion")
	}
}
