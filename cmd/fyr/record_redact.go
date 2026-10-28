package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
)

// teeBody is stdin as the brain store reads it, with what it read kept in
// seen, so the warning judges exactly the body that was offered.
func teeBody(seen *strings.Builder) io.Reader { return io.TeeReader(stdin, seen) }

// warnRedactable names on stderr, after the store, an ID given with --id, a
// title or a body that holds text the redactor would change: a project's
// records are exported to the committed trail.jsonl, and the operator chose a
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
	warn := func(name, what, where string) {
		fmt.Fprintf(stderr, "fyr record import: warning: %s: %s holds text the redactor would change%s%s\n", name, what, where, tail)
	}
	for i, r := range res.Records {
		title, body := r.Text()
		idHot := redact.Redact(r.ID) != r.ID || redact.Redact(r.Path) != r.Path
		titleHot := redact.Redact(title) != title
		name := r.ID
		if idHot || titleHot {
			name = fmt.Sprintf("record %d in the list above", i+1)
		}
		if idHot {
			warn(name, "the ID or the source path", "")
		}
		if titleHot {
			warn(name, "the title", "")
		}
		if n := firstRedacted(body); n > 0 {
			warn(name, "the body", fmt.Sprintf(" (first at line %d)", n))
		}
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
