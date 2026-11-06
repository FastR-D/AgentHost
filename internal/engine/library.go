package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agenthost/internal/workflow"
)

// Library is a directory of Markdown workflow files.
type Library struct {
	dir string
	mu  sync.Mutex
}

type WorkflowInfo struct {
	Name        string    `json:"name"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Steps       int       `json:"steps"`
	Error       string    `json:"error,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Size        int64     `json:"size"`
}

// NewLibrary opens dir and installs or upgrades the bundled example workflows.
func NewLibrary(dir string, seed fs.FS) (*Library, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Library{dir: dir}
	if seed != nil {
		syncExamples(dir, seed)
	}
	return l, nil
}

// exampleManifest records, per example file, the hash of the version
// AgentHost last wrote. A file whose content still has that hash has not been
// edited by the user and may be upgraded; a recorded file that is missing was
// deleted on purpose and is not restored.
const exampleManifest = ".examples.json"

// legacyExamples are hashes of examples shipped before the manifest existed
// (AgentHost 0.1.0), so libraries created then can be upgraded too.
var legacyExamples = map[string]string{
	"call-subworkflow.md":     "94fd9d4add315da89f04042db6c8e77479f735efb585b5bd7fd2aed12f851207",
	"hello-world.md":          "a271a60cffe14c770564ab9c207e96ed86ff7ef0042c62a2e3ddc4f98e7cabea",
	"project-health-check.md": "ccef1b11af9dd2da73ee98c782751189d8697f8a0a669bbd290beb8cd49df3c0",
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func syncExamples(dir string, seed fs.FS) {
	mpath := filepath.Join(dir, exampleManifest)
	manifest := map[string]string{}
	if b, err := os.ReadFile(mpath); err == nil {
		_ = json.Unmarshal(b, &manifest)
	}
	_, legacy := os.Stat(filepath.Join(dir, ".seeded"))
	fresh := len(manifest) == 0 && os.IsNotExist(legacy)
	changed := false
	entries, _ := fs.ReadDir(seed, ".")
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		want, err := fs.ReadFile(seed, e.Name())
		if err != nil {
			continue
		}
		dst := filepath.Join(dir, e.Name())
		have, err := os.ReadFile(dst)
		recorded, known := manifest[e.Name()]
		if !known {
			recorded, known = legacyExamples[e.Name()]
		}
		switch {
		case os.IsNotExist(err):
			// Never resurrect an example the user deleted.
			if known && !fresh {
				continue
			}
		case err != nil:
			continue
		case !known || sha(have) != recorded:
			continue // the user's own file, or an example they edited
		case sha(have) == sha(want):
			continue // already current
		}
		if os.WriteFile(dst, want, 0o644) == nil {
			manifest[e.Name()] = sha(want)
			changed = true
		}
	}
	if changed {
		if b, err := json.MarshalIndent(manifest, "", "  "); err == nil {
			_ = os.WriteFile(mpath, b, 0o644)
		}
	}
}

func (l *Library) Dir() string { return l.dir }

// CleanName validates a workflow file name and adds the .md suffix.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("workflow 名称不能为空")
	}
	if strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return "", fmt.Errorf("非法的 workflow 名称 %q", name)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	return name, nil
}

func (l *Library) List() []*WorkflowInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, _ := os.ReadDir(l.dir)
	out := []*WorkflowInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		info := &WorkflowInfo{Name: e.Name(), UpdatedAt: fi.ModTime(), Size: fi.Size(), Title: strings.TrimSuffix(e.Name(), ".md")}
		b, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err == nil {
			if wf, err := workflow.Parse(string(b), info.Title); err == nil {
				info.Title, info.Description, info.Steps = wf.Name, wf.Description, len(wf.Steps)
			} else {
				info.Error = err.Error()
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (l *Library) Read(name string) (string, error) {
	name, err := CleanName(name)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(l.dir, name))
	if os.IsNotExist(err) {
		return "", ErrNotFound
	}
	return string(b), err
}

// Save validates and writes a workflow.
func (l *Library) Save(name, source string) (string, error) {
	name, err := CleanName(name)
	if err != nil {
		return "", err
	}
	if _, err := workflow.Parse(source, strings.TrimSuffix(name, ".md")); err != nil {
		return "", fmt.Errorf("workflow 校验失败：%w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return name, writeFileAtomic(filepath.Join(l.dir, name), []byte(source))
}

func (l *Library) Delete(name string) error {
	name, err := CleanName(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err = os.Remove(filepath.Join(l.dir, name))
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}
