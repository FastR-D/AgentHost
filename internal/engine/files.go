package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agenthost/internal/node"
)

// ---------------------------------------------------------------- file viewer

type FileList struct {
	Node    string           `json:"node"`
	Path    string           `json:"path"`
	Parent  string           `json:"parent"`
	Entries []node.FileEntry `json:"entries"`
}

type FileView struct {
	Node      string    `json:"node"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"modTime"`
	Kind      string    `json:"kind"` // text | image | binary
	Mime      string    `json:"mime,omitempty"`
	Text      string    `json:"text,omitempty"`
	Truncated bool      `json:"truncated"`
}

const previewMax = 1 << 20

func (e *Engine) filePath(nd node.Node, p string) string {
	return e.resolveDir(workspaceOf(nd), p)
}

func (e *Engine) ListFiles(nodeID, path string) (*FileList, error) {
	nd, err := e.node(nodeID)
	if err != nil {
		return nil, err
	}
	p := e.filePath(nd, path)
	ents, err := nd.ReadDir(p)
	if err != nil {
		return nil, fmt.Errorf("无法读取目录 %s：%v", p, err)
	}
	return &FileList{Node: nd.Info().ID, Path: p, Parent: filepath.Dir(p), Entries: ents}, nil
}

func sniffKind(name string, data []byte) (kind, mt string) {
	mt = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if mt == "" && len(data) > 0 {
		mt = http.DetectContentType(data)
	}
	if strings.HasPrefix(mt, "image/") {
		return "image", mt
	}
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	for _, b := range head {
		if b == 0 {
			return "binary", mt
		}
	}
	// Allow a split character at the end of a truncated preview.
	if !utf8.Valid(head) && !utf8.Valid(head[:max(0, len(head)-utf8.UTFMax)]) {
		return "binary", mt
	}
	return "text", mt
}

func (e *Engine) ReadFileView(nodeID, path string) (*FileView, error) {
	nd, err := e.node(nodeID)
	if err != nil {
		return nil, err
	}
	p := e.filePath(nd, path)
	f, err := nd.ReadFile(p, previewMax)
	if err != nil {
		return nil, fmt.Errorf("无法读取 %s：%v", p, err)
	}
	v := &FileView{Node: nd.Info().ID, Path: p, Size: f.Size, ModTime: f.ModTime, Truncated: f.Truncated}
	v.Kind, v.Mime = sniffKind(p, f.Data)
	if v.Kind == "text" {
		data := f.Data
		for len(data) > 0 && !utf8.Valid(data) { // drop a split trailing character
			data = data[:len(data)-1]
		}
		v.Text = string(data)
	}
	return v, nil
}

// RawFile returns file content for download or image display.
func (e *Engine) RawFile(nodeID, path string) (string, []byte, error) {
	nd, err := e.node(nodeID)
	if err != nil {
		return "", nil, err
	}
	p := e.filePath(nd, path)
	f, err := nd.ReadFile(p, 20<<20)
	if err != nil {
		return "", nil, err
	}
	if f.Truncated {
		return "", nil, errors.New("文件超过 20 MB，无法在浏览器中查看")
	}
	_, mt := sniffKind(p, f.Data)
	if mt == "" {
		mt = "application/octet-stream"
	}
	return mt, f.Data, nil
}

// ---------------------------------------------------------------- git

type GitFile struct {
	Path   string `json:"path"`
	Status string `json:"status"` // M A D R ? U
	Staged bool   `json:"staged"`
}

type GitStatus struct {
	Repo     bool      `json:"repo"`
	Root     string    `json:"root,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Upstream string    `json:"upstream,omitempty"`
	Ahead    int       `json:"ahead"`
	Behind   int       `json:"behind"`
	Files    []GitFile `json:"files"`
	Remote   string    `json:"remote,omitempty"`
	Last     string    `json:"last,omitempty"` // last commit: hash subject (relative date)
	Error    string    `json:"error,omitempty"`
}

const gitStatusScript = `git rev-parse --show-toplevel 2>/dev/null || { echo "@@norepo"; exit 0; }
echo "@@status"; git status --porcelain=v2 --branch 2>&1 | head -500
echo "@@remote"; git remote get-url origin 2>/dev/null
echo "@@last"; git log -1 --format='%h %s (%cr)' 2>/dev/null`

func (e *Engine) GitStatus(sessionID string) (*GitStatus, error) {
	e.mu.Lock()
	s := e.sessions[sessionID]
	e.mu.Unlock()
	if s == nil {
		return nil, ErrNotFound
	}
	nd, err := e.node(s.NodeID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, out, err := e.runCheck(ctx, nd, node.Spec{Script: gitStatusScript, Dir: s.Cwd, Env: []string{"GIT_OPTIONAL_LOCKS=0", "LC_ALL=C"}})
	if err != nil && out == "" {
		return &GitStatus{Error: err.Error(), Files: []GitFile{}}, nil
	}
	return parseGitStatus(out), nil
}

func parseGitStatus(out string) *GitStatus {
	g := &GitStatus{Files: []GitFile{}}
	if strings.Contains(out, "@@norepo") {
		return g
	}
	g.Repo = true
	section := "root"
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "@@") {
			section = strings.TrimPrefix(l, "@@")
			continue
		}
		if l == "" {
			continue
		}
		switch section {
		case "root":
			g.Root = strings.TrimSpace(l)
		case "remote":
			g.Remote = strings.TrimSpace(l)
		case "last":
			g.Last = strings.TrimSpace(l)
		case "status":
			f := strings.Fields(l)
			switch {
			case strings.HasPrefix(l, "# branch.head "):
				g.Branch = strings.TrimPrefix(l, "# branch.head ")
			case strings.HasPrefix(l, "# branch.upstream "):
				g.Upstream = strings.TrimPrefix(l, "# branch.upstream ")
			case strings.HasPrefix(l, "# branch.ab ") && len(f) >= 4:
				g.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[2], "+"))
				g.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[3], "-"))
			case strings.HasPrefix(l, "1 ") && len(f) >= 9:
				g.Files = append(g.Files, gitFile(f[1], strings.Join(f[8:], " ")))
			case strings.HasPrefix(l, "2 ") && len(f) >= 10:
				path := strings.Join(f[9:], " ")
				if i := strings.Index(path, "\t"); i >= 0 {
					path = path[:i]
				}
				g.Files = append(g.Files, gitFile(f[1], path))
			case strings.HasPrefix(l, "u ") && len(f) >= 11:
				g.Files = append(g.Files, GitFile{Path: strings.Join(f[10:], " "), Status: "U"})
			case strings.HasPrefix(l, "? "):
				g.Files = append(g.Files, GitFile{Path: strings.TrimPrefix(l, "? "), Status: "?"})
			case strings.HasPrefix(l, "fatal:"):
				g.Error = l
			}
		}
	}
	return g
}

// gitFile turns a porcelain XY code into one status letter.
func gitFile(xy, path string) GitFile {
	x, y := xy[0], xy[1]
	if y != '.' {
		return GitFile{Path: path, Status: string(y), Staged: x != '.'}
	}
	return GitFile{Path: path, Status: string(x), Staged: true}
}

const gitSyncPrompt = `请把当前仓库的改动同步到远端仓库：

1. 运行 git status 和 git log 看看现在的情况。没有任何改动、也没有未推送的提交时，直接告诉我“无需同步”，不要做别的。
2. 检查改动里有没有不该提交的东西（密钥、令牌、.env、大文件、构建产物、依赖目录）。有的话不要提交它们，并在最后提醒我。
3. 暂存应该提交的文件，写一条清楚的提交信息（沿用仓库已有的风格；看不出风格就用中文简洁描述改了什么），然后提交。
4. 用 git pull --rebase 拉取远端更新。如果有冲突，停下来告诉我哪些文件冲突，不要自行强制解决。
5. git push 到当前分支的上游；没有上游时用 git push -u origin 当前分支。
6. 不要使用 --force，不要改动 git 配置，不要切换或删除分支。

最后用几句话告诉我：提交了什么、推送到了哪个远端和分支。`

// GitSync asks Codex to commit and push the session's repository.
func (e *Engine) GitSync(sessionID, note string) (json.RawMessage, error) {
	text := gitSyncPrompt
	if n := strings.TrimSpace(note); n != "" {
		text += "\n\n补充要求：" + n
	}
	st, err := e.GitStatus(sessionID)
	if err != nil {
		return nil, err
	}
	if !st.Repo || st.Root == "" {
		return nil, errors.New("这个会话的目录不是 Git 仓库")
	}
	return e.startChat(ChatRequest{SessionID: sessionID, Text: text}, "同步到 Git 远端")
}
