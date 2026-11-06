package workflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a step timeout (nanoseconds in JSON).
type Duration = time.Duration

// keyAlias maps every accepted setting name to its canonical key.
var keyAlias = map[string]string{
	"id": "id", "编号": "id",
	"type": "type", "agent": "type", "类型": "type", "方式": "type",
	"depends_on": "depends_on", "needs": "depends_on", "after": "depends_on", "depends": "depends_on",
	"依赖": "depends_on", "依赖于": "depends_on", "依赖步骤": "depends_on", "前置": "depends_on", "前置步骤": "depends_on",
	"if": "if", "when": "if", "条件": "if", "执行条件": "if",
	"retry": "retry", "retries": "retry", "重试": "retry", "重试次数": "retry",
	"timeout": "timeout", "超时": "timeout", "超时时间": "timeout",
	"continue_on_error": "continue_on_error", "失败继续": "continue_on_error", "失败后继续": "continue_on_error",
	"允许失败": "continue_on_error", "忽略失败": "continue_on_error",
	"cwd": "cwd", "workdir": "cwd", "目录": "cwd", "工作目录": "cwd",
	"model": "model", "模型": "model",
	"sandbox": "sandbox", "沙箱": "sandbox", "权限": "sandbox",
	"session": "session", "沿用": "session", "沿用会话": "session", "继续会话": "session",
	"run": "run", "command": "run", "命令": "run",
	"workflow": "workflow", "uses": "workflow", "调用": "workflow", "子流程": "workflow",
	"with": "with", "参数": "with",
	"parallel": "parallel", "并行": "parallel",
}

// flagLines are whole-line settings without a value.
var flagLines = map[string][2]string{
	"并行": {"parallel", "true"}, "可并行": {"parallel", "true"}, "与上一步并行": {"parallel", "true"},
	"和上一步并行": {"parallel", "true"}, "同时执行": {"parallel", "true"}, "parallel": {"parallel", "true"},
	"失败继续": {"continue_on_error", "true"}, "失败后继续": {"continue_on_error", "true"},
	"允许失败": {"continue_on_error", "true"}, "忽略失败": {"continue_on_error", "true"},
	"总是执行": {"if", "always"}, "始终执行": {"if", "always"},
	"失败时执行": {"if", "failure"}, "仅在失败时执行": {"if", "failure"}, "仅失败时执行": {"if", "failure"},
}

var (
	reSetting  = regexp.MustCompile(`^\s*(?:[-*]\s*)?([\p{L}_][\p{L}\p{N}_]*)\s*[:：]\s*(.*?)\s*$`)
	reFlagLine = regexp.MustCompile(`^\s*(?:[-*]\s*)?([\p{L}_]+)\s*[。.]?\s*$`)
)

// buildSteps interprets candidate bodies and assigns ids.
func buildSteps(cands []*candidate) ([]*Step, error) {
	var steps []*Step
	for i, c := range cands {
		st, err := buildStep(c, i+1)
		if err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	// Unique ids: explicit ids must be unique, generated ones get a suffix.
	explicit := map[string]bool{}
	for _, st := range steps {
		if st.ID == "" {
			continue
		}
		if explicit[st.ID] {
			return nil, errAt(st.Line, "步骤 ID %q 重复", st.ID)
		}
		explicit[st.ID] = true
	}
	used := map[string]bool{}
	for id := range explicit {
		used[id] = true
	}
	for i, st := range steps {
		if st.ID != "" {
			continue
		}
		base := slug(st.Title)
		if base == "" {
			base = fmt.Sprintf("step-%d", i+1)
		}
		id := base
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		used[id] = true
		st.ID = id
	}
	return steps, nil
}

func buildStep(c *candidate, number int) (*Step, error) {
	title, tags := splitTitle(c.title)
	if title == "" {
		title = fmt.Sprintf("步骤 %d", number)
	}
	st := &Step{Title: title, Number: number, Line: c.line, If: IfSuccess}
	for _, t := range tags {
		switch t {
		case "parallel":
			st.Parallel = true
		case "shell":
			st.Type = TypeShell
		case "continue":
			st.ContinueOnError = true
		case "always":
			st.If = IfAlways
		}
	}

	meta, rest, err := splitSettings(c.content)
	if err != nil {
		return nil, err
	}
	if err := applySettings(st, meta); err != nil {
		return nil, errAt(c.line, "步骤「%s」：%v", title, err)
	}

	if st.Type == "" {
		switch {
		case st.Workflow != "":
			st.Type = TypeWorkflow
		case st.Run != "":
			st.Type = TypeShell
		default:
			if code, ok := onlyShellBlock(rest); ok {
				st.Type = TypeShell
				st.Run = code
			} else {
				st.Type = TypeAgent
			}
		}
	}
	content := strings.TrimSpace(joinLines(rest))
	switch st.Type {
	case TypeShell:
		if st.Run == "" {
			code, ok := firstCodeBlock(rest)
			if !ok {
				return nil, errAt(c.line, "命令步骤「%s」需要一个代码块（```bash … ```）或一行「命令：…」", title)
			}
			st.Run = code
		}
	case TypeAgent:
		st.Prompt = content
		if st.Prompt == "" {
			// A bare heading is still a step: its title is the instruction.
			st.Prompt = title
		}
	case TypeWorkflow:
		if st.Workflow == "" {
			return nil, errAt(c.line, "子流程步骤「%s」需要写「调用：文件名.md」", title)
		}
		st.Prompt = content
	}
	return st, nil
}

// splitSettings takes the settings block from the top of a step: "键：值"
// lines, flag lines like "失败继续", or a ```yaml block.
func splitSettings(body []line) (map[string]string, []line, error) {
	i := 0
	for i < len(body) && strings.TrimSpace(body[i].text) == "" {
		i++
	}
	meta := map[string]string{}
	if i < len(body) {
		if m := reFence.FindStringSubmatch(body[i].text); m != nil && (m[2] == "yaml" || m[2] == "yml") {
			start := i
			j := i + 1
			for j < len(body) && !strings.HasPrefix(strings.TrimSpace(body[j].text), m[1][:3]) {
				j++
			}
			if j >= len(body) {
				return nil, nil, errAt(body[start].no, "yaml 代码块没有结束")
			}
			var y map[string]any
			if err := yaml.Unmarshal([]byte(joinLines(body[start+1:j])), &y); err != nil {
				return nil, nil, errAt(body[start].no, "步骤设置格式错误：%v", err)
			}
			for k, v := range y {
				key, ok := keyAlias[strings.ToLower(strings.TrimSpace(k))]
				if !ok {
					continue
				}
				if key == "with" || key == "depends_on" {
					b, _ := yaml.Marshal(v)
					meta[key] = "yaml:" + string(b)
					continue
				}
				meta[key] = str(v)
			}
			return meta, body[j+1:], nil
		}
	}
	for i < len(body) {
		t := body[i].text
		if m := reSetting.FindStringSubmatch(t); m != nil {
			if key, ok := keyAlias[strings.ToLower(m[1])]; ok {
				meta[key] = strings.Trim(m[2], "`")
				i++
				continue
			}
		}
		if m := reFlagLine.FindStringSubmatch(t); m != nil {
			if f, ok := flagLines[strings.ToLower(m[1])]; ok {
				meta[f[0]] = f[1]
				i++
				continue
			}
		}
		break
	}
	return meta, body[i:], nil
}

func applySettings(st *Step, meta map[string]string) error {
	for k, v := range meta {
		switch k {
		case "id":
			st.ID = v
		case "type":
			t, ok := normType(v)
			if !ok {
				return fmt.Errorf("不认识的类型 %q（可选：agent、命令/shell、子流程/workflow）", v)
			}
			st.Type = t
		case "depends_on":
			st.depsSet = true
			st.deps = refList(v)
		case "if":
			c, ok := normIf(v)
			if !ok {
				return fmt.Errorf("条件只能是 成功时 / 失败时 / 总是（success / failure / always），不能是 %q", v)
			}
			st.If = c
		case "retry":
			n, ok := parseCount(v)
			if !ok || n < 0 || n > 10 {
				return fmt.Errorf("重试次数应为 0–10 的整数，不能是 %q", v)
			}
			st.Retry = n
		case "timeout":
			d, ok := parseDuration(v)
			if !ok {
				return fmt.Errorf("超时写法不对：%q（例如 90s、10m、10分钟、1小时）", v)
			}
			st.Timeout = d
		case "continue_on_error":
			b, ok := parseBool(v)
			if !ok {
				return fmt.Errorf("失败继续 应为 是/否（true/false），不能是 %q", v)
			}
			st.ContinueOnError = b
		case "parallel":
			b, ok := parseBool(v)
			if !ok {
				return fmt.Errorf("并行 应为 是/否，不能是 %q", v)
			}
			st.Parallel = b
		case "cwd":
			st.Cwd = v
		case "model":
			st.Model = v
		case "sandbox":
			sb, ok := normSandbox(v)
			if !ok {
				return fmt.Errorf("沙箱只能是 只读 / 可写 / 完全访问（read-only / workspace-write / danger-full-access）")
			}
			st.Sandbox = sb
		case "session":
			st.Session = v
		case "run":
			st.Run = v
		case "workflow":
			st.Workflow = v
		case "with":
			m, err := parseWith(v)
			if err != nil {
				return err
			}
			st.With = m
		}
	}
	return nil
}

func normType(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "agent", "codex", "llm", "ai", "智能体", "代理":
		return TypeAgent, true
	case "shell", "bash", "sh", "command", "cmd", "命令", "脚本", "终端", "shell命令":
		return TypeShell, true
	case "workflow", "subworkflow", "call", "task", "子流程", "调用", "流程":
		return TypeWorkflow, true
	}
	return "", false
}

func normIf(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "success", "succeeded", "on_success", "成功", "成功时", "成功后":
		return IfSuccess, true
	case "failure", "failed", "on_failure", "失败", "失败时", "失败后", "仅失败时":
		return IfFailure, true
	case "always", "总是", "始终", "无论成败", "一定执行", "总是执行":
		return IfAlways, true
	}
	return "", false
}

func normSandbox(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "read-only", "readonly", "只读":
		return "read-only", true
	case "workspace-write", "write", "可写", "读写", "工作区可写":
		return "workspace-write", true
	case "danger-full-access", "full", "full-access", "完全访问", "无限制", "不限制":
		return "danger-full-access", true
	}
	return "", false
}

func parseBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "true", "yes", "y", "on", "1", "是", "对", "允许", "开", "要":
		return true, true
	case "false", "no", "n", "off", "0", "否", "不", "不允许", "关", "不要":
		return false, true
	}
	return false, false
}

var cnDigits = map[rune]int{'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// parseCount reads "2", "2次", "两次", "十" and similar.
func parseCount(v string) (int, bool) {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "次"))
	if n, err := strconv.Atoi(v); err == nil {
		return n, true
	}
	return cnNumber(v)
}

func cnNumber(v string) (int, bool) {
	rs := []rune(v)
	if len(rs) == 0 {
		return 0, false
	}
	if len(rs) == 1 {
		if rs[0] == '十' {
			return 10, true
		}
		n, ok := cnDigits[rs[0]]
		return n, ok
	}
	if len(rs) == 2 && rs[0] == '十' {
		n, ok := cnDigits[rs[1]]
		return 10 + n, ok
	}
	if len(rs) == 2 && rs[1] == '十' {
		n, ok := cnDigits[rs[0]]
		return n * 10, ok
	}
	if len(rs) == 3 && rs[1] == '十' {
		a, ok1 := cnDigits[rs[0]]
		b, ok2 := cnDigits[rs[2]]
		return a*10 + b, ok1 && ok2
	}
	return 0, false
}

var reCnDuration = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(小时|钟头|时|分钟|分|秒钟|秒|h|hr|hours?|m|min|minutes?|s|sec|seconds?)`)

func parseDuration(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d, true
	}
	ms := reCnDuration.FindAllStringSubmatch(strings.ToLower(v), -1)
	if len(ms) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, m := range ms {
		f, _ := strconv.ParseFloat(m[1], 64)
		unit := time.Second
		switch m[2] {
		case "小时", "钟头", "时", "h", "hr", "hour", "hours":
			unit = time.Hour
		case "分钟", "分", "m", "min", "minute", "minutes":
			unit = time.Minute
		}
		total += time.Duration(f * float64(unit))
	}
	return total, total > 0
}

// parseWith reads sub-workflow variables: a yaml mapping or "a=1, b=2".
func parseWith(v string) (map[string]string, error) {
	out := map[string]string{}
	if strings.HasPrefix(v, "yaml:") {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(strings.TrimPrefix(v, "yaml:")), &m); err != nil || m == nil {
			return nil, fmt.Errorf("参数需要写成 名称: 值 的列表")
		}
		for k, x := range m {
			out[k] = str(x)
		}
		return out, nil
	}
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' }) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			kv = strings.SplitN(part, "：", 2)
		}
		if len(kv) != 2 {
			kv = strings.SplitN(part, ":", 2)
		}
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, fmt.Errorf("参数写法：名称=值，多个用逗号分隔（例如 env=staging, region=cn）")
		}
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return out, nil
}

// refList splits a dependency value into references. Plain commas, Chinese
// enumeration commas and yaml lists are all accepted; "无" means none.
func refList(v string) []string {
	if strings.HasPrefix(v, "yaml:") {
		var xs []any
		if yaml.Unmarshal([]byte(strings.TrimPrefix(v, "yaml:")), &xs) == nil {
			var out []string
			for _, x := range xs {
				if s := str(x); s != "" {
					out = append(out, s)
				}
			}
			return emptyIfNone(out)
		}
		var s string
		_ = yaml.Unmarshal([]byte(strings.TrimPrefix(v, "yaml:")), &s)
		v = s
	}
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ';' || r == '；'
	}) {
		p = strings.Trim(strings.TrimSpace(p), "`\"'「」“”")
		if p != "" {
			out = append(out, p)
		}
	}
	return emptyIfNone(out)
}

func emptyIfNone(xs []string) []string {
	if len(xs) == 1 {
		switch strings.ToLower(xs[0]) {
		case "无", "没有", "none", "不依赖", "无依赖", "-":
			return []string{}
		}
	}
	if xs == nil {
		return []string{}
	}
	return xs
}

var shellLangs = setOf("bash", "sh", "shell", "zsh", "console", "shell-session")

// onlyShellBlock reports whether the body is a single shell code block and nothing else.
func onlyShellBlock(body []line) (string, bool) {
	var ls []line
	for _, l := range body {
		if strings.TrimSpace(l.text) != "" || len(ls) > 0 {
			ls = append(ls, l)
		}
	}
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1].text) == "" {
		ls = ls[:len(ls)-1]
	}
	if len(ls) < 2 {
		return "", false
	}
	m := reFence.FindStringSubmatch(ls[0].text)
	if m == nil || !shellLangs[strings.ToLower(m[2])] {
		return "", false
	}
	if !strings.HasPrefix(strings.TrimSpace(ls[len(ls)-1].text), m[1][:3]) {
		return "", false
	}
	for _, l := range ls[1 : len(ls)-1] {
		if strings.HasPrefix(strings.TrimSpace(l.text), m[1][:3]) {
			return "", false // more than one block
		}
	}
	return joinLines(ls[1 : len(ls)-1]), true
}

func firstCodeBlock(body []line) (string, bool) {
	for i := 0; i < len(body); i++ {
		m := reFence.FindStringSubmatch(body[i].text)
		if m == nil {
			continue
		}
		var out []string
		for j := i + 1; j < len(body); j++ {
			if strings.HasPrefix(strings.TrimSpace(body[j].text), m[1][:3]) {
				return strings.Join(out, "\n"), true
			}
			out = append(out, body[j].text)
		}
		return strings.Join(out, "\n"), true
	}
	return "", false
}
