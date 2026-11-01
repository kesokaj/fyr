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
)

const (
	draftMaxBytes = 4096
	maxDraftLines = 100
)

type draftLine struct {
	Kind   string
	Action string
	ID     string
	Target string
	Title  string
	Body   string
	Quote  string
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
	for _, field := range []string{d.Kind, d.Action, d.ID, d.Target, d.Title, d.Body, d.Quote} {
		if redact.Redact(field) != field {
			return draftLine{}, errors.New("draft field refused by redactor")
		}
	}
	if strings.ContainsAny(d.ID, "\r\n\x00") || strings.ContainsAny(d.Title, "\r\n\x00") {
		return draftLine{}, errors.New("draft id or title contains control characters")
	}
	if len(d.Body) >= draftMaxBytes {
		return draftLine{}, errors.New("draft body is 4 KiB or larger")
	}
	if strings.TrimSpace(d.Body) == "" {
		return draftLine{}, errors.New("draft body is empty")
	}
	return d, nil
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
		switch key {
		case "kind":
			d.Kind = *val
		case "action":
			d.Action = *val
		case "id":
			d.ID = *val
		case "target":
			d.Target = *val
		case "title":
			d.Title = *val
		case "body":
			d.Body = *val
		case "quote":
			d.Quote = *val
		default:
			return d, fmt.Errorf("unknown key %q", key)
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
	if d.Action != "" || d.ID != "" || d.Target != "" || d.Title != "" {
		return Finding{}, errors.New("finding draft takes only body and quote")
	}
	detail := oneLine(d.Body)
	if strings.TrimSpace(d.Quote) != "" {
		detail += " (quote: " + oneLine(d.Quote) + ")"
	}
	return Finding{"draft", "finding", class, detail}, nil
}

func (r *run) applyMemoryDraft(root string, d draftLine) (Finding, error) {
	if d.Target != "" || d.Title != "" || d.Quote != "" {
		return Finding{}, errors.New("memory draft takes only action, id and body")
	}
	bo := brain.Options{Home: r.o.Home, Dir: root, Now: r.o.Now}
	_, err := brain.MemoryDraft(bo, d.Action, d.ID, d.Body)
	return Finding{}, err
}

func (r *run) applyFeedbackDraft(root, class string, d draftLine) (Finding, error) {
	if d.Action != "" || d.ID != "" || d.Target != "" || d.Title != "" {
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
	if d.Action != "" || d.ID != "" || d.Quote != "" {
		return Finding{}, errors.New("proposal draft takes only target, title and body")
	}
	if d.Target != "project" && d.Target != "harness" {
		return Finding{}, fmt.Errorf("proposal target must be project or harness, not %q", d.Target)
	}
	title := oneLine(strings.TrimSpace(d.Title))
	if title == "" {
		return Finding{}, errors.New("proposal title is empty")
	}
	prefix := "Auto, " + d.Target + " — "
	if !strings.HasPrefix(title, "Auto, ") {
		title = prefix + title
	}
	body := fmt.Sprintf("Target: %s\nClass: %s\nProvenance: derived\n\n%s", d.Target, class, strings.TrimSpace(d.Body))
	g := brain.Options{Home: r.o.Home, Global: true, Now: r.o.Now}
	if _, err := brain.Add(g, brain.AddInput{
		Kind: "proposal", Title: title, Project: root, Body: strings.NewReader(body),
	}); err != nil {
		return Finding{}, err
	}
	r.st.OpenAsks++
	return Finding{}, nil
}
