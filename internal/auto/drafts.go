package auto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

const (
	draftMaxBytes = 4096
	maxDraftLines = 100
)

type draftLine struct {
	Kind              string
	Action            string
	ID                string
	Target            string
	Title             string
	Body              string
	Quote             string
	MemoryType        string
	Scope             string
	Sessions          string
	SignalID          string
	ReportDate        string
	ActionScope       string
	RecommendedAction string
}

// processDraftLines validates each JSONL line from a worker's auto-drafts.jsonl
// and writes valid memory, feedback and proposal records with provenance
// "derived" (§ Drafts, D8, D39). Invalid lines become report findings and are
// never stored.
func (r *run) processDraftLines(root, class string, data []byte) []Finding {
	var out []Finding
	lines := strings.Split(string(data), "\n")
	nonEmpty := 0
	for i, raw := range lines {
		lineNum := i + 1
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		nonEmpty++
		if nonEmpty > maxDraftLines {
			out = append(out, Finding{"draft", "invalid", class, fmt.Sprintf("line %d: over %d draft lines; remainder refused", lineNum, maxDraftLines)})
			break
		}
		d, err := parseAndValidateDraft(line)
		if err != nil {
			out = append(out, Finding{"draft", "invalid", class, fmt.Sprintf("line %d: %s", lineNum, oneLine(err.Error()))})
			continue
		}
		f, err := r.applyDraftLine(root, class, d)
		if err != nil {
			out = append(out, Finding{"draft", "invalid", class, fmt.Sprintf("line %d: %s", lineNum, oneLine(err.Error()))})
			continue
		}
		if f.Check != "" {
			out = append(out, f)
		}
	}
	return out
}

func parseAndValidateDraft(line string) (draftLine, error) {
	if len(line) > draftMaxBytes {
		return draftLine{}, errors.New("draft line is over 4 KiB")
	}
	if !utf8.ValidString(line) {
		return draftLine{}, errors.New("draft line is not valid UTF-8")
	}
	if redact.Redact(line) != line || redact.RedactJSONL(line+"\n") != line+"\n" {
		return draftLine{}, errors.New("draft line refused by redactor")
	}
	d, err := decodeDraftObject(line)
	if err != nil {
		return draftLine{}, err
	}
	if redactorRefusesDraft(d) {
		return draftLine{}, errors.New("draft field refused by redactor")
	}
	if hasControlDraftMeta(d) {
		return draftLine{}, errors.New("draft metadata contains control characters")
	}
	if len(d.Body) >= draftMaxBytes {
		return draftLine{}, errors.New("draft body is 4 KiB or larger")
	}
	if strings.TrimSpace(d.Body) == "" {
		return draftLine{}, errors.New("draft body is empty")
	}
	return d, nil
}

func redactorRefusesDraft(d draftLine) bool {
	for _, fld := range []string{
		d.Kind, d.Action, d.ID, d.Target, d.Title, d.Body, d.Quote,
		d.MemoryType, d.Scope, d.Sessions, d.SignalID, d.ReportDate,
		d.ActionScope, d.RecommendedAction,
	} {
		if redact.Redact(fld) != fld {
			return true
		}
	}
	return false
}

func hasControlDraftMeta(d draftLine) bool {
	for _, fld := range []string{
		d.ID, d.Title, d.MemoryType, d.Scope, d.Sessions,
		d.SignalID, d.ReportDate, d.ActionScope, d.RecommendedAction,
	} {
		if strings.ContainsAny(fld, "\r\n\x00") {
			return true
		}
	}
	return false
}

// decodeDraftObject reads one JSON object key by key, refusing repeated keys,
// unknown or case-folded keys, null or non-string values, and trailing tokens.
func decodeDraftObject(line string) (draftLine, error) {
	var d draftLine
	dec := json.NewDecoder(bytes.NewReader([]byte(line)))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return d, errors.New("not a JSON object")
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return d, errors.New("malformed JSON draft line")
		}
		key, ok := t.(string)
		if !ok || seen[key] {
			return d, fmt.Errorf("duplicate or invalid key %q", key)
		}
		seen[key] = true
		var val *string
		if err := dec.Decode(&val); err != nil || val == nil {
			return d, fmt.Errorf("key %q must be a string", key)
		}
		if err := setDraftField(&d, key, *val); err != nil {
			return d, err
		}
	}
	if _, err := dec.Token(); err != nil {
		return d, errors.New("malformed JSON draft line")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return d, errors.New("trailing data after JSON object")
	}
	return d, nil
}

func setDraftField(d *draftLine, key, val string) error {
	switch key {
	case "kind":
		d.Kind = val
	case "action":
		d.Action = val
	case "id":
		d.ID = val
	case "target":
		d.Target = val
	case "title":
		d.Title = val
	case "body":
		d.Body = val
	case "quote":
		d.Quote = val
	case "memory_type":
		d.MemoryType = val
	case "scope":
		d.Scope = val
	case "sessions":
		d.Sessions = val
	case "signal_id":
		d.SignalID = val
	case "report_date":
		d.ReportDate = val
	case "action_scope":
		d.ActionScope = val
	case "recommended_action":
		d.RecommendedAction = val
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

func (r *run) applyDraftLine(root, class string, d draftLine) (Finding, error) {
	switch d.Kind {
	case "finding":
		return applyFindingDraft(class, d)
	case "memory":
		return r.applyMemoryDraft(root, d)
	case "feedback":
		return r.applyFeedbackDraft(root, class, d)
	case "proposal":
		return r.applyProposalDraft(root, class, d)
	default:
		return Finding{}, fmt.Errorf("unknown draft kind %q", d.Kind)
	}
}

func applyFindingDraft(class string, d draftLine) (Finding, error) {
	if d.Action != "" || d.ID != "" || d.Target != "" || d.Title != "" || d.MemoryType != "" || d.Scope != "" || d.Sessions != "" || hasProposalMetaFields(d) {
		return Finding{}, errors.New("finding draft takes only body and quote")
	}
	detail := oneLine(d.Body)
	if strings.TrimSpace(d.Quote) != "" {
		detail += " (quote: " + oneLine(d.Quote) + ")"
	}
	return Finding{"draft", "finding", class, detail}, nil
}

func (r *run) applyMemoryDraft(root string, d draftLine) (Finding, error) {
	if d.Target != "" || d.Title != "" || d.Quote != "" || hasProposalMetaFields(d) {
		return Finding{}, errors.New("memory draft takes only action, id, body, memory_type, scope and sessions")
	}
	if d.Scope != "" && d.Scope != "project" && d.Scope != "global" {
		return Finding{}, fmt.Errorf("memory scope must be project or global, not %q", d.Scope)
	}
	isGlobal := d.Scope == "global" || root == "" || root == search.GlobalWorkspace
	if isGlobal && (d.Action == "retire" || d.Action == "merge") {
		return Finding{}, fmt.Errorf("global memory cannot be %sd via draft", d.Action)
	}
	var sessions []string
	if strings.TrimSpace(d.Sessions) != "" {
		for _, s := range strings.Split(d.Sessions, ",") {
			if s = strings.TrimSpace(s); s != "" {
				sessions = append(sessions, s)
			}
		}
	}
	bo := brain.Options{Home: r.o.Home, Dir: root, Global: isGlobal, Now: r.o.Now}
	opts := brain.MemoryDraftOptions{Type: d.MemoryType, EvidenceCount: 1, SourceSessions: sessions}
	_, err := brain.MemoryDraftWithOptions(bo, d.Action, d.ID, d.Body, opts)
	return Finding{}, err
}

func (r *run) applyFeedbackDraft(root, class string, d draftLine) (Finding, error) {
	if d.Action != "" || d.ID != "" || d.Target != "" || d.Title != "" || d.MemoryType != "" || d.Scope != "" || d.Sessions != "" || hasProposalMetaFields(d) {
		return Finding{}, errors.New("feedback draft takes only body and quote")
	}
	first, _, _ := strings.Cut(strings.TrimSpace(d.Body), "\n")
	title := oneLine(strings.TrimSpace(first))
	for len(title) > 120 {
		_, size := utf8.DecodeLastRuneInString(title)
		title = title[:len(title)-size]
	}
	if title == "" {
		title = "derived feedback (" + class + ")"
	}
	body := strings.TrimSpace(d.Body)
	if strings.TrimSpace(d.Quote) != "" {
		body += "\n\nQuote: " + strings.TrimSpace(d.Quote)
	}
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	_, err := brain.Add(g, brain.AddInput{
		Kind: "feedback", Title: title, Project: root, Provenance: "derived", Body: strings.NewReader(body),
	})
	return Finding{}, err
}

func (r *run) applyProposalDraft(root, class string, d draftLine) (Finding, error) {
	if d.Action != "" || d.ID != "" || d.Quote != "" || d.MemoryType != "" || d.Scope != "" || d.Sessions != "" {
		return Finding{}, errors.New("proposal draft takes only target, title, body, signal_id, report_date, action_scope and recommended_action")
	}
	if d.Target != "project" && d.Target != "harness" {
		return Finding{}, fmt.Errorf("proposal target must be project or harness, not %q", d.Target)
	}
	if err := validateProposalDraftMeta(d); err != nil {
		return Finding{}, err
	}
	title := oneLine(strings.TrimSpace(d.Title))
	if title == "" {
		return Finding{}, errors.New("proposal title is empty")
	}
	prefix := "Auto, " + d.Target + " — "
	if !strings.HasPrefix(title, prefix) {
		title = prefix + title
	}
	meta := formatProposalDraftMeta(d)
	body := fmt.Sprintf("Target: %s\nClass: %s\nProvenance: derived\n%s\n%s", d.Target, class, meta, indentProposalBody(d.Body))
	_, created, err := r.upsertProposal(proposalSpec{
		Project: root, Title: title, Body: body, UpdateBody: d.Body, ReportDate: d.ReportDate, Derived: true,
	})
	if err != nil {
		return Finding{}, err
	}
	if created {
		r.st.OpenAsks++
	}
	return Finding{}, nil
}

func indentProposalBody(s string) string {
	s = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(strings.TrimSpace(s))
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}
