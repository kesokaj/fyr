package work

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// The contract's bounds: lines per worker (D4), notes kept of them, and
// bytes per line, so one long line cannot carry a transcript.
const (
	maxLines    = 39 // under 40 (D4), so two workers and the stamp line fit 80 (D85)
	maxNotes    = 10
	maxLineSize = 1000
)

// The handoff sections the contract prints, in print order, each line
// prefixed "work: <name> | " (fit has the cut order).
var sectionHeads = []string{"## Open / In Progress", "## Next Steps", "## Kickoff"}

// Block is the result contract: the result line, the sandbox's notes, and
// for a fetched worker its handoff's three sections, its change records and
// its drafts, at most maxLines lines, every one printable and clipped to
// maxLineSize bytes; fit says what goes over maxLines.
func (r Result) Block() []string {
	p := "work: " + r.Name
	head := []string{r.line()}
	notes := r.Notes
	cut := 0
	if len(notes) > maxNotes {
		cut += len(notes) - maxNotes
		notes = notes[:maxNotes]
	}
	head = append(head, notes...)
	secs := make([][]string, len(sectionHeads))
	var changes, tail []string
	if r.Head != "" {
		if r.Handoff != nil {
			head = append(head, fmt.Sprintf("%s: handoff %s", p, r.Handoff.ID))
			secs = sections(r.Handoff.Body)
		}
		for _, c := range r.Changes {
			changes = append(changes, fmt.Sprintf("%s: change %s  %s", p, c.ID, c.Title))
		}
		if len(changes) == 0 {
			changes = []string{p + ": no change record"}
		}
		if r.Drafts > 0 {
			tail = append(tail, fmt.Sprintf("%s: drafts %s (%d files)", p, r.DraftsDir, r.Drafts))
		}
	}
	cut += fit(len(head)+len(tail), cut > 0, secs, &changes)
	out := head
	for _, s := range secs {
		// Prefixed, so no handoff line can pass for a contract line
		// (security review of Phase 2, finding 3).
		for _, l := range s {
			out = append(out, p+" | "+l)
		}
	}
	out = append(append(out, changes...), tail...)
	if cut > 0 {
		out = append(out, fmt.Sprintf("%s: cut %d lines; read the handoff after the merge", p, cut))
	}
	for i, l := range out {
		out[i] = clip(sandbox.Printable(l))
	}
	return out
}

// fit cuts, each from its end, the Kickoff section, then Open / In
// Progress, then the change lines, then Next Steps (D39: it goes last, it
// holds a gate's question), until the fixed lines, the cut line and what is
// left fit maxLines; it returns how many lines it cut. reserved says the
// cut line is printed anyway.
func fit(fixed int, reserved bool, secs [][]string, changes *[]string) int {
	total := fixed + len(*changes)
	for _, s := range secs {
		total += len(s)
	}
	if reserved {
		total++
	}
	if total <= maxLines {
		return 0
	}
	over := total - maxLines
	if !reserved {
		over++ // the cut line this adds
	}
	parts := []*[]string{&secs[2], &secs[0], changes, &secs[1]}
	cut := 0
	for i, part := range parts {
		n := min(over-cut, len(*part))
		if i != 2 && n > 0 && len(*part)-n == 1 { // a heading alone is cut too
			n++
		}
		*part = (*part)[:len(*part)-n]
		if cut += n; cut >= over {
			break
		}
	}
	return cut
}

// sections is the handoff body's three sections in print order, each its
// heading and its lines up to the next heading outside a fence, with the
// trailing blank lines dropped; a missing section is empty.
func sections(body string) [][]string {
	found := map[string][]string{}
	var cur string
	fence := false
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fence = !fence
		}
		if !fence && (strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "# ")) {
			cur = ""
			for _, h := range sectionHeads {
				if strings.TrimSpace(l) == h {
					cur = h
				}
			}
		}
		if cur != "" {
			found[cur] = append(found[cur], l)
		}
	}
	out := make([][]string, len(sectionHeads))
	for i, h := range sectionHeads {
		s := found[h]
		for len(s) > 0 && strings.TrimSpace(s[len(s)-1]) == "" {
			s = s[:len(s)-1]
		}
		out[i] = s
	}
	return out
}

// line is the result line: the verdict, agy's exit, the time, the branch,
// and the sandbox when it is kept.
func (r Result) line() string {
	v := r.Verdict
	if len(r.Reasons) > 0 {
		v += " (" + strings.Join(r.Reasons, "; ") + ")"
	}
	branch := "work/" + r.Name + " not fetched"
	if r.Head != "" {
		branch = fmt.Sprintf("work/%s %s..%s (%d commits)", r.Name, short(r.Base), short(r.Head), r.Commits)
	}
	l := fmt.Sprintf("work: %s  %s  agy exit %d  %ds  %s", r.Name, v, r.AgyExit, r.Seconds, branch)
	if r.Dir != "" {
		l += "  kept " + r.Dir
	}
	return l
}

// clip is l cut to maxLineSize bytes on a rune boundary, marked.
func clip(l string) string {
	if len(l) <= maxLineSize {
		return l
	}
	n := maxLineSize
	for n > 0 && !utf8.RuneStart(l[n]) {
		n--
	}
	return l[:n] + " [cut]"
}
