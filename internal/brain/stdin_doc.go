package brain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

// RecordStdinDoc holds metadata and body parsed from 1-call stdin headers or --json-in.
type RecordStdinDoc struct {
	Title   string `json:"title"`
	Status  string `json:"status"`
	ID      string `json:"id"`
	Project string `json:"project"`
	Body    string `json:"body"`
}

// RecordEditJSONDoc holds optional fields for `fyr record edit <id> --json-in`.
type RecordEditJSONDoc struct {
	Title  *string `json:"title"`
	Status *string `json:"status"`
	Body   *string `json:"body"`
}

// MemoryStdinDoc holds metadata and body for `fyr memory add --json-in`.
type MemoryStdinDoc struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Provenance  string `json:"provenance"`
	Body        string `json:"body"`
}

// RecordJSON is the JSON representation of a Record for `record list --json` and `record get --json`.
type RecordJSON struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Date       string `json:"date"`
	Title      string `json:"title"`
	Body       string `json:"body,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Project    string `json:"project,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Updated    string `json:"updated"`
}

// MemorySummary is one item in `fyr memory show --json`.
type MemorySummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Provenance  string `json:"provenance"`
	Updated     string `json:"updated"`
}

// SearchHitJSON is one record hit in `fyr search --json`.
type SearchHitJSON struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Date       string `json:"date"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Project    string `json:"project,omitempty"`
	Provenance string `json:"provenance,omitempty"`
	Updated    string `json:"updated"`
}

var (
	recordHeaderKeys = map[string]bool{"title": true, "status": true, "id": true, "project": true}
	memoryHeaderKeys = map[string]bool{"type": true, "name": true, "description": true, "provenance": true}
)

// ParseRecordStdin reads optional leading "Title: ...", "Status: ...", "ID: ...",
// "Project: ..." header lines (optionally fenced by "---") before the first blank
// line and returns the parsed metadata plus an io.Reader for the body.
func ParseRecordStdin(r io.Reader) (RecordStdinDoc, io.Reader, error) {
	if r == nil {
		return RecordStdinDoc{}, strings.NewReader(""), nil
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxBody+4096))
	if err != nil {
		return RecordStdinDoc{}, nil, err
	}
	kv, bodyBytes, ok := splitLeadingHeaders(raw, recordHeaderKeys)
	if !ok {
		return RecordStdinDoc{}, io.MultiReader(bytes.NewReader(raw), r), nil
	}
	doc := RecordStdinDoc{
		Title:   kv["title"],
		Status:  kv["status"],
		ID:      kv["id"],
		Project: kv["project"],
		Body:    string(bodyBytes),
	}
	return doc, io.MultiReader(bytes.NewReader(bodyBytes), r), nil
}

// ParseMemoryStdin reads optional leading "Type: ...", "Name: ...",
// "Description: ...", "Provenance: ..." header lines before the first blank line.
func ParseMemoryStdin(r io.Reader) (MemoryEntry, io.Reader, error) {
	if r == nil {
		return MemoryEntry{}, strings.NewReader(""), nil
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxBody+4096))
	if err != nil {
		return MemoryEntry{}, nil, err
	}
	kv, bodyBytes, ok := splitLeadingHeaders(raw, memoryHeaderKeys)
	if !ok {
		return MemoryEntry{}, io.MultiReader(bytes.NewReader(raw), r), nil
	}
	e := MemoryEntry{
		Type:        kv["type"],
		Name:        kv["name"],
		Description: kv["description"],
		Provenance:  kv["provenance"],
	}
	return e, io.MultiReader(bytes.NewReader(bodyBytes), r), nil
}

func splitLeadingHeaders(raw []byte, allowed map[string]bool) (map[string]string, []byte, bool) {
	text := string(raw)
	rest := text
	fenced := false
	if after, ok := trimLinePrefix(rest, "---"); ok {
		fenced = true
		rest = after
	}
	kv := map[string]string{}
	for len(rest) > 0 {
		line, after, hasNL := cutLine(rest)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if fenced || len(kv) == 0 {
				return nil, nil, false
			}
			return kv, []byte(after), true
		}
		if trimmed == "---" && len(kv) > 0 {
			if afterBlank, ok := trimBlankLine(after); ok {
				after = afterBlank
			}
			return kv, []byte(after), true
		}
		k, v, ok := parseHeaderKV(line, allowed)
		if !ok {
			return nil, nil, false
		}
		kv[k] = v
		rest = after
		if !hasNL {
			break
		}
	}
	if !fenced && len(kv) > 0 {
		return kv, nil, true
	}
	return nil, nil, false
}

func trimLinePrefix(s, prefix string) (string, bool) {
	line, after, ok := cutLine(s)
	if !ok || strings.TrimSpace(line) != prefix {
		return s, false
	}
	return after, true
}

func trimBlankLine(s string) (string, bool) {
	line, after, ok := cutLine(s)
	if ok && strings.TrimSpace(line) == "" {
		return after, true
	}
	return s, false
}

func cutLine(s string) (line, after string, hasNL bool) {
	idx := strings.IndexByte(s, '\n')
	if idx < 0 {
		return strings.TrimSuffix(s, "\r"), "", false
	}
	return strings.TrimSuffix(s[:idx], "\r"), s[idx+1:], true
}

func parseHeaderKV(line string, allowed map[string]bool) (string, string, bool) {
	k, v, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(k))
	if !allowed[key] {
		return "", "", false
	}
	return key, strings.TrimSpace(v), true
}

// ParseRecordJSON decodes a single RecordStdinDoc JSON object from r with unknown fields refused.
func ParseRecordJSON(r io.Reader) (RecordStdinDoc, error) {
	var doc RecordStdinDoc
	if err := decodeStrictJSON(r, &doc); err != nil {
		return RecordStdinDoc{}, err
	}
	return doc, nil
}

// ParseRecordEditJSON decodes a single RecordEditJSONDoc JSON object from r with unknown fields refused.
func ParseRecordEditJSON(r io.Reader) (RecordEditJSONDoc, error) {
	var doc RecordEditJSONDoc
	if err := decodeStrictJSON(r, &doc); err != nil {
		return RecordEditJSONDoc{}, err
	}
	return doc, nil
}

// ParseMemoryJSON decodes a single MemoryStdinDoc JSON object from r with unknown fields refused.
func ParseMemoryJSON(r io.Reader) (MemoryEntry, io.Reader, error) {
	var doc MemoryStdinDoc
	if err := decodeStrictJSON(r, &doc); err != nil {
		return MemoryEntry{}, nil, err
	}
	e := MemoryEntry{
		Type:        doc.Type,
		Name:        doc.Name,
		Description: doc.Description,
		Provenance:  doc.Provenance,
	}
	return e, strings.NewReader(doc.Body), nil
}

func decodeStrictJSON(r io.Reader, dst any) error {
	if r == nil {
		return usage("invalid --json-in payload: empty input")
	}
	dec := json.NewDecoder(io.LimitReader(r, maxLine))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return usage("invalid --json-in payload: %v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return usage("invalid --json-in payload: more than one JSON value on stdin")
	}
	return nil
}

// FormatRecordsJSON marshals records as a JSON array with printable text fields.
func FormatRecordsJSON(recs []Record, includeBody bool) (string, error) {
	items := make([]RecordJSON, 0, len(recs))
	for _, r := range recs {
		item := RecordJSON{
			ID:         search.Printable(r.ID),
			Kind:       search.Printable(r.Kind),
			Status:     search.Printable(r.Status),
			Date:       search.Printable(r.Date),
			Title:      search.Printable(r.Title),
			Scope:      search.Printable(r.Scope),
			Project:    search.Printable(r.Project),
			Provenance: search.Printable(r.Provenance),
			Updated:    search.Printable(r.Updated),
		}
		if includeBody {
			item.Body = r.Body
		}
		items = append(items, item)
	}
	return marshalIndentedJSON(items)
}

// MemoryShowJSON returns the project's active memories as a JSON array.
func MemoryShowJSON(o Options) (string, error) {
	s, err := memorySession(o)
	if err != nil {
		return "", err
	}
	defer s.close()
	rows, err := s.st.db.Query(`SELECT id, title, body, provenance, updated FROM records WHERE scope = ? AND kind = 'memory' AND status = 'closed' ORDER BY id`, s.scope)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	items := []MemorySummary{}
	for rows.Next() {
		var id, title, body, prov, updated string
		if err := rows.Scan(&id, &title, &body, &prov, &updated); err != nil {
			return "", err
		}
		if prov == "" {
			prov = "unknown"
		}
		typ := "?"
		if m := memoryType.FindStringSubmatch(body); m != nil {
			typ = m[1]
		}
		items = append(items, MemorySummary{
			ID:          search.Printable(id),
			Name:        search.Printable(strings.TrimPrefix(id, "MEMORY-")),
			Type:        search.Printable(typ),
			Description: search.Printable(title),
			Provenance:  search.Printable(prov),
			Updated:     search.Printable(updated),
		})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return marshalIndentedJSON(items)
}

// FormatSearchJSON marshals brain search hits and file/graph search output as JSON.
func FormatSearchJSON(hits []Hit, fileOutput string, recordsError ...string) (string, error) {
	recs := make([]SearchHitJSON, 0, len(hits))
	for _, h := range hits {
		recs = append(recs, SearchHitJSON{
			ID:         search.Printable(h.ID),
			Label:      search.Printable(h.Label),
			Kind:       search.Printable(h.Kind),
			Status:     search.Printable(h.Status),
			Date:       search.Printable(h.Date),
			Title:      search.Printable(h.Title),
			Snippet:    search.Printable(h.Snippet),
			Scope:      search.Printable(h.Scope),
			Project:    search.Printable(h.Project),
			Provenance: search.Printable(h.Provenance),
			Updated:    search.Printable(h.Updated),
		})
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(fileOutput), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	var recErr string
	if len(recordsError) > 0 {
		recErr = recordsError[0]
	}
	payload := struct {
		Records      []SearchHitJSON `json:"records"`
		RecordsError string          `json:"recordsError,omitempty"`
		Files        []string        `json:"files,omitempty"`
		Output       string          `json:"output,omitempty"`
	}{
		Records:      recs,
		RecordsError: recErr,
		Files:        files,
		Output:       fileOutput,
	}
	return marshalIndentedJSON(payload)
}

func marshalIndentedJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return buf.String(), nil
}
