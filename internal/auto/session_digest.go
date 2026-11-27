package auto

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// SessionDigest summarizes one conversation's request, key actions/decisions,
// errors, and outcome once per conversation ID in projectPassCache.
type SessionDigest struct {
	ID        string
	Label     string
	Request   string
	Actions   []string
	Decisions []string
	Errors    []string
	Outcome   string
}

func convSourceID(c convSource) string {
	id := strings.TrimSpace(strings.TrimPrefix(c.label, "antigravity "))
	if id == "" {
		return c.label
	}
	return id
}

func (r *run) sessionDigestFor(c convSource) SessionDigest {
	id := convSourceID(c)
	if r.sweepCache != nil {
		if d, ok := r.sweepCache.digests[id]; ok {
			return d
		}
	}
	d := BuildSessionDigest(id, c.label, c.text)
	if r.sweepCache != nil {
		if r.sweepCache.digests == nil {
			r.sweepCache.digests = map[string]SessionDigest{}
		}
		r.sweepCache.digests[id] = d
		r.sweepCache.digestBuilds++
	}
	return d
}

// BuildSessionDigest extracts a bounded summary from JSONL or plain-text
// conversation content.
func BuildSessionDigest(id, label, raw string) SessionDigest {
	d := SessionDigest{ID: id, Label: label}
	for _, rawLine := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		d.ingestLine(line)
	}
	if d.Request == "" && d.Outcome != "" {
		d.Request = d.Outcome
	}
	return d
}

func (d *SessionDigest) ingestLine(line string) {
	role, status, content, tools := parseTranscriptStep(line)
	text := oneLine(strings.TrimSpace(content))
	if text == "" {
		text = oneLine(line)
	}
	capped := trail.TruncateUTF8(text, 320)
	if d.Request == "" && (role == "user" || role == "USER_INPUT" || role == "") {
		d.Request = capped
	}
	for _, tc := range tools {
		d.Actions = appendUniqueBounded(d.Actions, tc, 6, 160)
	}
	low := strings.ToLower(text)
	if isDecisionLine(low) {
		d.Decisions = appendUniqueBounded(d.Decisions, capped, 4, 200)
	}
	if status == "ERROR" || isErrorLine(low) {
		d.Errors = appendUniqueBounded(d.Errors, capped, 4, 200)
	}
	if role != "user" && role != "USER_INPUT" {
		d.Outcome = capped
	} else if d.Outcome == "" {
		d.Outcome = capped
	}
}

func parseTranscriptStep(line string) (role, status, content string, tools []string) {
	if !strings.HasPrefix(line, "{") {
		return "", "", line, nil
	}
	var m map[string]any
	if json.Unmarshal([]byte(line), &m) != nil {
		return "", "", line, nil
	}
	role = firstString(m, "type", "role", "source")
	status = strings.ToUpper(firstString(m, "status"))
	content = firstString(m, "content", "text", "msg", "prompt", "response")
	if tcs, ok := m["tool_calls"].([]any); ok {
		for _, rawTC := range tcs {
			if tm, ok := rawTC.(map[string]any); ok {
				if name := firstString(tm, "name", "tool"); name != "" {
					tools = append(tools, name)
				}
			}
		}
	}
	if tool := firstString(m, "tool"); tool != "" {
		tools = append(tools, tool)
	}
	if content == "" {
		content = line
	}
	return role, status, content, tools
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func isDecisionLine(low string) bool {
	for _, kw := range []string{"decision", "decided", "adr-", "plan-", "chose", "because"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func isErrorLine(low string) bool {
	for _, kw := range []string{"error:", "failed", "panic:", "denied", "exit 1", "exit 2"} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

func appendUniqueBounded(list []string, val string, maxItems, maxBytes int) []string {
	if len(list) >= maxItems {
		return list
	}
	v := trail.TruncateUTF8(oneLine(strings.TrimSpace(val)), maxBytes)
	if v == "" || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// Format renders a compact Markdown digest section for learning inputs.
func (d SessionDigest) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Conversation %s\n### SessionDigest (%s)\n", oneLine(d.Label), oneLine(d.ID))
	if d.Request != "" {
		fmt.Fprintf(&b, "- Request: %s\n", d.Request)
	}
	if len(d.Actions) > 0 {
		fmt.Fprintf(&b, "- Actions: %s\n", strings.Join(d.Actions, "; "))
	}
	if len(d.Decisions) > 0 {
		fmt.Fprintf(&b, "- Decisions: %s\n", strings.Join(d.Decisions, " | "))
	}
	if len(d.Errors) > 0 {
		fmt.Fprintf(&b, "- Errors: %s\n", strings.Join(d.Errors, " | "))
	}
	if d.Outcome != "" && d.Outcome != d.Request {
		fmt.Fprintf(&b, "- Outcome: %s\n", d.Outcome)
	}
	b.WriteByte('\n')
	return b.String()
}
