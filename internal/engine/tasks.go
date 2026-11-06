package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Task is a reusable task description: plain Markdown that Codex follows,
// plus (optionally) the server and directory it usually runs in. It is
// stored as <dir>/<name>.md with an optional front matter:
//
//	---
//	名称: 项目体检
//	服务器: local
//	目录: /work/app
//	---
//	正文……
type Task struct {
	Name      string    `json:"name"` // file name without .md; stable id
	Title     string    `json:"title"`
	Node      string    `json:"node,omitempty"` // server id
	Cwd       string    `json:"cwd,omitempty"`
	Parallel  int       `json:"parallel,omitempty"` // Codex working on it at once; 0 = one
	Body      string    `json:"body"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TaskLib struct {
	mu  sync.Mutex
	dir string
}

// LegacyDirs are the libraries of earlier AgentHost versions; the user's
// own files there become tasks the first time the task library is opened.
type LegacyDirs struct {
	Workflows string // Markdown workflows
	Skills    string // the old "技能库" (<name>/SKILL.md)
}

// OpenTaskLib opens dir. The first time, it installs the example tasks from
// seed and brings over the user's files from the legacy libraries (which are
// left in place).
func OpenTaskLib(dir string, seed fs.FS, legacy LegacyDirs) (*TaskLib, error) {
	_, err := os.Stat(dir)
	fresh := os.IsNotExist(err)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &TaskLib{dir: dir}
	if fresh {
		if seed != nil {
			entries, _ := fs.ReadDir(seed, ".")
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				if b, err := fs.ReadFile(seed, e.Name()); err == nil {
					_ = os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
				}
			}
		}
		if n := l.migrate(legacy); n > 0 {
			slog.Info("moved earlier workflows / skills into the task library", "count", n, "dir", dir)
		}
	}
	return l, nil
}

func (l *TaskLib) Dir() string { return l.dir }

// ---------------------------------------------------------------- format

func splitFrontmatter(text string) (fm, body string, ok bool) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text, false
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", text, false
	}
	rest := text[4+end+4:]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		rest = ""
	}
	return text[4 : 4+end], rest, true
}

func pick(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstHeading(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "# "))
		}
	}
	return ""
}

// parseTask reads a task file. Front matter keys may be Chinese or English.
func parseTask(name string, b []byte) Task {
	t := Task{Name: name}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if fm, body, ok := splitFrontmatter(text); ok {
		var m map[string]any
		if yaml.Unmarshal([]byte(fm), &m) == nil {
			t.Title = pick(m, "名称", "标题", "title", "name")
			t.Node = pick(m, "服务器", "server", "node")
			t.Cwd = pick(m, "目录", "cwd", "dir")
			if n, err := strconv.Atoi(pick(m, "并行", "parallel")); err == nil {
				t.Parallel = clampParallel(n)
			}
			text = body
		}
	}
	t.Body = strings.TrimSpace(text)
	if t.Title == "" {
		t.Title = firstHeading(t.Body)
	}
	if t.Title == "" {
		t.Title = name
	}
	return t
}

type taskFront struct {
	Title    string `yaml:"名称"`
	Node     string `yaml:"服务器,omitempty"`
	Cwd      string `yaml:"目录,omitempty"`
	Parallel int    `yaml:"并行,omitempty"`
}

func renderTask(t Task) []byte {
	y, _ := yaml.Marshal(taskFront{Title: t.Title, Node: t.Node, Cwd: t.Cwd, Parallel: clampParallel(t.Parallel)})
	return []byte("---\n" + string(y) + "---\n\n" + strings.TrimSpace(t.Body) + "\n")
}

// ---------------------------------------------------------------- names

// CleanTaskName validates a task name (the file name, ".md" optional).
func CleanTaskName(name string) (string, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".md")
	if name == "" || strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") || strings.Contains(name, "..") {
		return "", ErrNotFound
	}
	return name, nil
}

var reNameJunk = regexp.MustCompile(`[\x00-\x1f/\\:*?"<>|#%&]+`)

func taskSlug(title string) string {
	s := reNameJunk.ReplaceAllString(title, " ")
	s = strings.Trim(strings.Join(strings.Fields(s), "-"), ".-")
	if utf8.RuneCountInString(s) > 60 {
		s = strings.Trim(string([]rune(s)[:60]), ".-")
	}
	if s == "" {
		s = "任务"
	}
	return s
}

// uniqueName returns base, or base-2, base-3… if taken. Call with l.mu held.
func (l *TaskLib) uniqueName(base string) string {
	name := base
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(l.dir, name+".md")); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
}

// ---------------------------------------------------------------- CRUD

func (l *TaskLib) List() []Task {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, _ := os.ReadDir(l.dir)
	out := []Task{}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".md") {
			continue
		}
		p := filepath.Join(l.dir, n)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		t := parseTask(strings.TrimSuffix(n, ".md"), b)
		if fi, err := os.Stat(p); err == nil {
			t.UpdatedAt = fi.ModTime()
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (l *TaskLib) Get(name string) (Task, error) {
	name, err := CleanTaskName(name)
	if err != nil {
		return Task{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	p := filepath.Join(l.dir, name+".md")
	b, err := os.ReadFile(p)
	if err != nil {
		return Task{}, ErrNotFound
	}
	t := parseTask(name, b)
	if fi, err := os.Stat(p); err == nil {
		t.UpdatedAt = fi.ModTime()
	}
	return t, nil
}

// Save creates (empty Name) or updates a task.
func (l *TaskLib) Save(t Task) (Task, error) {
	t.Title = strings.TrimSpace(strings.ReplaceAll(t.Title, "\n", " "))
	t.Node, t.Cwd, t.Parallel = strings.TrimSpace(t.Node), strings.TrimSpace(t.Cwd), clampParallel(t.Parallel)
	t.Body = strings.TrimSpace(strings.ReplaceAll(t.Body, "\r\n", "\n"))
	if t.Title == "" {
		return Task{}, errors.New("请给任务起个名字")
	}
	if t.Body == "" {
		return Task{}, errors.New("请写下这个任务要做什么")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.Name == "" {
		t.Name = l.uniqueName(taskSlug(t.Title))
	} else {
		name, err := CleanTaskName(t.Name)
		if err != nil {
			return Task{}, errors.New("任务名不合法")
		}
		t.Name = name
	}
	if err := writeFileAtomic(filepath.Join(l.dir, t.Name+".md"), renderTask(t)); err != nil {
		return Task{}, err
	}
	t.UpdatedAt = time.Now()
	return t, nil
}

func (l *TaskLib) Delete(name string) error {
	name, err := CleanTaskName(name)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err = os.Remove(filepath.Join(l.dir, name+".md"))
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}

// ---------------------------------------------------------------- migration

// Example skills shipped by the previous version; these are not migrated
// (the example tasks replace them).
var oldSkillExamples = map[string]string{
	"code-review":     "cdc9ec1b8fb6d6b60e9d4d7ad82afcd9381cc7587fc31fa10778f48fb8c9ed93",
	"project-checkup": "b7d6d7bb0d6cbd2b1dc8514debe806eb749258c9c0312529ee16e1ad2007b821",
	"server-check":    "4f9bfc89435caf1ec67d9262ad64374a03583d6650848c0db544bb44134ac7b0",
}

func (l *TaskLib) migrate(old LegacyDirs) int {
	n := 0
	add := func(t Task, mod time.Time) {
		if strings.TrimSpace(t.Body) == "" {
			return
		}
		l.mu.Lock()
		t.Name = l.uniqueName(taskSlug(t.Name))
		p := filepath.Join(l.dir, t.Name+".md")
		err := os.WriteFile(p, renderTask(t), 0o644)
		l.mu.Unlock()
		if err == nil {
			_ = os.Chtimes(p, mod, mod)
			n++
		}
	}
	if old.Workflows != "" {
		manifest := map[string]string{}
		if b, err := os.ReadFile(filepath.Join(old.Workflows, exampleManifest)); err == nil {
			_ = json.Unmarshal(b, &manifest)
		}
		entries, _ := os.ReadDir(old.Workflows)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(strings.ToLower(name), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(old.Workflows, name))
			if err != nil {
				continue
			}
			h := sha(b)
			if manifest[name] == h || legacyExamples[name] == h {
				continue // an untouched example
			}
			var mod time.Time
			if fi, err := e.Info(); err == nil {
				mod = fi.ModTime()
			}
			add(taskFromWorkflow(name, b), mod)
		}
	}
	if old.Skills != "" {
		entries, _ := os.ReadDir(old.Skills)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(old.Skills, e.Name(), "SKILL.md")
			b, err := os.ReadFile(p)
			if err != nil || oldSkillExamples[e.Name()] == sha(b) {
				continue
			}
			var mod time.Time
			if fi, err := os.Stat(p); err == nil {
				mod = fi.ModTime()
			}
			add(taskFromOldSkill(e.Name(), b), mod)
		}
	}
	return n
}

// taskFromWorkflow keeps a workflow's text; its name and directory become the
// task's, and variables are written out so the text still makes sense.
func taskFromWorkflow(file string, b []byte) Task {
	t := Task{Name: strings.TrimSuffix(file, filepath.Ext(file))}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	var vars []string
	if fm, body, ok := splitFrontmatter(text); ok {
		var f struct {
			Name string         `yaml:"name"`
			Cwd  string         `yaml:"cwd"`
			Vars map[string]any `yaml:"vars"`
		}
		if yaml.Unmarshal([]byte(fm), &f) == nil {
			t.Title, t.Cwd, text = strings.TrimSpace(f.Name), strings.TrimSpace(f.Cwd), body
			for k, v := range f.Vars {
				vars = append(vars, fmt.Sprintf("- %s = %v", k, v))
			}
		}
	}
	sort.Strings(vars)
	t.Body = strings.TrimSpace(text)
	if len(vars) > 0 {
		t.Body += "\n\n变量（文中的 {{vars.名字}} 指这些值）：\n" + strings.Join(vars, "\n")
	}
	if t.Title == "" {
		t.Title = firstHeading(t.Body)
	}
	if t.Title == "" {
		t.Title = t.Name
	}
	return t
}

func taskFromOldSkill(name string, b []byte) Task {
	t := Task{Name: name, Title: name}
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	if fm, body, ok := splitFrontmatter(text); ok {
		var f struct {
			Metadata struct {
				ShortDescription string `yaml:"short-description"`
			} `yaml:"metadata"`
		}
		if yaml.Unmarshal([]byte(fm), &f) == nil {
			if s := strings.TrimSpace(f.Metadata.ShortDescription); s != "" {
				t.Title = s
			}
			text = body
		}
	}
	t.Body = strings.TrimSpace(text)
	return t
}
