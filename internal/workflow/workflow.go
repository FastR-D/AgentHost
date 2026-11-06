// Package workflow parses Markdown workflow documents.
//
// The format is deliberately forgiving, so that an ordinary hand-written
// runbook works:
//
//	# 发布前检查
//
//	总体说明：第一个步骤之前的文字会告诉每个 Agent。
//
//	## 1. 分析改动
//	看看最近的提交改了什么，列出风险点。
//
//	## 2. 运行测试
//	类型：命令
//	失败继续
//
//	```bash
//	go test ./...
//	```
//
//	## 3. 代码审阅（并行）
//	重点检查错误处理。
//
//	## 4. 写报告
//	依赖：运行测试、代码审阅
//	条件：总是
//
//	根据上面的结果写一份报告。
//
// Steps are the shallowest headings (a lone "#" heading is the title), or a
// numbered list when there are no headings. Sections such as "背景" or
// "注意事项" become shared instructions; a "步骤" section expands into the
// list or sub-headings it contains. Settings are short "键：值" lines at the
// top of a step. Steps run in document order unless they say otherwise.
//
// Documents in which no steps can be found run in agent mode: the whole
// document goes to one Codex agent that plans and executes it.
//
// The original strict syntax (frontmatter, "- key: value" bullets, ```yaml
// metadata blocks, {{steps.<id>.output}} templates) keeps working.
package workflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Step types.
const (
	TypeAgent    = "agent"
	TypeShell    = "shell"
	TypeWorkflow = "workflow"
)

// Conditions controlling whether a step runs, based on its dependencies.
const (
	IfSuccess = "success" // all dependencies succeeded (default)
	IfFailure = "failure" // at least one dependency failed
	IfAlways  = "always"  // run regardless of dependency outcome
)

// Execution modes.
const (
	ModeSteps = "steps" // AgentHost schedules the recognised steps
	ModeAgent = "agent" // one Codex agent reads and executes the whole document
)

// OrchestratorID is the id of the single step of an agent-mode workflow.
const OrchestratorID = "orchestrate"

type Workflow struct {
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	Instructions    string            `json:"instructions,omitempty"`
	Cwd             string            `json:"cwd,omitempty"`
	Model           string            `json:"model,omitempty"`
	Sandbox         string            `json:"sandbox,omitempty"`
	MaxParallel     int               `json:"maxParallel"`
	Vars            map[string]string `json:"vars,omitempty"`
	Mode            string            `json:"mode"`
	Network         bool              `json:"network,omitempty"` // agent commands may use the network
	ModeReason      string            `json:"modeReason,omitempty"`
	DetectedSteps   int               `json:"detectedSteps"`             // steps found, even in agent mode
	ContextSections []string          `json:"contextSections,omitempty"` // headings folded into Instructions
	Body            string            `json:"body,omitempty"`            // document after the frontmatter
	Steps           []*Step           `json:"steps"`
}

type Step struct {
	ID              string            `json:"id"`
	Number          int               `json:"number"`
	Title           string            `json:"title"`
	Type            string            `json:"type"`
	DependsOn       []string          `json:"dependsOn"`
	AutoDeps        bool              `json:"autoDeps,omitempty"` // dependencies came from document order
	Parallel        bool              `json:"parallel,omitempty"`
	If              string            `json:"if"`
	Retry           int               `json:"retry,omitempty"`
	Timeout         Duration          `json:"timeout,omitempty"`
	ContinueOnError bool              `json:"continueOnError,omitempty"`
	Cwd             string            `json:"cwd,omitempty"`
	Model           string            `json:"model,omitempty"`
	Sandbox         string            `json:"sandbox,omitempty"`
	Session         string            `json:"session,omitempty"`
	Prompt          string            `json:"prompt,omitempty"`
	Run             string            `json:"run,omitempty"`
	Workflow        string            `json:"workflow,omitempty"`
	With            map[string]string `json:"with,omitempty"`
	Orchestrate     bool              `json:"orchestrate,omitempty"`
	Line            int               `json:"line"`

	deps    []string // raw dependency references, resolved later
	depsSet bool
}

// ParseError points at the line that caused a problem.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("第 %d 行：%s", e.Line, e.Msg)
	}
	return e.Msg
}

func errAt(line int, format string, args ...any) error {
	return &ParseError{Line: line, Msg: fmt.Sprintf(format, args...)}
}

type line struct {
	no   int // 1-based line number in the source
	text string
}

var (
	reHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)
	reFence   = regexp.MustCompile("^\\s*(```+|~~~+)\\s*([A-Za-z0-9_+-]*)")
	reSlugBad = regexp.MustCompile(`[^a-z0-9]+`)
)

// Parse parses a workflow document. fallbackName is used when the document
// names itself neither in frontmatter nor with a title heading.
func Parse(src, fallbackName string) (*Workflow, error) {
	return ParseMode(src, fallbackName, "")
}

// ParseMode is Parse with the execution mode forced to ModeSteps or
// ModeAgent; "" lets the document decide.
func ParseMode(src, fallbackName, mode string) (*Workflow, error) {
	src = unwrapFence(strings.ReplaceAll(src, "\r\n", "\n"))
	raw := strings.Split(src, "\n")
	all := make([]line, len(raw))
	for i, t := range raw {
		all[i] = line{no: i + 1, text: t}
	}

	wf := &Workflow{MaxParallel: 4}
	body, err := parseFrontmatter(wf, all)
	if err != nil {
		return nil, err
	}
	if mode != "" {
		m, ok := normMode(mode)
		if !ok {
			return nil, fmt.Errorf("未知的执行模式 %q（可选 steps 或 agent）", mode)
		}
		wf.Mode = m
	}
	wf.Body = strings.TrimSpace(joinLines(body))
	if wf.Body == "" {
		return nil, errAt(0, "workflow 没有内容")
	}

	cands, instructions := findSteps(wf, body)
	wf.Instructions = strings.TrimSpace(instructions)
	if wf.Name == "" {
		wf.Name = fallbackName
	}
	if wf.Name == "" {
		wf.Name = "未命名 workflow"
	}

	steps, err := buildSteps(cands)
	if err != nil && wf.Mode != ModeAgent {
		return nil, err
	}
	wf.DetectedSteps = len(steps)

	switch {
	case wf.Mode == ModeAgent:
		wf.ModeReason = "设置为交给 Codex 编排执行"
	case len(steps) == 0:
		wf.Mode = ModeAgent
		wf.ModeReason = "没有识别到明确的步骤，整篇交给 Codex 理解并执行"
	default:
		wf.Mode = ModeSteps
	}
	if wf.Mode == ModeAgent {
		if wf.Instructions == "" && len(steps) == 0 && countText(body) == 0 {
			return nil, errAt(0, "workflow 只有标题，没有要执行的内容")
		}
		wf.Steps = []*Step{{
			ID: OrchestratorID, Number: 1, Title: "Codex 编排执行", Type: TypeAgent,
			DependsOn: []string{}, If: IfSuccess, Prompt: wf.Body, Orchestrate: true, Line: 1,
		}}
		return wf, nil
	}
	wf.Steps = steps
	if err := resolve(wf); err != nil {
		return nil, err
	}
	return wf, nil
}

var reWholeFence = regexp.MustCompile("(?s)^\\s*(```+|~~~+)[ \\t]*(?:markdown|md)?[ \\t]*\\n(.*)\\n[ \\t]*(```+|~~~+)\\s*$")

// unwrapFence removes a code fence wrapped around the whole document, as
// left behind when a workflow is copied out of a chat or a README.
func unwrapFence(src string) string {
	m := reWholeFence.FindStringSubmatch(src)
	if m == nil || m[1] != m[3] {
		return src
	}
	// Only if the opening fence is not closed earlier (i.e. it really wraps everything).
	inner := m[2]
	for _, l := range strings.Split(inner, "\n") {
		if strings.TrimSpace(l) == m[1] {
			return src
		}
	}
	return inner + "\n"
}

// countText counts non-blank lines that are not headings.
func countText(body []line) int {
	n := 0
	for _, l := range body {
		t := strings.TrimSpace(l.text)
		if t != "" && !reHeading.MatchString(t) {
			n++
		}
	}
	return n
}

func normMode(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "steps", "step", "structured", "步骤", "调度", "按步骤":
		return ModeSteps, true
	case "agent", "codex", "orchestrate", "free", "编排", "自由", "智能", "交给codex":
		return ModeAgent, true
	}
	return "", false
}

// ---------------------------------------------------------------- frontmatter

func parseFrontmatter(wf *Workflow, all []line) ([]line, error) {
	if len(all) == 0 || strings.TrimSpace(all[0].text) != "---" {
		return all, nil
	}
	end := -1
	for i := 1; i < len(all); i++ {
		if strings.TrimSpace(all[i].text) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, errAt(1, "文件头以 --- 开始，但没有用 --- 结束")
	}
	var fm map[string]any
	if err := yaml.Unmarshal([]byte(joinLines(all[1:end])), &fm); err != nil {
		return nil, errAt(1, "文件头格式错误：%v（含冒号的值请加引号）", err)
	}
	for k, v := range fm {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "name", "title", "名称", "标题":
			wf.Name = str(v)
		case "description", "描述", "说明":
			wf.Description = str(v)
		case "cwd", "workdir", "目录", "工作目录":
			wf.Cwd = str(v)
		case "model", "模型":
			wf.Model = str(v)
		case "sandbox", "沙箱", "权限":
			sb, ok := normSandbox(str(v))
			if !ok {
				return nil, errAt(1, "文件头：沙箱只能是 只读 / 可写 / 完全访问（read-only / workspace-write / danger-full-access）")
			}
			wf.Sandbox = sb
		case "network", "联网", "允许联网", "网络":
			b, ok := parseBool(str(v))
			if !ok {
				return nil, errAt(1, "文件头：联网 应为 是/否")
			}
			wf.Network = b
		case "mode", "模式":
			m, ok := normMode(str(v))
			if !ok {
				return nil, errAt(1, "文件头：模式只能是 steps（按步骤调度）或 agent（交给 Codex 编排）")
			}
			wf.Mode = m
		case "max_parallel", "parallel", "concurrency", "并行数", "最大并行", "并发":
			n, err := strconv.Atoi(str(v))
			if err != nil || n < 1 {
				return nil, errAt(1, "文件头：%s 必须是正整数", k)
			}
			wf.MaxParallel = n
		case "vars", "inputs", "env", "变量", "参数":
			m, ok := v.(map[string]any)
			if !ok {
				return nil, errAt(1, "文件头：%s 需要写成 名称: 值 的列表", k)
			}
			if wf.Vars == nil {
				wf.Vars = map[string]string{}
			}
			for vk, vv := range m {
				wf.Vars[vk] = str(vv)
			}
		}
	}
	return all[end+1:], nil
}

// ---------------------------------------------------------------- document structure

// candidate is a step before its body has been interpreted.
type candidate struct {
	title   string
	line    int
	content []line
}

// codeMask reports, per line, whether it belongs to a fenced code block
// (fence lines included).
func codeMask(ls []line) []bool {
	mask := make([]bool, len(ls))
	in := ""
	for i, l := range ls {
		if m := reFence.FindStringSubmatch(l.text); m != nil {
			mask[i] = true
			marker := m[1][:3]
			if in == "" {
				in = marker
			} else if strings.HasPrefix(strings.TrimSpace(l.text), in) {
				in = ""
			}
			continue
		}
		mask[i] = in != ""
	}
	return mask
}

type heading struct {
	idx   int // index into body
	level int
	text  string
}

func headingsOf(ls []line, mask []bool) []heading {
	var hs []heading
	for i, l := range ls {
		if mask[i] {
			continue
		}
		if m := reHeading.FindStringSubmatch(l.text); m != nil {
			hs = append(hs, heading{idx: i, level: len(m[1]), text: strings.TrimSpace(m[2])})
		}
	}
	return hs
}

// contextTitles are section names that describe the job rather than being a step.
var contextTitles = setOf(
	"背景", "背景说明", "项目背景", "说明", "总体说明", "整体说明", "概述", "简介", "介绍", "目标", "目的",
	"前提", "前提条件", "前置条件", "前置要求", "环境", "环境要求", "注意", "注意事项", "约定", "规则",
	"要求", "原则", "备注", "参考", "参考资料", "附录", "上下文", "输入", "产出要求", "输出要求",
	"background", "overview", "context", "notes", "note", "goal", "goals", "prerequisites",
	"requirements", "rules", "references", "appendix", "about", "introduction",
)

// stepListTitles are section names whose list or sub-headings are the steps.
var stepListTitles = setOf(
	"步骤", "流程", "操作步骤", "执行步骤", "执行流程", "工作流程", "处理流程", "具体步骤", "任务", "任务列表",
	"steps", "procedure", "workflow", "tasks", "plan",
)

func setOf(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// findSteps locates step candidates and collects the shared instructions.
func findSteps(wf *Workflow, body []line) ([]*candidate, string) {
	mask := codeMask(body)
	hs := headingsOf(body, mask)

	// A single level-1 heading is the document title.
	titleIdx := -1
	if len(hs) > 0 && hs[0].level == 1 {
		n1 := 0
		for _, h := range hs {
			if h.level == 1 {
				n1++
			}
		}
		if n1 == 1 {
			titleIdx = hs[0].idx
			if wf.Name == "" {
				wf.Name = cleanTitle(hs[0].text)
			}
			hs = hs[1:]
		}
	}
	without := func(ls []line, offset int) []line {
		var out []line
		for i, l := range ls {
			if i+offset != titleIdx {
				out = append(out, l)
			}
		}
		return out
	}

	var instr []string
	if len(hs) == 0 {
		rest := without(body, 0)
		items, other := listSteps(rest, codeMask(rest), 2)
		if len(items) > 0 {
			return items, joinLines(other)
		}
		return nil, joinLines(rest)
	}

	level := hs[0].level
	for _, h := range hs {
		if h.level < level {
			level = h.level
		}
	}
	var top []heading
	for _, h := range hs {
		if h.level == level {
			top = append(top, h)
		}
	}
	instr = append(instr, joinLines(without(body[:top[0].idx], 0)))

	var cands []*candidate
	for i, h := range top {
		end := len(body)
		if i+1 < len(top) {
			end = top[i+1].idx
		}
		content := body[h.idx+1 : end]
		key := strings.ToLower(normTitle(h.text))
		switch {
		case contextTitles[key]:
			wf.ContextSections = append(wf.ContextSections, cleanTitle(h.text))
			instr = append(instr, "## "+cleanTitle(h.text)+"\n"+strings.TrimSpace(joinLines(content)))
		case stepListTitles[key]:
			sub, other := expandSection(content)
			if len(sub) == 0 {
				cands = append(cands, &candidate{title: h.text, line: body[h.idx].no, content: content})
				continue
			}
			cands = append(cands, sub...)
			if t := strings.TrimSpace(other); t != "" {
				instr = append(instr, t)
			}
		default:
			cands = append(cands, &candidate{title: h.text, line: body[h.idx].no, content: content})
		}
	}
	return cands, strings.Join(nonEmpty(instr), "\n\n")
}

// expandSection turns the sub-headings or numbered list of a "步骤" section into steps.
func expandSection(content []line) ([]*candidate, string) {
	mask := codeMask(content)
	hs := headingsOf(content, mask)
	if len(hs) > 0 {
		level := hs[0].level
		for _, h := range hs {
			if h.level < level {
				level = h.level
			}
		}
		var out []*candidate
		var top []heading
		for _, h := range hs {
			if h.level == level {
				top = append(top, h)
			}
		}
		for i, h := range top {
			end := len(content)
			if i+1 < len(top) {
				end = top[i+1].idx
			}
			out = append(out, &candidate{title: h.text, line: content[h.idx].no, content: content[h.idx+1 : end]})
		}
		return out, joinLines(content[:top[0].idx])
	}
	items, other := listSteps(content, mask, 1)
	return items, joinLines(other)
}

var (
	reItemNum    = regexp.MustCompile(`^ {0,3}(\d+)[.)、．]\s+(.+)$`)
	reItemDi     = regexp.MustCompile(`^ {0,3}第\s*([一二三四五六七八九十百\d]+)\s*步\s*[：:、.．]?\s*(.+)$`)
	reItemBuzhou = regexp.MustCompile(`(?i)^ {0,3}(?:步骤|step)\s*(\d+|[一二三四五六七八九十]+)\s*[：:.、．\-–]\s*(.+)$`)
)

func listItem(text string) (string, bool) {
	for _, re := range []*regexp.Regexp{reItemNum, reItemDi, reItemBuzhou} {
		if m := re.FindStringSubmatch(text); m != nil {
			return strings.TrimSpace(m[2]), true
		}
	}
	return "", false
}

// listSteps finds a top-level numbered list and makes each item a step.
// Everything between two items belongs to the first; after the last item,
// indented lines, blank lines and code blocks still belong to it.
func listSteps(ls []line, mask []bool, min int) ([]*candidate, []line) {
	var idx []int
	for i, l := range ls {
		if mask[i] {
			continue
		}
		if _, ok := listItem(l.text); ok {
			idx = append(idx, i)
		}
	}
	if len(idx) < min {
		return nil, ls
	}
	other := append([]line{}, ls[:idx[0]]...)
	var out []*candidate
	for n, i := range idx {
		title, _ := listItem(ls[i].text)
		end := len(ls)
		if n+1 < len(idx) {
			end = idx[n+1]
		} else {
			// The last item ends at the first unindented prose line after a blank line.
			sawBlank := false
			for j := i + 1; j < len(ls); j++ {
				t := ls[j].text
				if strings.TrimSpace(t) == "" {
					sawBlank = true
					continue
				}
				if sawBlank && !mask[j] && !strings.HasPrefix(t, " ") && !strings.HasPrefix(t, "\t") {
					end = j
					break
				}
				sawBlank = false
			}
			other = append(other, ls[end:]...)
		}
		out = append(out, &candidate{title: title, line: ls[i].no, content: dedent(ls[i+1 : end])})
	}
	return out, other
}

func dedent(ls []line) []line {
	min := -1
	for _, l := range ls {
		if strings.TrimSpace(l.text) == "" {
			continue
		}
		n := len(l.text) - len(strings.TrimLeft(l.text, " \t"))
		if min < 0 || n < min {
			min = n
		}
	}
	if min <= 0 {
		return ls
	}
	out := make([]line, len(ls))
	for i, l := range ls {
		t := l.text
		if len(t) >= min {
			t = t[min:]
		} else {
			t = strings.TrimLeft(t, " \t")
		}
		out[i] = line{no: l.no, text: t}
	}
	return out
}

// ---------------------------------------------------------------- titles

var (
	reNumPrefix = regexp.MustCompile(`^(?:\d+\s*[.)、．:：]\s*|第\s*[一二三四五六七八九十百\d]+\s*步\s*[：:、.．\-–]?\s*|(?i:步骤|step)\s*(?:\d+|[一二三四五六七八九十]+)\s*[：:.、．\-–]\s*|[一二三四五六七八九十]+\s*[、.．]\s*)`)
	reTagSuffix = regexp.MustCompile(`\s*[（(]([^（）()]+)[）)]\s*$`)
)

// titleTags are markers allowed in parentheses at the end of a step title.
var titleTags = map[string]string{
	"并行": "parallel", "可并行": "parallel", "同时": "parallel", "同时执行": "parallel", "parallel": "parallel",
	"命令": "shell", "shell": "shell", "脚本": "shell", "bash": "shell",
	"失败继续": "continue", "失败可继续": "continue", "允许失败": "continue", "可选": "continue", "optional": "continue",
	"总是执行": "always", "总是": "always", "always": "always",
}

// splitTitle strips numbering and recognised "（并行）" style tags.
func splitTitle(t string) (string, []string) {
	t = strings.TrimSpace(t)
	var tags []string
	for {
		m := reTagSuffix.FindStringSubmatchIndex(t)
		if m == nil {
			break
		}
		inner := t[m[2]:m[3]]
		var found []string
		ok := true
		for _, p := range strings.FieldsFunc(inner, func(r rune) bool { return r == ',' || r == '，' || r == '、' || r == ' ' || r == '/' }) {
			tag, known := titleTags[strings.ToLower(strings.TrimSpace(p))]
			if !known {
				ok = false
				break
			}
			found = append(found, tag)
		}
		if !ok || len(found) == 0 {
			break
		}
		tags = append(tags, found...)
		t = strings.TrimSpace(t[:m[0]])
	}
	if loc := reNumPrefix.FindStringIndex(t); loc != nil && loc[0] == 0 && strings.TrimSpace(t[loc[1]:]) != "" {
		t = strings.TrimSpace(t[loc[1]:])
	}
	t = strings.Trim(t, "*_`")
	return t, tags
}

func cleanTitle(t string) string {
	c, _ := splitTitle(t)
	return c
}

var reNormDrop = regexp.MustCompile("[\\s「」『』“”\"'`《》<>*_：:，,。.!！?？]+")

// normTitle reduces a title to a form used for matching references.
func normTitle(t string) string {
	return strings.ToLower(reNormDrop.ReplaceAllString(cleanTitle(t), ""))
}

// ---------------------------------------------------------------- helpers

func slug(s string) string {
	s = strings.ToLower(s)
	s = reSlugBad.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func joinLines(ls []line) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.text
	}
	return strings.Join(parts, "\n")
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

var reTemplate = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Render replaces {{path}} placeholders using lookup. Unknown paths are left as-is.
func Render(s string, lookup func(path string) (string, bool)) string {
	return reTemplate.ReplaceAllStringFunc(s, func(m string) string {
		p := reTemplate.FindStringSubmatch(m)[1]
		if v, ok := lookup(p); ok {
			return v
		}
		return m
	})
}
