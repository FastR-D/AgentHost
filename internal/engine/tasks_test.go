package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func writeSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n做法\n"
	if err := os.WriteFile(dir+"/SKILL.md", []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillItems(r Run) []*Item {
	var out []*Item
	for _, it := range r.Steps[0].Items {
		if it.Kind == ItemSkills {
			out = append(out, it)
		}
	}
	return out
}

// Skills come from the task's repository and only go to that Codex process.
func TestRepoSkills(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newTestEngine(t, nil)
	s, err := e.CreateSession("", "repo/sub", "")
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Dir(s.Cwd)
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	writeSkill(t, repo+"/tools/deploy/skills/deploy-helper", "deploy-helper", "部署助手")
	writeSkill(t, repo+"/node_modules/pkg/skills/nm", "nm-skill", "依赖包里的")
	writeSkill(t, repo+"/.agents/skills/native", "native-one", "Codex 自己会加载")

	// The UI sees what is in the repository, including Codex's own folders.
	scan, err := e.DiscoverSkills("", s.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if scan.Root != repo || len(scan.Skills) != 2 || scan.Skills[0].Name != "native-one" || !scan.Skills[0].Native ||
		scan.Skills[1].Name != "deploy-helper" || scan.Skills[1].Native || scan.Skills[1].Description != "部署助手" {
		t.Fatalf("scan: %+v", scan)
	}

	// Only the skill Codex would not load by itself is injected, from its own folder.
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Text: "用 $deploy-helper 部署，顺便 $native-one。$unknown 不管"})
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	if !strings.HasSuffix(r.Output, "[roots deploy-helper] [skill deploy-helper] [skill native-one]") {
		t.Fatalf("injection: %q", r.Output)
	}
	items := skillItems(r)
	if len(items) != 1 || !strings.Contains(items[0].Title, "注入了 1 个技能") || !strings.Contains(items[0].Text, "deploy-helper（tools/deploy/skills/deploy-helper）：部署助手") ||
		!strings.Contains(items[0].Text, "指定使用：deploy-helper、native-one") {
		t.Fatalf("notice: %+v", items)
	}
	// A follow-up still gets the skills, without naming any.
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Text: "谢谢"})
	if r = waitDone(t, e, runIDOf(t, raw)); r.Output != "DONE: 谢谢 [roots deploy-helper]" {
		t.Fatalf("follow-up: %q", r.Output)
	}

	// Above the repository, its skills are that project's, not this directory's.
	writeSkill(t, filepath.Dir(repo)+"/notes/skills/jot", "jot", "记笔记")
	if scan, _ := e.DiscoverSkills("", filepath.Dir(repo)); len(scan.Skills) != 1 || scan.Skills[0].Name != "jot" {
		t.Fatalf("workspace scan: %+v", scan)
	}

	// No skills: nothing injected, nothing shown.
	plain, _ := e.CreateSession("", "plain", "")
	raw, _ = e.StartChat(ChatRequest{SessionID: plain.ID, Text: "hi"})
	if r = waitDone(t, e, runIDOf(t, raw)); r.Output != "DONE: hi" || len(skillItems(r)) != 0 {
		t.Fatalf("plain: %q %+v", r.Output, skillItems(r))
	}
}

// The workspace may sit inside a repository (e.g. AgentHost's own checkout):
// that repository and AgentHost's data are not the task's code.
func TestSkillsWorkspaceInsideRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := newTestEngine(t, nil)
	top := filepath.Dir(e.cfg.DataDir)
	if out, err := exec.Command("git", "init", "-q", top).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	writeSkill(t, top+"/tools/skills/host-tool", "host-tool", "宿主仓库的")
	writeSkill(t, e.cfg.DataDir+"/skills-lib/old", "old", "旧技能库")
	s, _ := e.CreateSession("", "proj", "")
	writeSkill(t, s.Cwd+"/skills/mine", "mine", "项目自己的")
	scan, err := e.DiscoverSkills("", s.Cwd)
	if err != nil || scan.Root != s.Cwd || len(scan.Skills) != 1 || scan.Skills[0].Name != "mine" {
		t.Fatalf("scan: %+v %v", scan, err)
	}
	// Outside the workspace the repository is the project, minus AgentHost's data.
	if scan, _ = e.DiscoverSkills("", top+"/tools"); scan.Root != top || len(scan.Skills) != 2 || scan.Skills[0].Name != "host-tool" || scan.Skills[1].Name != "mine" {
		t.Fatalf("repo scan: %+v", scan)
	}
}

func TestTaskLib(t *testing.T) {
	e := newTestEngine(t, nil)
	lib := e.Tasks()
	tk, err := lib.Save(Task{Title: "部署 / 发布", Cwd: "app", Body: "先跑测试\n再发布"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Name != "部署-发布" {
		t.Fatalf("name %q", tk.Name)
	}
	b, _ := os.ReadFile(lib.Dir() + "/部署-发布.md")
	if string(b) != "---\n名称: 部署 / 发布\n目录: app\n---\n\n先跑测试\n再发布\n" {
		t.Fatalf("file:\n%s", b)
	}
	if again, _ := lib.Save(Task{Title: "部署 / 发布", Body: "x"}); again.Name != "部署-发布-2" {
		t.Fatalf("unique name %q", again.Name)
	}
	if _, err := lib.Save(Task{Title: "", Body: "x"}); err == nil {
		t.Fatal("empty title accepted")
	}
	// Hand-written files: English keys, or no front matter at all.
	os.WriteFile(lib.Dir()+"/en.md", []byte("---\ntitle: Weekly\nserver: box2\ncwd: /srv/app\n---\nwrite it\n"), 0o644)
	os.WriteFile(lib.Dir()+"/plain.md", []byte("# 看日志\n\n看看最近的错误日志\n"), 0o644)
	if en, _ := lib.Get("en"); en.Title != "Weekly" || en.Node != "box2" || en.Cwd != "/srv/app" || en.Body != "write it" {
		t.Fatalf("english keys: %+v", en)
	}
	if p, _ := lib.Get("plain.md"); p.Title != "看日志" || !strings.HasPrefix(p.Body, "# 看日志") {
		t.Fatalf("no front matter: %+v", p)
	}
	if _, err := lib.Get("../etc/passwd"); err == nil {
		t.Fatal("path escape accepted")
	}
	if n := len(lib.List()); n != 4 {
		t.Fatalf("list: %d", n)
	}

	// Running a task: its text is the prompt, the note goes after it.
	s, _ := e.CreateSession("", "", "")
	raw, err := e.StartChat(ChatRequest{SessionID: s.ID, Task: tk.Name})
	if err != nil {
		t.Fatal(err)
	}
	r := waitDone(t, e, runIDOf(t, raw))
	if r.Task != tk.Name || r.TaskTitle != "部署 / 发布" || r.Title != "部署 / 发布" || r.Prompt != "先跑测试\n再发布" || r.Output != "DONE: 先跑测试" {
		t.Fatalf("task run: %+v", r)
	}
	raw, _ = e.StartChat(ChatRequest{SessionID: s.ID, Task: tk.Name, Text: "只发 staging"})
	r = waitDone(t, e, runIDOf(t, raw))
	if r.Title != "部署 / 发布：只发 staging" || r.Note != "只发 staging" || !strings.HasSuffix(r.Prompt, "\n\n---\n\n补充说明：只发 staging") {
		t.Fatalf("task + note: %q %q", r.Title, r.Prompt)
	}
	raw, err = e.Retry(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r2 := waitDone(t, e, runIDOf(t, raw)); r2.Task != tk.Name || r2.Note != "只发 staging" || r2.Prompt != r.Prompt {
		t.Fatalf("retry lost the task: %+v", r2)
	}
	if _, err := e.StartChat(ChatRequest{SessionID: s.ID, Task: "nope"}); err == nil {
		t.Fatal("unknown task accepted")
	}

	// A Markdown file in the code is a task too.
	os.WriteFile(s.Cwd+"/DEPLOY.md", []byte("# 发布流程\n\n1. 打包\n"), 0o644)
	raw, err = e.StartChat(ChatRequest{SessionID: s.ID, TaskFile: "DEPLOY.md"})
	if err != nil {
		t.Fatal(err)
	}
	r = waitDone(t, e, runIDOf(t, raw))
	if r.TaskFile != s.Cwd+"/DEPLOY.md" || r.TaskTitle != "发布流程" || r.Output != "DONE: # 发布流程" {
		t.Fatalf("task file: %+v", r)
	}
	if _, err := e.StartChat(ChatRequest{SessionID: s.ID, TaskFile: "missing.md"}); err == nil {
		t.Fatal("missing task file accepted")
	}

	if err := lib.Delete(tk.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Get(tk.Name); err == nil {
		t.Fatal("deleted task still there")
	}
}

// The first time, the user's own workflows and skills become tasks.
func TestTaskMigration(t *testing.T) {
	dir := t.TempDir()
	wf, sk := dir+"/workflows", dir+"/skills-lib"
	os.MkdirAll(wf, 0o755)
	example := []byte("# example\n")
	os.WriteFile(wf+"/ex.md", example, 0o644)
	os.WriteFile(wf+"/"+exampleManifest, []byte(`{"ex.md":"`+sha(example)+`"}`), 0o644)
	os.WriteFile(wf+"/hello-world.md", []byte("edited by the user\n"), 0o644)
	os.WriteFile(wf+"/测试workflow.md", []byte("---\nname: 新的 Workflow\ncwd: proj\nvars:\n  target: \".\"\n---\n\n# 新的 Workflow\n\n阅读 {{vars.target}}\n"), 0o644)
	os.MkdirAll(sk+"/weekly", 0o755)
	os.WriteFile(sk+"/weekly/SKILL.md", []byte("---\nname: weekly\ndescription: x\nmetadata:\n  short-description: 写周报\n---\n\n看提交写周报\n"), 0o644)

	seed := fstest.MapFS{"server-check.md": {Data: []byte("---\n名称: 服务器晨检\n---\n\n看看磁盘\n")}}
	lib, err := OpenTaskLib(dir+"/tasks", seed, LegacyDirs{Workflows: wf, Skills: sk})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Task{}
	for _, tk := range lib.List() {
		got[tk.Name] = tk
	}
	if len(got) != 4 {
		t.Fatalf("tasks: %v", got)
	}
	if m := got["测试workflow"]; m.Title != "新的 Workflow" || m.Cwd != "proj" || !strings.Contains(m.Body, "阅读 {{vars.target}}") || !strings.Contains(m.Body, "- target = .") {
		t.Fatalf("workflow: %+v", m)
	}
	if m := got["weekly"]; m.Title != "写周报" || m.Body != "看提交写周报" {
		t.Fatalf("skill: %+v", m)
	}
	if got["hello-world"].Body != "edited by the user" || got["server-check"].Title != "服务器晨检" {
		t.Fatalf("others: %+v", got)
	}
	if _, err := os.Stat(wf + "/测试workflow.md"); err != nil {
		t.Fatal("old files must stay")
	}
	// Only once: a deleted task stays deleted.
	lib.Delete("weekly")
	lib, _ = OpenTaskLib(dir+"/tasks", seed, LegacyDirs{Workflows: wf, Skills: sk})
	if _, err := lib.Get("weekly"); err == nil {
		t.Fatal("migrated again")
	}
}
