package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"agenthost/internal/codex"
	"agenthost/internal/node"
)

// Skills live in the user's code: any folder with a SKILL.md. AgentHost
// never installs them; when a task starts it looks for them in the task's
// repository and hands them to that one Codex process only
// (skills/extraRoots/set), so ~/.codex and config.toml are never touched.

// RepoSkill is a skill found in a directory on a server.
type RepoSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"` // absolute path of SKILL.md
	Rel         string `json:"rel"`  // folder relative to the scanned root
	// Native skills sit where Codex looks by itself (.agents/skills or
	// .codex/skills between the repository root and the working directory).
	Native bool `json:"native,omitempty"`
}

// SkillScan is the result of looking for skills.
type SkillScan struct {
	Root      string      `json:"root"` // repository root, or the directory itself
	Skills    []RepoSkill `json:"skills"`
	Truncated bool        `json:"truncated,omitempty"`
	Error     string      `json:"error,omitempty"`
}

const (
	skillMark      = "@@AGENTHOST-SKILL@@ "
	skillRootMark  = "@@AGENTHOST-ROOT@@ "
	skillGitMark   = "@@AGENTHOST-GIT@@ "
	maxSkills      = 100
	skillScanDepth = 7
)

// Folders that hold dependencies, build output or caches: skills there
// belong to someone else's package, not to the task.
var skillPrune = []string{
	"node_modules", "vendor", "dist", "build", "target", "out", ".venv", "venv", "__pycache__",
	"site-packages", ".cache", ".npm", ".pnpm-store", ".yarn", ".gradle", ".m2", ".cargo", ".rustup",
	".next", ".nuxt", ".tox", ".mypy_cache", ".pytest_cache", ".terraform", ".idea", ".vscode",
}

// skillScanScript prints the scan root, then every SKILL.md with the start
// of its content. It scans the whole repository when the directory is inside
// one, so a task in a sub-folder still finds the repository's skills. Outside
// a repository it also reports the repositories below, whose skills belong to
// those projects rather than to this directory.
func skillScanScript() string {
	var prune []string
	for _, p := range skillPrune {
		prune = append(prune, "-name "+shellQuote(p))
	}
	return fmt.Sprintf(`dir=$(pwd)
inrepo=1
root=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null) || { root="$dir"; inrepo=; }
[ -n "$root" ] && [ "$root" != "/" ] || { root="$dir"; inrepo=; }
# A repository that holds the whole workspace is not this task's project.
ws=${AH_WORKSPACE%%/}
if [ -n "$inrepo" ] && [ -n "$ws" ]; then
  case "$dir/" in "$ws"/*) case "$ws/" in "$root"/?*) root="$dir"; inrepo= ;; esac ;; esac
fi
printf '%%s%%s\n' %[1]s "$root"
cd "$root" 2>/dev/null || exit 0
n=0
find . -maxdepth %[4]d \( -name .git -print -prune \) -o \( %[5]s \) -prune -o -type f -name SKILL.md -print 2>/dev/null | while IFS= read -r f; do
  case "$f" in
  */.git) [ -n "$inrepo" ] || printf '%%s%%s\n' %[3]s "${f#./}" ;;
  *)
    n=$((n+1)); [ "$n" -gt %[6]d ] && break
    printf '%%s%%s\n' %[2]s "${f#./}"
    head -c 4000 "$f" 2>/dev/null | head -n 60
    printf '\n' ;;
  esac
done`, shellQuote(skillRootMark), shellQuote(skillMark), shellQuote(skillGitMark), skillScanDepth, strings.Join(prune, " -o "), maxSkills+1)
}

// DiscoverSkills lists the skills in a directory on a server, for the UI.
func (e *Engine) DiscoverSkills(nodeID, dir string) (*SkillScan, error) {
	nd, err := e.node(nodeID)
	if err != nil {
		return nil, err
	}
	dir = e.resolveDir(workspaceOf(nd), dir)
	if !nd.IsDir(dir) {
		return &SkillScan{Root: dir, Skills: []RepoSkill{}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return e.discoverSkills(ctx, nd, dir), nil
}

func (e *Engine) discoverSkills(ctx context.Context, nd node.Node, dir string) *SkillScan {
	info := nd.Info()
	env := []string{"GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "AH_WORKSPACE=" + info.Workspace}
	_, out, err := e.runScript(ctx, nd, node.Spec{Script: skillScanScript(), Dir: dir, Env: env}, 1<<20)
	// AgentHost's own data (old libraries, copies) is never the task's code.
	exclude := []string{info.DataDir}
	if info.Local {
		exclude = append(exclude, e.cfg.DataDir)
	}
	scan := parseSkillScan(out, dir, exclude...)
	if err != nil && len(scan.Skills) == 0 {
		scan.Error = err.Error()
	}
	return scan
}

func parseSkillScan(out, dir string, exclude ...string) *SkillScan {
	scan := &SkillScan{Root: dir, Skills: []RepoSkill{}}
	var nested []string // other repositories below a directory that is not one
	var cur *RepoSkill
	var body strings.Builder
	finish := func() {
		if cur == nil {
			return
		}
		name, desc := skillFrontmatter(body.String())
		if name == "" {
			name = filepath.Base(filepath.Dir(cur.Path))
		}
		cur.Name, cur.Description = name, desc
		scan.Skills = append(scan.Skills, *cur)
		cur = nil
		body.Reset()
	}
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, skillRootMark):
			if r := strings.TrimSpace(strings.TrimPrefix(l, skillRootMark)); r != "" {
				scan.Root = r
			}
		case strings.HasPrefix(l, skillGitMark):
			finish()
			if d := filepath.Dir(strings.TrimPrefix(l, skillGitMark)); d != "." {
				nested = append(nested, d+"/")
			}
		case strings.HasPrefix(l, skillMark):
			finish()
			rel := strings.TrimPrefix(l, skillMark)
			folder := filepath.Dir(rel)
			if folder == "." {
				folder = ""
			}
			cur = &RepoSkill{Path: filepath.Join(scan.Root, rel), Rel: folder}
		case cur != nil:
			body.WriteString(l)
			body.WriteByte('\n')
		}
	}
	finish()
	if len(nested) > 0 || len(exclude) > 0 {
		kept := scan.Skills[:0]
	skills:
		for _, s := range scan.Skills {
			for _, n := range nested {
				if strings.HasPrefix(s.Rel+"/", n) {
					continue skills
				}
			}
			for _, x := range exclude {
				if x != "" && strings.HasPrefix(s.Path, strings.TrimSuffix(x, "/")+"/") {
					continue skills
				}
			}
			kept = append(kept, s)
		}
		scan.Skills = kept
	}
	if len(scan.Skills) > maxSkills {
		scan.Skills, scan.Truncated = scan.Skills[:maxSkills], true
	}
	// Mark the folders Codex reads by itself: .agents/skills and .codex/skills
	// in the working directory or any parent up to the repository root.
	for i := range scan.Skills {
		s := &scan.Skills[i]
		for _, base := range []string{".agents/skills/", ".codex/skills/"} {
			j := strings.Index("/"+s.Rel+"/", "/"+base)
			if j < 0 {
				continue
			}
			owner := filepath.Join(scan.Root, s.Rel[:j])
			if owner == dir || strings.HasPrefix(dir+"/", strings.TrimSuffix(owner, "/")+"/") {
				s.Native = true
			}
		}
	}
	sort.SliceStable(scan.Skills, func(i, j int) bool { return scan.Skills[i].Rel < scan.Skills[j].Rel })
	return scan
}

// skillFrontmatter reads name and description from the start of a SKILL.md.
func skillFrontmatter(text string) (name, desc string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", ""
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return "", ""
	}
	var f struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal([]byte(text[4:4+end]), &f) != nil {
		return "", ""
	}
	return strings.TrimSpace(f.Name), strings.TrimSpace(f.Description)
}

// ---------------------------------------------------------------- injection into one Codex process

// codexSkill is an entry of skills/list.
type codexSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
}

func listCodexSkills(ctx context.Context, client *codex.Client, cwd string, reload bool) ([]codexSkill, []string, error) {
	var res struct {
		Data []struct {
			Skills []codexSkill `json:"skills"`
			Errors []struct {
				Path    string `json:"path"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"data"`
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := client.Call(cctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": reload}, &res); err != nil {
		return nil, nil, err
	}
	var skills []codexSkill
	var errs []string
	for _, d := range res.Data {
		skills = append(skills, d.Skills...)
		for _, e := range d.Errors {
			errs = append(errs, strings.TrimSpace(e.Path+"："+e.Message))
		}
	}
	return skills, errs, nil
}

// injected describes what a Codex process was given.
type injected struct {
	skills []RepoSkill       // injected by AgentHost
	known  map[string]string // every skill name Codex can use -> SKILL.md path
	root   string
	errs   []string
}

// injectSkills finds the skills in the task's repository and makes them
// available to this Codex process only.
func (e *Engine) injectSkills(ctx context.Context, r *Run, st *Step, nd node.Node, client *codex.Client) *injected {
	inj := &injected{known: map[string]string{}}
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	scan := e.discoverSkills(sctx, nd, st.Cwd)
	cancel()
	inj.root = scan.Root
	if scan.Error != "" {
		e.logf(r.ID, st.ID, "host", "warn", "查找技能失败：%s", scan.Error)
	}
	if len(scan.Skills) == 0 {
		return inj
	}
	// Leave out what Codex already loads by itself.
	native := map[string]bool{}
	before, _, lerr := listCodexSkills(ctx, client, st.Cwd, false)
	for _, s := range before {
		native[filepath.Clean(s.Path)] = true
		inj.known[s.Name] = s.Path
	}
	var roots []string
	for _, s := range scan.Skills {
		if native[filepath.Clean(s.Path)] || (lerr != nil && s.Native) {
			continue
		}
		// The skill's own folder: only this skill, not its neighbours.
		roots = append(roots, filepath.Dir(s.Path))
		inj.skills = append(inj.skills, s)
	}
	if len(roots) == 0 {
		return inj
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := client.Call(cctx, "skills/extraRoots/set", map[string]any{"extraRoots": roots}, nil)
	cancel()
	if err != nil {
		e.logf(r.ID, st.ID, "host", "warn", "向 Codex 注入技能失败：%v", err)
		inj.skills = nil
		return inj
	}
	e.logf(r.ID, st.ID, "host", "info", "已向本次 Codex 进程注入 %d 个技能：%s", len(roots), strings.Join(roots, ", "))
	// Ask Codex what it actually loaded: names, real paths and broken files.
	after, errs, err := listCodexSkills(ctx, client, st.Cwd, true)
	if err != nil {
		for _, s := range inj.skills {
			inj.known[s.Name] = s.Path
		}
		return inj
	}
	for _, s := range after {
		if _, ok := inj.known[s.Name]; !ok || s.Scope != "system" {
			inj.known[s.Name] = s.Path
		}
	}
	for _, m := range errs {
		for _, root := range roots {
			if strings.HasPrefix(m, root+"/") {
				inj.errs = append(inj.errs, m)
				break
			}
		}
	}
	return inj
}

// Skill mentions in a task: $name, as in the Codex CLI.
var reSkillMention = regexp.MustCompile(`\$([A-Za-z0-9][A-Za-z0-9_.:-]*)`)

// mentionedSkills returns the skills a prompt names explicitly, as Codex input items.
func mentionedSkills(prompt string, known map[string]string) (items []map[string]any, names []string) {
	seen := map[string]bool{}
	for _, m := range reSkillMention.FindAllStringSubmatch(prompt, -1) {
		name := strings.TrimRight(m[1], ".:-")
		path, ok := known[name]
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, map[string]any{"type": "skill", "name": name, "path": path})
		names = append(names, name)
	}
	return items, names
}

// skillsNotice is the timeline entry telling the user which skills this task got.
func skillsNotice(inj *injected, mentioned []string) *Item {
	if len(inj.skills) == 0 && len(mentioned) == 0 && len(inj.errs) == 0 {
		return nil
	}
	var b strings.Builder
	for _, s := range inj.skills {
		fmt.Fprintf(&b, "• %s", s.Name)
		if s.Rel != "" {
			fmt.Fprintf(&b, "（%s）", s.Rel)
		}
		if s.Description != "" {
			fmt.Fprintf(&b, "：%s", truncate(s.Description, 160))
		}
		b.WriteByte('\n')
	}
	if len(mentioned) > 0 {
		fmt.Fprintf(&b, "任务里指定使用：%s\n", strings.Join(mentioned, "、"))
	}
	for _, m := range inj.errs {
		fmt.Fprintf(&b, "⚠ 无法加载：%s\n", m)
	}
	title := fmt.Sprintf("本任务注入了 %d 个技能", len(inj.skills))
	if len(inj.skills) == 0 {
		title = "技能"
	}
	if inj.root != "" && len(inj.skills) > 0 {
		title += "（来自 " + inj.root + "）"
	}
	raw, _ := json.Marshal(inj.skills)
	return &Item{ID: newID("skills"), Kind: ItemSkills, Status: "completed", Title: title, Text: strings.TrimSpace(b.String()), Raw: raw, StartedAt: now()}
}
