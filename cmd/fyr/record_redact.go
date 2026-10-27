package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/redact"
)

// teeBody is stdin as the brain store reads it, with what it read kept in
// seen, so the warning judges exactly the body that was offered.
func teeBody(seen *strings.Builder) io.Reader { return io.TeeReader(stdin, seen) }

// warnRedactable names on stderr, after the store, a title or body that holds
// text the redactor would change: a project's records are exported to the
// committed trail.jsonl, and the operator chose a warning over a refusal (SD42
// of the 2026-10-27 audit). The record stays stored and the exit status is
// kept. The text itself is never printed: for the body, only its first line,
// as read from stdin, where the redacted body differs. A field that was not
// set is passed as "" and never warns.
func warnRedactable(title, body string) {
	if redact.Redact(title) != title {
		fmt.Fprintln(stderr, "fyr record: warning: the title holds text the redactor would change; stored as written")
	}
	red := redact.Redact(body)
	if red == body {
		return
	}
	a, b := strings.Split(body, "\n"), strings.Split(red, "\n")
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	fmt.Fprintf(stderr, "fyr record: warning: the body holds text the redactor would change (first at line %d); stored as written\n", n+1)
}
