package workflow

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const sample = "---\n" +
	"name: Health check\n" +
	"cwd: proj\n" +
	"max_parallel: 2\n" +
	"vars:\n" +
	"  target: ./...\n" +
	"---\n" +
	"\n" +
	"# Ignored because frontmatter has a name\n" +
	"\n" +
	"Shared rules for every agent.\n" +
	"\n" +
	"## Analyse code\n" +
	"- id: analyse\n" +
	"\n" +
	"Read the repo.\n" +
	"\n" +
	"## Run tests\n" +
	"- id: test\n" +
	"- type: shell\n" +
	"- depends_on: analyse\n" +
	"- retry: 2\n" +
	"- timeout: 5m\n" +
	"- continue_on_error: true\n" +
	"\n" +
	"```bash\n" +
	"# ## not a heading\n" +
	"go test {{vars.target}}\n" +
	"```\n" +
	"\n" +
	"## Lint\n" +
	"```yaml\n" +
	"id: lint\n" +
	"type: shell\n" +
	"depends_on: []\n" +
	"run: golangci-lint run\n" +
	"```\n" +
	"\n" +
	"## Report\n" +
	"- depends_on: [test, lint]\n" +
	"- if: always\n" +
	"- session: analyse\n" +
	"\n" +
	"Write a report using {{steps.test.output}}.\n" +
	"\n" +
	"## Deploy\n" +
	"```yaml\n" +
	"workflow: deploy.md\n" +
	"with:\n" +
	"  env: staging\n" +
	"```\n"

func TestParseSample(t *testing.T) {
	wf, err := Parse(sample, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "Health check" || wf.Cwd != "proj" || wf.MaxParallel != 2 {
		t.Fatalf("frontmatter not applied: %+v", wf)
	}
	if wf.Vars["target"] != "./..." {
		t.Fatalf("vars: %v", wf.Vars)
	}
	if wf.Instructions != "Shared rules for every agent." {
		t.Fatalf("instructions: %q", wf.Instructions)
	}
	if len(wf.Steps) != 5 {
		t.Fatalf("want 5 steps, got %d", len(wf.Steps))
	}
	a, test, lint, report, deploy := wf.Steps[0], wf.Steps[1], wf.Steps[2], wf.Steps[3], wf.Steps[4]

	if a.ID != "analyse" || a.Type != TypeAgent || a.Prompt != "Read the repo." || len(a.DependsOn) != 0 {
		t.Fatalf("analyse: %+v", a)
	}
	if test.Type != TypeShell || test.Retry != 2 || test.Timeout != 5*time.Minute || !test.ContinueOnError {
		t.Fatalf("test: %+v", test)
	}
	if !strings.Contains(test.Run, "go test {{vars.target}}") || !strings.Contains(test.Run, "## not a heading") {
		t.Fatalf("test run: %q", test.Run)
	}
	if lint.ID != "lint" || lint.Run != "golangci-lint run" || len(lint.DependsOn) != 0 {
		t.Fatalf("lint: %+v", lint)
	}
	if report.ID != "report" || report.If != IfAlways || report.Session != "analyse" ||
		!reflect.DeepEqual(report.DependsOn, []string{"test", "lint"}) {
		t.Fatalf("report: %+v", report)
	}
	if deploy.Type != TypeWorkflow || deploy.Workflow != "deploy.md" || deploy.With["env"] != "staging" ||
		!reflect.DeepEqual(deploy.DependsOn, []string{"report"}) {
		t.Fatalf("deploy: %+v", deploy)
	}
	layers := wf.Layers()
	want := [][]string{{"analyse", "lint"}, {"test"}, {"report"}, {"deploy"}}
	if !reflect.DeepEqual(layers, want) {
		t.Fatalf("layers = %v, want %v", layers, want)
	}
}

func TestDefaultSequentialAndTitle(t *testing.T) {
	wf, err := Parse("# 我的流程\n\n## 第一步\n做A\n\n## 第二步\n做B\n", "x")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Name != "我的流程" {
		t.Fatalf("name %q", wf.Name)
	}
	if wf.Steps[0].ID != "step-1" || wf.Steps[1].ID != "step-2" {
		t.Fatalf("ids %s %s", wf.Steps[0].ID, wf.Steps[1].ID)
	}
	if !reflect.DeepEqual(wf.Steps[1].DependsOn, []string{"step-1"}) {
		t.Fatalf("deps %v", wf.Steps[1].DependsOn)
	}
}

func TestBulletThatIsNotMetadataStaysInPrompt(t *testing.T) {
	wf, err := Parse("## Do it\n- check the README\n- fix typos\n", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wf.Steps[0].Prompt, "- check the README") {
		t.Fatalf("prompt %q", wf.Steps[0].Prompt)
	}
}

func TestErrors(t *testing.T) {
	cases := map[string]string{
		"no steps":     "# Just a title\n",
		"empty doc":    "\n\n",
		"unknown dep":  "## A\n- depends_on: nope\nhi\n",
		"cycle":        "## A\n- id: a\n- depends_on: b\nx\n## B\n- id: b\n- depends_on: a\ny\n",
		"dup":          "## A\n- id: a\nx\n## B\n- id: a\ny\n",
		"shell no cmd": "## A\n- type: shell\njust text\n",
		"bad if":       "## A\n- if: sometimes\nx\n",
		"session":      "## A\n- id: a\nx\n## B\n- depends_on: []\n- session: a\ny\n",
		"bad type":     "## A\n- type: robot\nx\n",
	}
	for name, src := range cases {
		if _, err := Parse(src, "x"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestRender(t *testing.T) {
	got := Render("a {{ vars.x }} b {{steps.s1.output}} c {{unknown}}", func(p string) (string, bool) {
		switch p {
		case "vars.x":
			return "X", true
		case "steps.s1.output":
			return "OUT", true
		}
		return "", false
	})
	if got != "a X b OUT c {{unknown}}" {
		t.Fatalf("got %q", got)
	}
}

// ---------------------------------------------------------------- forgiving format

func ids(ss []*Step) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return out
}

func titleDeps(t *testing.T, wf *Workflow) map[string][]string {
	t.Helper()
	byID := map[string]string{}
	for _, s := range wf.Steps {
		byID[s.ID] = s.Title
	}
	out := map[string][]string{}
	for _, s := range wf.Steps {
		d := []string{}
		for _, id := range s.DependsOn {
			d = append(d, byID[id])
		}
		out[s.Title] = d
	}
	return out
}

const casual = "# 发布前检查\n\n总体说明写在这里。\n\n" +
	"## 1. 分析改动\n看看最近的提交改了什么。\n\n" +
	"## 2. 运行测试\n类型：命令\n失败继续\n重试：两次\n超时：10分钟\n\n```bash\ngo test ./...\n```\n\n" +
	"## 3. 代码审阅（并行）\n- 沙箱：只读\n\n重点检查错误处理。\n\n" +
	"## 4. 写报告\n依赖：运行测试、代码审阅\n条件：总是\n\n根据上面的结果写报告。\n\n" +
	"## 5. 归档\n把报告存起来。\n"

func TestCasualFormat(t *testing.T) {
	wf, err := Parse(casual, "x")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Mode != ModeSteps || wf.Name != "发布前检查" || wf.Instructions != "总体说明写在这里。" {
		t.Fatalf("mode=%s name=%q instr=%q", wf.Mode, wf.Name, wf.Instructions)
	}
	var titles []string
	for _, s := range wf.Steps {
		titles = append(titles, s.Title)
	}
	if !reflect.DeepEqual(titles, []string{"分析改动", "运行测试", "代码审阅", "写报告", "归档"}) {
		t.Fatalf("titles %v", titles)
	}
	test, review, report := wf.Steps[1], wf.Steps[2], wf.Steps[3]
	if test.Type != TypeShell || !test.ContinueOnError || test.Retry != 2 || test.Timeout != 10*time.Minute || test.Run != "go test ./..." {
		t.Fatalf("test step %+v", test)
	}
	if !review.Parallel || review.Sandbox != "read-only" || review.Prompt != "重点检查错误处理。" {
		t.Fatalf("review step %+v", review)
	}
	if report.If != IfAlways || report.AutoDeps {
		t.Fatalf("report step %+v", report)
	}
	want := map[string][]string{
		"分析改动": {}, "运行测试": {"分析改动"}, "代码审阅": {"分析改动"},
		"写报告": {"运行测试", "代码审阅"}, "归档": {"写报告"},
	}
	if got := titleDeps(t, wf); !reflect.DeepEqual(got, want) {
		t.Fatalf("deps\n got %v\nwant %v", got, want)
	}
}

func TestParallelGroupFeedsNextStep(t *testing.T) {
	wf, err := Parse("## 拉代码\nx\n## 前端构建（并行）\nx\n## 后端构建（并行）\nx\n## 打包\nx\n", "x")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"拉代码": {}, "前端构建": {}, "后端构建": {}, "打包": {"拉代码", "前端构建", "后端构建"}}
	if got := titleDeps(t, wf); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestContextSectionsAndStepList(t *testing.T) {
	src := "# 发布流程\n\n## 背景\n这是一个 Go 服务。\n\n## 步骤\n先读一下说明。\n\n" +
		"1. 拉取代码\n2. 运行测试\n   类型：命令\n   ```bash\n   go test ./...\n   ```\n3. 打包发布\n   依赖：1\n\n" +
		"## 注意事项\n不要改动 main 分支。\n"
	wf, err := Parse(src, "x")
	if err != nil {
		t.Fatal(err)
	}
	if got := wf.ContextSections; !reflect.DeepEqual(got, []string{"背景", "注意事项"}) {
		t.Fatalf("context %v", got)
	}
	for _, s := range []string{"这是一个 Go 服务。", "不要改动 main 分支。", "先读一下说明。"} {
		if !strings.Contains(wf.Instructions, s) {
			t.Fatalf("instructions miss %q: %q", s, wf.Instructions)
		}
	}
	if len(wf.Steps) != 3 || wf.Steps[1].Type != TypeShell || wf.Steps[1].Run != "go test ./..." {
		t.Fatalf("steps %+v", wf.Steps)
	}
	if got := titleDeps(t, wf)["打包发布"]; !reflect.DeepEqual(got, []string{"拉取代码"}) {
		t.Fatalf("deps by number: %v", got)
	}
}

func TestHeadlessNumberedList(t *testing.T) {
	src := "帮我做一次例行检查。\n\n1. 看一下磁盘空间\n2. 第二件事\n```bash\ndf -h\n```\n3. 汇总\n\n以上都用中文回答。\n"
	wf, err := Parse(src, "check")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Mode != ModeSteps || len(wf.Steps) != 3 || wf.Name != "check" {
		t.Fatalf("mode %s steps %d name %q", wf.Mode, len(wf.Steps), wf.Name)
	}
	if !strings.Contains(wf.Instructions, "帮我做一次例行检查") || !strings.Contains(wf.Instructions, "以上都用中文回答") {
		t.Fatalf("instructions %q", wf.Instructions)
	}
	if wf.Steps[1].Type != TypeShell || wf.Steps[1].Run != "df -h" {
		t.Fatalf("unindented code block should belong to item 2: %+v", wf.Steps[1])
	}
	if wf.Steps[2].Prompt != "汇总" {
		t.Fatalf("trailing prose must not join the last item: %q", wf.Steps[2].Prompt)
	}
}

func TestAgentMode(t *testing.T) {
	free := "帮我看看这个仓库有没有明显的安全问题，\n顺便把依赖升级一下，最后告诉我改了什么。\n"
	wf, err := Parse(free, "x")
	if err != nil {
		t.Fatal(err)
	}
	if wf.Mode != ModeAgent || len(wf.Steps) != 1 || !wf.Steps[0].Orchestrate || !strings.Contains(wf.Steps[0].Prompt, "安全问题") {
		t.Fatalf("agent mode: %+v", wf)
	}
	// Context-only sections also mean "let Codex decide".
	wf, err = Parse("## 背景\n一个老项目。\n## 要求\n整理一下文档。\n", "x")
	if err != nil || wf.Mode != ModeAgent {
		t.Fatalf("context-only doc: %v %+v", err, wf)
	}
	// Forcing agent mode keeps the count of detected steps.
	wf, err = ParseMode(casual, "x", "agent")
	if err != nil || wf.Mode != ModeAgent || wf.DetectedSteps != 5 {
		t.Fatalf("forced agent: %v mode=%s detected=%d", err, wf.Mode, wf.DetectedSteps)
	}
	// Frontmatter can pin the mode.
	wf, err = Parse("---\n模式: 编排\n---\n## A\nx\n## B\ny\n", "x")
	if err != nil || wf.Mode != ModeAgent {
		t.Fatalf("frontmatter mode: %v %+v", err, wf)
	}
}

func TestReferences(t *testing.T) {
	base := "## 分析代码\nx\n## 运行单元测试\nx\n## 运行集成测试\nx\n"
	cases := map[string][]string{
		"依赖：1":             {"分析代码"},
		"依赖：第一步":           {"分析代码"},
		"依赖：步骤2、步骤3":       {"运行单元测试", "运行集成测试"},
		"依赖：单元测试":          {"运行单元测试"},
		"依赖：分析代码和运行单元测试":   {"分析代码", "运行单元测试"},
		"依赖：无":             {},
		"- depends_on: []": {},
	}
	for line, want := range cases {
		wf, err := Parse(base+"## 报告\n"+line+"\n\n写报告\n", "x")
		if err != nil {
			t.Errorf("%s: %v", line, err)
			continue
		}
		if got := titleDeps(t, wf)["报告"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v want %v", line, got, want)
		}
	}
	for _, bad := range []string{"依赖：测试", "依赖：部署", "依赖：9"} {
		if _, err := Parse(base+"## 报告\n"+bad+"\n\nx\n", "x"); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
	wf, _ := Parse(base, "x")
	if s, err := wf.ResolveRef("集成测试"); err != nil || s.Title != "运行集成测试" {
		t.Fatalf("ResolveRef: %v %v", s, err)
	}
}

func TestTitlesTypesAndValues(t *testing.T) {
	src := "## 3. 代码审阅（并行，失败继续）\nx\n## Build\n```bash\nmake\n```\n## Build\n运行下面的命令看看：\n```bash\nmake\n```\n" +
		"## 部署\n调用：deploy.md\n参数：env=staging, region=cn\n条件：失败时\n"
	wf, err := Parse(src, "x")
	if err != nil {
		t.Fatal(err)
	}
	if s := wf.Steps[0]; s.Title != "代码审阅" || !s.Parallel || !s.ContinueOnError {
		t.Fatalf("tags: %+v", s)
	}
	if wf.Steps[1].Type != TypeShell || wf.Steps[2].Type != TypeAgent {
		t.Fatalf("shell detection: %s %s", wf.Steps[1].Type, wf.Steps[2].Type)
	}
	if !reflect.DeepEqual(ids(wf.Steps[1:3]), []string{"build", "build-2"}) {
		t.Fatalf("auto id dedupe: %v", ids(wf.Steps))
	}
	d := wf.Steps[3]
	if d.Type != TypeWorkflow || d.Workflow != "deploy.md" || d.With["env"] != "staging" || d.With["region"] != "cn" || d.If != IfFailure {
		t.Fatalf("workflow step %+v", d)
	}
}

func TestRenderUnicodePath(t *testing.T) {
	got := Render("见 {{steps.分析代码.output}}", func(p string) (string, bool) { return "[" + p + "]", true })
	if got != "见 [steps.分析代码.output]" {
		t.Fatalf("%q", got)
	}
}

func TestBareHeadingIsAStep(t *testing.T) {
	wf, err := Parse("## A\n- id: a\n", "x")
	if err != nil || wf.Steps[0].Prompt != "A" {
		t.Fatalf("%v %+v", err, wf)
	}
}

func TestWrappedDocument(t *testing.T) {
	inner := "## 1. A\nx\n\n## 2. B\n```bash\necho hi\n```\n"
	for _, src := range []string{"~~~markdown\n" + inner + "~~~\n", "```md\n## 1. A\nx\n## 2. B\ny\n```", "~~~\n" + inner + "~~~"} {
		wf, err := Parse(src, "x")
		if err != nil || wf.Mode != ModeSteps || len(wf.Steps) != 2 {
			t.Fatalf("wrapped doc not unwrapped: %v %+v\n%s", err, wf, src)
		}
	}
	// A document that merely starts with a code block is left alone.
	wf, err := Parse("```bash\nls\n```\n\n## 1. A\nx\n## 2. B\ny\n", "x")
	if err != nil || len(wf.Steps) != 2 || !strings.Contains(wf.Instructions, "ls") {
		t.Fatalf("leading code block: %v %+v", err, wf)
	}
}
