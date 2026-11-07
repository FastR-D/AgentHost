package engine

import (
	"errors"
	"fmt"
	"io/fs"
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

// OpenTaskLib opens dir. The first time, it installs the example tasks from seed.
func OpenTaskLib(dir string, seed fs.FS) (*TaskLib, error) {
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
			pval := pick(m, "并行", "parallel")
			if pval == "不限" || pval == "unlimited" || pval == "auto" {
				t.Parallel = parallelUnlimited
			} else if n, err := strconv.Atoi(pval); err == nil {
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
	Parallel any    `yaml:"并行,omitempty"` // int or "不限"
}

func parallelFrontVal(n int) any {
	if n == parallelUnlimited {
		return "不限"
	}
	if n <= 1 {
		return nil
	}
	return n
}

func renderTask(t Task) []byte {
	y, _ := yaml.Marshal(taskFront{Title: t.Title, Node: t.Node, Cwd: t.Cwd, Parallel: parallelFrontVal(t.Parallel)})
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
