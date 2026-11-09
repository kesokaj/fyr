package brain

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestParseRecordStdinHeadersAndFallback(t *testing.T) {
	t.Run("unfenced headers", func(t *testing.T) {
		in := "Title: Fix race in store\nStatus: fixed\nID: BUG-042\nProject: /tmp/proj\n\nSymptom: x.\nRule: y.\n"
		doc, body, err := ParseRecordStdin(strings.NewReader(in))
		if err != nil {
			t.Fatalf("ParseRecordStdin: %v", err)
		}
		b, _ := io.ReadAll(body)
		if doc.Title != "Fix race in store" || doc.Status != "fixed" || doc.ID != "BUG-042" || doc.Project != "/tmp/proj" {
			t.Errorf("doc = %+v", doc)
		}
		if string(b) != "Symptom: x.\nRule: y.\n" {
			t.Errorf("body = %q", string(b))
		}
	})

	t.Run("fenced headers", func(t *testing.T) {
		in := "---\nTitle: Fenced change\nStatus: accepted\n---\n\n# Context\nDecision body.\n"
		doc, body, err := ParseRecordStdin(strings.NewReader(in))
		if err != nil {
			t.Fatalf("ParseRecordStdin: %v", err)
		}
		b, _ := io.ReadAll(body)
		if doc.Title != "Fenced change" || doc.Status != "accepted" {
			t.Errorf("doc = %+v", doc)
		}
		if string(b) != "# Context\nDecision body.\n" {
			t.Errorf("body = %q", string(b))
		}
	})

	t.Run("plain body fallback when first line is not a header", func(t *testing.T) {
		in := "Symptom: broken\nTitle: this is inside the body\n"
		doc, body, err := ParseRecordStdin(strings.NewReader(in))
		if err != nil {
			t.Fatalf("ParseRecordStdin: %v", err)
		}
		b, _ := io.ReadAll(body)
		if doc != (RecordStdinDoc{}) {
			t.Errorf("expected empty doc on fallback, got %+v", doc)
		}
		if string(b) != in {
			t.Errorf("body = %q, want %q", string(b), in)
		}
	})

	t.Run("unclosed fence falls back to plain body", func(t *testing.T) {
		in := "---\nTitle: No closing fence\n\nBody text\n"
		doc, body, err := ParseRecordStdin(strings.NewReader(in))
		if err != nil {
			t.Fatalf("ParseRecordStdin: %v", err)
		}
		b, _ := io.ReadAll(body)
		if doc != (RecordStdinDoc{}) || string(b) != in {
			t.Errorf("expected fallback on unclosed fence, got doc=%+v body=%q", doc, string(b))
		}
	})

	t.Run("oversized body after large headers is not truncated", func(t *testing.T) {
		longProj := strings.Repeat("p", 5000)
		oversizedBody := strings.Repeat("x", maxBody+10) + "\n"
		in := "Title: Large header test\nProject: /" + longProj + "\n\n" + oversizedBody
		_, body, err := ParseRecordStdin(strings.NewReader(in))
		if err != nil {
			t.Fatalf("ParseRecordStdin: %v", err)
		}
		if _, err := readBody(body); err == nil {
			t.Fatal("expected readBody to reject body > 1 MiB after > 4 KiB headers, got nil")
		}
	})
}

func TestParseMemoryStdin(t *testing.T) {
	in := "Type: feedback\nName: table-tests\nDescription: Use table tests\nProvenance: derived\n\nAlways use table-driven tests.\n"
	e, body, err := ParseMemoryStdin(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseMemoryStdin: %v", err)
	}
	b, _ := io.ReadAll(body)
	if e.Type != "feedback" || e.Name != "table-tests" || e.Description != "Use table tests" || e.Provenance != "derived" {
		t.Errorf("entry = %+v", e)
	}
	if string(b) != "Always use table-driven tests.\n" {
		t.Errorf("body = %q", string(b))
	}
}

func TestParseStrictJSONIn(t *testing.T) {
	doc, err := ParseRecordJSON(strings.NewReader(`{"title":"JSON title","status":"open","id":"BUG-009","body":"Symptom: z\n"}`))
	if err != nil {
		t.Fatalf("ParseRecordJSON: %v", err)
	}
	if doc.Title != "JSON title" || doc.Status != "open" || doc.ID != "BUG-009" || doc.Body != "Symptom: z\n" {
		t.Errorf("doc = %+v", doc)
	}

	// Must-fail: unknown field rejected as usage error.
	if _, err := ParseRecordJSON(strings.NewReader(`{"title":"t","unknownField":123}`)); !IsUsage(err) {
		t.Errorf("unknown field: got %v, want usage error", err)
	}
	// Must-fail: trailing JSON value rejected as usage error.
	if _, err := ParseRecordJSON(strings.NewReader(`{"title":"t"}{"title":"t2"}`)); !IsUsage(err) {
		t.Errorf("trailing JSON: got %v, want usage error", err)
	}

	editDoc, err := ParseRecordEditJSON(strings.NewReader(`{"status":"fixed","body":"Updated body"}`))
	if err != nil {
		t.Fatalf("ParseRecordEditJSON: %v", err)
	}
	if editDoc.Status == nil || *editDoc.Status != "fixed" || editDoc.Title != nil || editDoc.Body == nil || *editDoc.Body != "Updated body" {
		t.Errorf("editDoc = %+v", editDoc)
	}
	if _, err := ParseRecordEditJSON(strings.NewReader(`{"id":"BUG-001"}`)); !IsUsage(err) {
		t.Errorf("unknown edit field: got %v, want usage error", err)
	}

	memEntry, memBody, err := ParseMemoryJSON(strings.NewReader(`{"type":"project","name":"rel-cadence","description":"weekly","provenance":"operator","body":"Ship on Mondays.\n"}`))
	if err != nil {
		t.Fatalf("ParseMemoryJSON: %v", err)
	}
	mb, _ := io.ReadAll(memBody)
	if memEntry.Type != "project" || memEntry.Name != "rel-cadence" || memEntry.Description != "weekly" || memEntry.Provenance != "operator" || string(mb) != "Ship on Mondays.\n" {
		t.Errorf("memEntry = %+v body = %q", memEntry, string(mb))
	}
	if _, _, err := ParseMemoryJSON(strings.NewReader(`{"type":"project","bogus":true}`)); !IsUsage(err) {
		t.Errorf("unknown memory field: got %v, want usage error", err)
	}
}

func TestFormatRecordsAndMemoryJSON(t *testing.T) {
	js, err := FormatRecordsJSON(nil, false)
	if err != nil || strings.TrimSpace(js) != "[]" {
		t.Fatalf("empty FormatRecordsJSON = %q, %v", js, err)
	}
	js, err = FormatRecordsJSON([]Record{{ID: "BUG-001", Kind: "bug", Status: "open", Date: "2026-11-09", Title: "t", Body: "b", Updated: "2026-11-09T00:00:00Z"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	var recs []RecordJSON
	if err := json.Unmarshal([]byte(js), &recs); err != nil || len(recs) != 1 || recs[0].ID != "BUG-001" || recs[0].Body != "b" {
		t.Fatalf("unmarshal FormatRecordsJSON: %v %+v", err, recs)
	}

	sjs, err := FormatSearchJSON(nil, "a.go:1: match\n", "corrupt trail")
	if err != nil || !strings.Contains(sjs, `"recordsError": "corrupt trail"`) {
		t.Fatalf("FormatSearchJSON recordsError: %q %v", sjs, err)
	}
}
