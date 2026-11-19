package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
)

// teeBodyFrom wraps r so what the brain store reads is kept in seen for the
// redactor warning.
func teeBodyFrom(r io.Reader, seen *strings.Builder) io.Reader { return io.TeeReader(r, seen) }

// openBodyReader returns the reader for a record or memory body: stdin when
// --body-file was not set, or the regular file at path when it was, refusing
// piped stdin alongside --body-file.
func openBodyReader(path string, set bool) (io.Reader, func(), error) {
	if !set {
		return stdin, func() {}, nil
	}
	if brain.StdinPiped(stdin) {
		return nil, nil, brain.RefusePipedBodyFile()
	}
	f, err := brain.OpenBodyFile(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

// warnRedactable names on stderr, after the store, an ID given with --id, a
// title or a body that holds text the redactor would change: a project's
// records reach .agents/trail.jsonl when exported, and the operator chose a
// warning over a refusal (SD42 of the 2026-10-27 audit; the ID, ledger D31
// 3b). The record stays stored and the exit status is kept. The text itself
// is never printed, the ID's neither (stdout still prints it, as add's
// output): for the body, only its first line, as read from stdin, where the
// redacted body differs. A field that was not set is passed as "" and never
// warns.
func warnRedactable(id, title, body string) {
	if redact.Redact(id) != id {
		fmt.Fprintln(stderr, "fyr record: warning: the ID holds text the redactor would change; stored as written")
	}
	if redact.Redact(title) != title {
		fmt.Fprintln(stderr, "fyr record: warning: the title holds text the redactor would change; stored as written")
	}
	if n := firstRedacted(body); n > 0 {
		fmt.Fprintf(stderr, "fyr record: warning: the body holds text the redactor would change (first at line %d); stored as written\n", n)
	}
}

type redactWarnTarget struct {
	sub          string
	id           string
	extraID      string
	fallbackName string
	idLabel      string
	title        string
	body         string
	tail         string
}

// warnRecordRedactable emits per-record ID/title/body redaction warnings to
// stderr without printing any secret text.
func warnRecordRedactable(t redactWarnTarget) {
	idHot := redact.Redact(t.id) != t.id || (t.extraID != "" && redact.Redact(t.extraID) != t.extraID)
	titleHot := redact.Redact(t.title) != t.title
	name := t.id
	if idHot || titleHot {
		name = t.fallbackName
	}
	warn := func(what, where string) {
		fmt.Fprintf(stderr, "fyr record %s: warning: %s: %s holds text the redactor would change%s%s\n", t.sub, name, what, where, t.tail)
	}
	if idHot {
		warn(t.idLabel, "")
	}
	if titleHot {
		warn("the title", "")
	}
	if n := firstRedacted(t.body); n > 0 {
		warn("the body", fmt.Sprintf(" (first at line %d)", n))
	}
}

// warnImportRedactable is warnRedactable for `record import`: one line per
// record it stored (or, in a dry run, would store) whose ID, source path,
// title or body holds text the redactor would change, never printing that
// text (ledger item 47). An ID is made from the file name or, for a global
// log line, the title, and slugging can hide a secret from the redactor, so
// a record whose ID, path or title trips it is named by its place in the
// list printImport printed, never by its ID (the phase's security review,
// rounds 1 and 2); any other is named by its ID. The exit status is kept.
func warnImportRedactable(res *brain.ImportResult, dryRun bool) {
	tail := "; stored as written"
	if dryRun {
		tail = ""
	}
	for i, r := range res.Records {
		title, body := r.Text()
		warnRecordRedactable(redactWarnTarget{
			sub:          "import",
			id:           r.ID,
			extraID:      r.Path,
			fallbackName: fmt.Sprintf("record %d in the list above", i+1),
			idLabel:      "the ID or the source path",
			title:        title,
			body:         body,
			tail:         tail,
		})
	}
}

// warnSyncRedactable warns on stderr when a record loaded by `record sync`
// holds text the redactor would change, without printing the text itself.
func warnSyncRedactable(recs []brain.Record) {
	for i, r := range recs {
		warnRecordRedactable(redactWarnTarget{
			sub:          "sync",
			id:           r.ID,
			fallbackName: fmt.Sprintf("record %d in .agents/trail.jsonl", i+1),
			idLabel:      "the ID",
			title:        r.Title,
			body:         r.Body,
			tail:         "; stored as written",
		})
	}
}

// firstRedacted is the 1-based line of the first change the redactor makes
// to body, or 0 when it makes none.
func firstRedacted(body string) int {
	red := redact.Redact(body)
	if red == body {
		return 0
	}
	a, b := strings.Split(body, "\n"), strings.Split(red, "\n")
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n + 1
}
