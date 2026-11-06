package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reNumRef = regexp.MustCompile(`(?i)^(?:#|步骤|第|step\s*-?)?\s*(\d+|[一二三四五六七八九十]+)\s*步?$`)

// ResolveRef finds the step a reference points at: an id, a step number
// ("2", "步骤2", "第二步"), a title, or an unambiguous part of a title.
func (w *Workflow) ResolveRef(ref string) (*Step, error) {
	return w.resolveRef(ref, nil)
}

func (w *Workflow) resolveRef(ref string, self *Step) (*Step, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("空的步骤引用")
	}
	for _, s := range w.Steps {
		if s.ID == ref {
			return s, nil
		}
	}
	if m := reNumRef.FindStringSubmatch(ref); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			n, _ = cnNumber(m[1])
		}
		if n >= 1 && n <= len(w.Steps) {
			return w.Steps[n-1], nil
		}
		return nil, fmt.Errorf("没有第 %d 个步骤（共 %d 个）", n, len(w.Steps))
	}
	want := normTitle(ref)
	if want == "" {
		return nil, fmt.Errorf("找不到步骤「%s」", ref)
	}
	for _, s := range w.Steps {
		if normTitle(s.Title) == want {
			return s, nil
		}
	}
	var hits []*Step
	for _, s := range w.Steps {
		if s != self && strings.Contains(normTitle(s.Title), want) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("找不到步骤「%s」（现有步骤：%s）", ref, w.titleList())
	default:
		var names []string
		for _, h := range hits {
			names = append(names, "「"+h.Title+"」")
		}
		return nil, fmt.Errorf("「%s」同时匹配 %s，请写完整的步骤名或编号", ref, strings.Join(names, "、"))
	}
}

func (w *Workflow) titleList() string {
	var names []string
	for _, s := range w.Steps {
		names = append(names, fmt.Sprintf("%d.%s", s.Number, s.Title))
	}
	return strings.Join(names, "，")
}

// resolve fills in dependencies, then validates them.
func resolve(w *Workflow) error {
	// Steps marked parallel join the previous step's group; a step that is not
	// parallel and has no explicit dependencies waits for the whole previous group.
	var group []*Step
	var groupDeps []string
	for i, st := range w.Steps {
		switch {
		case st.depsSet:
			ids := []string{}
			for _, ref := range st.deps {
				dep, err := w.resolveDeps(ref, st)
				if err != nil {
					return errAt(st.Line, "步骤「%s」的依赖：%v", st.Title, err)
				}
				for _, d := range dep {
					if !contains(ids, d.ID) {
						ids = append(ids, d.ID)
					}
				}
			}
			st.DependsOn = ids
			group, groupDeps = []*Step{st}, ids
		case st.Parallel && i > 0:
			st.DependsOn = append([]string{}, groupDeps...)
			st.AutoDeps = true
			group = append(group, st)
		default:
			ids := []string{}
			for _, g := range group {
				ids = append(ids, g.ID)
			}
			st.DependsOn = ids
			st.AutoDeps = true
			group, groupDeps = []*Step{st}, ids
		}
	}
	for _, st := range w.Steps {
		for _, d := range st.DependsOn {
			if d == st.ID {
				return errAt(st.Line, "步骤「%s」不能依赖它自己", st.Title)
			}
		}
	}
	if err := checkCycles(w.Steps); err != nil {
		return err
	}
	byID := map[string]*Step{}
	for _, s := range w.Steps {
		byID[s.ID] = s
	}
	for _, st := range w.Steps {
		if st.Session == "" {
			continue
		}
		ref, err := w.resolveRef(st.Session, st)
		if err != nil {
			return errAt(st.Line, "步骤「%s」的沿用会话：%v", st.Title, err)
		}
		if ref.Type != TypeAgent || st.Type != TypeAgent {
			return errAt(st.Line, "步骤「%s」：只有两个 Agent 步骤之间才能沿用会话", st.Title)
		}
		if !isAncestor(byID, st, ref.ID) {
			return errAt(st.Line, "步骤「%s」：要沿用会话的「%s」必须是它的上游步骤", st.Title, ref.Title)
		}
		st.Session = ref.ID
	}
	return nil
}

// resolveDeps resolves one dependency reference; if the whole text does not
// name a step, it is split on "和 / 与 / 及 / and" and each part resolved.
func (w *Workflow) resolveDeps(ref string, self *Step) ([]*Step, error) {
	s, err := w.resolveRef(ref, self)
	if err == nil {
		return []*Step{s}, nil
	}
	parts := regexp.MustCompile(`\s*(?:和|与|及|以及|\band\b)\s*`).Split(ref, -1)
	if len(parts) < 2 {
		return nil, err
	}
	var out []*Step
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		s, perr := w.resolveRef(p, self)
		if perr != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func checkCycles(steps []*Step) error {
	byID := map[string]*Step{}
	for _, s := range steps {
		byID[s.ID] = s
	}
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(s *Step, path []string) error
	visit = func(s *Step, path []string) error {
		color[s.ID] = grey
		path = append(path, s.Title)
		for _, d := range s.DependsOn {
			switch color[d] {
			case grey:
				return errAt(s.Line, "依赖形成了循环：%s → %s", strings.Join(path, " → "), byID[d].Title)
			case white:
				if err := visit(byID[d], path); err != nil {
					return err
				}
			}
		}
		color[s.ID] = black
		return nil
	}
	for _, s := range steps {
		if color[s.ID] == white {
			if err := visit(s, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func isAncestor(ids map[string]*Step, st *Step, target string) bool {
	seen := map[string]bool{}
	stack := append([]string{}, st.DependsOn...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == target {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, ids[id].DependsOn...)
	}
	return false
}

// Layers groups step IDs into dependency levels, useful for display.
func (w *Workflow) Layers() [][]string {
	level := map[string]int{}
	byID := map[string]*Step{}
	for _, s := range w.Steps {
		byID[s.ID] = s
	}
	var depth func(s *Step) int
	depth = func(s *Step) int {
		if l, ok := level[s.ID]; ok {
			return l
		}
		l := 0
		for _, d := range s.DependsOn {
			if dl := depth(byID[d]) + 1; dl > l {
				l = dl
			}
		}
		level[s.ID] = l
		return l
	}
	max := 0
	for _, s := range w.Steps {
		if l := depth(s); l > max {
			max = l
		}
	}
	out := make([][]string, max+1)
	for _, s := range w.Steps {
		out[level[s.ID]] = append(out[level[s.ID]], s.ID)
	}
	for _, l := range out {
		sort.Strings(l)
	}
	return out
}
