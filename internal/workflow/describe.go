package workflow

import (
	"fmt"
	"strings"
)

var typeNames = map[string]string{TypeAgent: "Agent", TypeShell: "命令", TypeWorkflow: "子流程"}
var ifNames = map[string]string{IfFailure: "失败时", IfAlways: "总是"}

// Describe explains how AgentHost understood a workflow, in the same terms
// a person uses to write one. It is shown to Codex by check_workflow and
// printed by `agenthost check`.
func Describe(wf *Workflow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "名称：%s\n", wf.Name)
	if wf.Mode == ModeAgent {
		fmt.Fprintf(&b, "执行方式：交给 Codex 编排（%s）\n", wf.ModeReason)
		if wf.DetectedSteps > 0 {
			fmt.Fprintf(&b, "文档里能识别出 %d 个步骤，但不会按步骤调度。\n", wf.DetectedSteps)
		} else {
			b.WriteString("提示：如需按步骤显示和调度，请把每个步骤写成 “## 1. 标题”。\n")
		}
		return b.String()
	}
	fmt.Fprintf(&b, "执行方式：按步骤调度，共 %d 个步骤\n", len(wf.Steps))
	if len(wf.Vars) > 0 {
		var vs []string
		for k, v := range wf.Vars {
			vs = append(vs, k+"="+v)
		}
		fmt.Fprintf(&b, "变量：%s\n", strings.Join(vs, "，"))
	}
	if wf.Instructions != "" {
		fmt.Fprintf(&b, "全局说明：%d 字", len([]rune(wf.Instructions)))
		if len(wf.ContextSections) > 0 {
			fmt.Fprintf(&b, "（含小节：%s）", strings.Join(wf.ContextSections, "、"))
		}
		b.WriteString("\n")
	}
	byID := map[string]*Step{}
	for _, s := range wf.Steps {
		byID[s.ID] = s
	}
	for _, s := range wf.Steps {
		fmt.Fprintf(&b, "%d. %s [%s]", s.Number, s.Title, typeNames[s.Type])
		var deps []string
		for _, d := range s.DependsOn {
			deps = append(deps, byID[d].Title)
		}
		switch {
		case len(deps) == 0:
			b.WriteString(" 起始步骤")
		case s.AutoDeps && s.Parallel:
			fmt.Fprintf(&b, " 与上一步并行，等待：%s", strings.Join(deps, "、"))
		default:
			fmt.Fprintf(&b, " 等待：%s", strings.Join(deps, "、"))
		}
		var extra []string
		if n := ifNames[s.If]; n != "" {
			extra = append(extra, "条件："+n)
		}
		if s.ContinueOnError {
			extra = append(extra, "失败继续")
		}
		if s.Retry > 0 {
			extra = append(extra, fmt.Sprintf("重试 %d 次", s.Retry))
		}
		if s.Timeout > 0 {
			extra = append(extra, "超时 "+s.Timeout.String())
		}
		if s.Sandbox != "" {
			extra = append(extra, "沙箱 "+s.Sandbox)
		}
		if s.Cwd != "" {
			extra = append(extra, "目录 "+s.Cwd)
		}
		if s.Session != "" {
			extra = append(extra, "沿用「"+byID[s.Session].Title+"」的会话")
		}
		if s.Workflow != "" {
			extra = append(extra, "调用 "+s.Workflow)
		}
		if len(extra) > 0 {
			fmt.Fprintf(&b, "（%s）", strings.Join(extra, "，"))
		}
		b.WriteString("\n")
		switch s.Type {
		case TypeShell:
			fmt.Fprintf(&b, "   命令：%s\n", oneLine(s.Run, 120))
		case TypeAgent:
			fmt.Fprintf(&b, "   内容：%s\n", oneLine(s.Prompt, 120))
		}
	}
	for _, w := range Lint(wf) {
		fmt.Fprintf(&b, "提示：%s\n", w)
	}
	return b.String()
}

// Lint returns advice about things that parse but are probably not intended.
func Lint(wf *Workflow) []string {
	var out []string
	if wf.Mode != ModeSteps {
		return nil
	}
	for _, s := range wf.Steps {
		if s.Type == TypeAgent && strings.TrimSpace(s.Prompt) == s.Title {
			out = append(out, fmt.Sprintf("步骤 %d「%s」只有标题，没有具体要求。", s.Number, s.Title))
		}
		if s.Type == TypeShell && strings.TrimSpace(s.Run) == "" {
			out = append(out, fmt.Sprintf("步骤 %d「%s」的命令是空的。", s.Number, s.Title))
		}
		if s.Type == TypeAgent && reFenceAny.MatchString(s.Prompt) && strings.Count(s.Prompt, "\n") < 3 {
			out = append(out, fmt.Sprintf("步骤 %d「%s」看起来只是一条命令，如需直接执行请加“类型：命令”。", s.Number, s.Title))
		}
		if len([]rune(s.Title)) > 20 {
			out = append(out, fmt.Sprintf("步骤 %d 的标题偏长，建议精简到 12 个字以内，细节放进正文。", s.Number))
		}
	}
	if len(wf.Steps) > 12 {
		out = append(out, fmt.Sprintf("共 %d 个步骤，偏多，可以考虑合并相关步骤。", len(wf.Steps)))
	}
	return out
}

var reFenceAny = reFence

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
