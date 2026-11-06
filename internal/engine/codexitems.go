package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// threadItem mirrors the fields of the app-server v2 ThreadItem union that
// AgentHost displays. Unknown variants are kept as raw JSON.
type threadItem struct {
	Type              string                    `json:"type"`
	ID                string                    `json:"id"`
	Text              string                    `json:"text"`
	Phase             string                    `json:"phase"`
	Summary           []string                  `json:"summary"`
	Content           json.RawMessage           `json:"content"`
	Command           json.RawMessage           `json:"command"`
	Cwd               string                    `json:"cwd"`
	AggregatedOutput  *string                   `json:"aggregatedOutput"`
	ExitCode          *int                      `json:"exitCode"`
	DurationMs        *int64                    `json:"durationMs"`
	Status            string                    `json:"status"`
	Changes           []rawChange               `json:"changes"`
	Server            string                    `json:"server"`
	Tool              string                    `json:"tool"`
	Namespace         *string                   `json:"namespace"`
	Arguments         json.RawMessage           `json:"arguments"`
	Result            json.RawMessage           `json:"result"`
	Error             *struct{ Message string } `json:"error"`
	ContentItems      json.RawMessage           `json:"contentItems"`
	Success           *bool                     `json:"success"`
	Query             string                    `json:"query"`
	Prompt            *string                   `json:"prompt"`
	Model             *string                   `json:"model"`
	ReceiverThreadIDs []string                  `json:"receiverThreadIds"`
	AgentsStates      json.RawMessage           `json:"agentsStates"`
	AgentPath         string                    `json:"agentPath"`
	Kind              string                    `json:"kind"`
	Path              string                    `json:"path"`
	StartedAtMs       *int64                    `json:"startedAtMs"`
	CompletedAtMs     *int64                    `json:"completedAtMs"`
}

type rawChange struct {
	Path string          `json:"path"`
	Kind json.RawMessage `json:"kind"`
	Diff string          `json:"diff"`
}

func msTime(ms *int64) *time.Time {
	if ms == nil || *ms == 0 {
		return nil
	}
	t := time.UnixMilli(*ms)
	return &t
}

func normStatus(s string) string {
	switch s {
	case "inProgress", "in_progress", "running", "pendingInit":
		return "running"
	case "completed", "success":
		return "completed"
	case "failed", "errored", "error":
		return "failed"
	case "declined":
		return "declined"
	case "interrupted", "shutdown":
		return "stopped"
	}
	return s
}

// convertItem turns an app-server ThreadItem into an AgentHost Item.
// It returns nil for items AgentHost does not display (user messages).
func convertItem(raw json.RawMessage, completed bool) *Item {
	var t threadItem
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil
	}
	it := &Item{ID: t.ID, StartedAt: msTime(t.StartedAtMs), EndedAt: msTime(t.CompletedAtMs)}
	status := normStatus(t.Status)
	if status == "" {
		if completed {
			status = "completed"
		} else {
			status = "running"
		}
	}
	it.Status = status
	switch t.Type {
	case "userMessage", "hookPrompt":
		return nil
	case "agentMessage":
		it.Kind = ItemMessage
		it.Text = t.Text
		it.Phase = t.Phase
	case "reasoning":
		it.Kind = ItemReasoning
		it.Text = strings.Join(t.Summary, "\n\n")
		if it.Text == "" {
			var content []string
			_ = json.Unmarshal(t.Content, &content)
			it.Text = strings.Join(content, "\n\n")
		}
	case "plan":
		it.Kind = ItemPlan
		it.Text = t.Text
	case "commandExecution":
		it.Kind = ItemCommand
		it.Command = commandString(t.Command)
		it.Cwd = t.Cwd
		if t.AggregatedOutput != nil {
			it.Output = *t.AggregatedOutput
		}
		it.ExitCode = t.ExitCode
		if t.DurationMs != nil {
			it.DurationMs = *t.DurationMs
		}
	case "fileChange":
		it.Kind = ItemFileChange
		for _, c := range t.Changes {
			var k struct {
				Type     string  `json:"type"`
				MovePath *string `json:"move_path"`
			}
			if err := json.Unmarshal(c.Kind, &k); err != nil {
				_ = json.Unmarshal(c.Kind, &k.Type)
			}
			kind := k.Type
			if k.MovePath != nil && *k.MovePath != "" {
				kind = "move → " + *k.MovePath
			}
			it.Changes = append(it.Changes, FileChange{Path: c.Path, Kind: kind, Diff: c.Diff})
		}
	case "mcpToolCall":
		it.Kind = ItemTool
		it.Server = t.Server
		it.Tool = t.Tool
		it.Arguments = nonNullJSON(t.Arguments)
		it.Result = toolResultText(t.Result)
		if t.Error != nil {
			it.Text = t.Error.Message
		}
		if t.DurationMs != nil {
			it.DurationMs = *t.DurationMs
		}
	case "dynamicToolCall":
		if hiddenTools[t.Tool] {
			return nil
		}
		it.Kind = ItemTool
		if t.Namespace != nil {
			it.Server = *t.Namespace
		}
		it.Tool = t.Tool
		it.Arguments = nonNullJSON(t.Arguments)
		it.Result = toolResultText(t.ContentItems)
		if t.Success != nil && !*t.Success && completed {
			it.Status = "failed"
		}
		if t.DurationMs != nil {
			it.DurationMs = *t.DurationMs
		}
	case "collabAgentToolCall":
		it.Kind = ItemAgent
		it.Tool = t.Tool
		if t.Prompt != nil {
			it.Text = *t.Prompt
		}
		if t.Model != nil {
			it.Server = *t.Model
		}
		it.Title = "Agent " + t.Tool
		if len(t.ReceiverThreadIDs) > 0 {
			it.Title += " → " + strings.Join(shortIDs(t.ReceiverThreadIDs), ", ")
		}
		it.Result = string(nonNullJSON(t.AgentsStates))
	case "subAgentActivity":
		it.Kind = ItemAgent
		it.Tool = t.Kind
		it.Title = "子 Agent " + t.AgentPath + " " + t.Kind
		if t.Model != nil {
			it.Server = *t.Model
		}
		it.Status = "completed"
	case "webSearch":
		it.Kind = ItemWebSearch
		it.Text = t.Query
		if completed {
			it.Status = "completed"
		}
	case "contextCompaction":
		it.Kind = ItemNotice
		it.Title = "上下文已压缩"
		it.Status = "completed"
	default:
		it.Kind = ItemOther
		it.Title = t.Type
		if t.Path != "" {
			it.Text = t.Path
		}
		it.Raw = append(json.RawMessage{}, raw...)
		if completed && it.Status == "running" {
			it.Status = "completed"
		}
	}
	return it
}

// hiddenTools are AgentHost's own dynamic tools; their effect is shown elsewhere.
var hiddenTools = map[string]bool{"report_progress": true}

func commandString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		return strings.Join(argv, " ")
	}
	return string(raw)
}

func nonNullJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return append(json.RawMessage{}, raw...)
}

// toolResultText flattens MCP-style content into readable text.
func toolResultText(raw json.RawMessage) string {
	raw = nonNullJSON(raw)
	if raw == nil {
		return ""
	}
	var res struct {
		Content           []map[string]any `json:"content"`
		StructuredContent any              `json:"structuredContent"`
	}
	var items []map[string]any
	if json.Unmarshal(raw, &res) == nil && res.Content != nil {
		items = res.Content
	} else if json.Unmarshal(raw, &items) != nil {
		return string(raw)
	}
	var parts []string
	for _, c := range items {
		if txt, ok := c["text"].(string); ok {
			parts = append(parts, txt)
		} else {
			b, _ := json.Marshal(c)
			parts = append(parts, string(b))
		}
	}
	if len(parts) == 0 && res.StructuredContent != nil {
		b, _ := json.MarshalIndent(res.StructuredContent, "", "  ")
		return string(b)
	}
	return strings.Join(parts, "\n")
}

func shortIDs(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		if len(id) > 8 {
			id = id[len(id)-8:]
		}
		out[i] = id
	}
	return out
}

// activityFor describes what an item means for "what is the agent doing now".
func activityFor(it *Item) string {
	switch it.Kind {
	case ItemReasoning:
		return "思考中"
	case ItemMessage:
		if it.Status == "running" {
			return "正在回复"
		}
		return ""
	case ItemCommand:
		return "执行命令: " + truncate(firstLine(it.Command), 120)
	case ItemFileChange:
		if len(it.Changes) > 0 {
			return fmt.Sprintf("修改文件: %s", it.Changes[0].Path)
		}
		return "修改文件"
	case ItemTool:
		return "调用工具: " + strings.Trim(it.Server+"/"+it.Tool, "/")
	case ItemWebSearch:
		return "搜索: " + it.Text
	case ItemAgent:
		return "调用 Agent: " + it.Tool
	case ItemPlan:
		return "制定计划"
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// avoid cutting a UTF-8 sequence in half
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n] + "…"
}

// keepTail caps s at max bytes, keeping the end.
func keepTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := len(s) - max
	for cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut++
	}
	return "…[前面的输出已截断]…\n" + s[cut:]
}
