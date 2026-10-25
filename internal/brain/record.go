package brain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/search"
)

// Record is one row of the store.
type Record struct {
	Scope   string // the main worktree's absolute root, or "" for global
	ID      string
	Kind    string
	Status  string
	Date    string // YYYY-MM-DD, the day it was added
	Title   string
	Body    string
	Project string // a global record's project (feedback, proposal)
	Updated string // RFC 3339, set by every write
}

// prefixes is every kind and the prefix of its IDs (D7, D11). testing is
// TEST, the name the plan's Known-limits records carry.
var prefixes = map[string]string{
	"plan": "PLAN", "review": "REVIEW", "testing": "TEST", "change": "CHANGE",
	"handoff": "HANDOFF", "bug": "BUG", "adr": "ADR", "retro": "RETRO",
	"learning": "LEARNING", "spec": "SPEC", "deploy": "DEPLOY", "guide": "GUIDE",
	"memory": "MEMORY", "feedback": "FEEDBACK", "proposal": "PROPOSAL",
}

// globalOnly kinds are refused in a project (D10, R8).
var globalOnly = map[string]bool{"feedback": true, "proposal": true}

// unexported kinds never reach trail.jsonl (D14, R4).
var unexported = map[string]bool{"memory": true, "feedback": true, "proposal": true}

// unexportedSQL is the same set as SQL, for the export and the load: built
// from the map, so the two cannot drift (security review 2026-10-25,
// finding 7).
var unexportedSQL = func() string {
	var ks []string
	for k := range unexported {
		ks = append(ks, "'"+k+"'")
	}
	sort.Strings(ks)
	return "(" + strings.Join(ks, ", ") + ")"
}()

// maxBody is the largest body a record takes.
const maxBody = 1 << 20

// maxTitle is the longest title, in bytes: one line a list or a hit prints.
const maxTitle = 512

// Kinds is every kind, sorted, for usage messages.
func Kinds() []string {
	out := make([]string, 0, len(prefixes))
	for k := range prefixes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func knownKind(k string) bool { _, ok := prefixes[k]; return ok }

var (
	// At most nine digits: a number fits an int, and a thousand-fold
	// growth of the trail is still covered.
	bugID      = regexp.MustCompile(`^BUG-(\d{3,9})$`)
	adrID      = regexp.MustCompile(`^ADR-(\d{3,9})([a-z]?)$`)
	slugTail   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	superseded = regexp.MustCompile(`^superseded:ADR-\d{3,9}[a-z]?$`)
	applied    = regexp.MustCompile(`^applied:[0-9a-f]{7,40}$`)
	dateRe     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	nonSlug    = regexp.MustCompile(`[^a-z0-9]+`)
)

// validID reports whether id is one kind may carry: BUG-NNN, ADR-NNN[a-z],
// or the kind's prefix and a lowercase slug (the generated form is
// <KIND>-<YYYY-MM-DD-HHMM>-<slug>; an import may give a shorter one, such as
// SPEC-fyr-record).
func validID(kind, id string) bool {
	switch kind {
	case "bug":
		return bugID.MatchString(id)
	case "adr":
		return adrID.MatchString(id)
	}
	tail, ok := strings.CutPrefix(id, prefixes[kind]+"-")
	return ok && slugTail.MatchString(tail)
}

func idGrammar(kind string) string {
	switch kind {
	case "bug":
		return "BUG-NNN"
	case "adr":
		return "ADR-NNN or ADR-NNNa"
	}
	return prefixes[kind] + "-<lowercase slug>"
}

// allowed reports whether status is one kind takes (D6). A plan's is derived
// and a handoff has none; both are listed for the load, which reads them back.
func allowed(kind, status string) bool {
	switch kind {
	case "plan":
		return status == "open" || status == "closed"
	case "handoff":
		return status == ""
	case "bug":
		switch status {
		case "open", "fixed", "accepted", "obsolete", "graduated":
			return true
		}
		return false
	case "adr":
		return status == "accepted" || superseded.MatchString(status)
	case "spec", "deploy", "guide":
		return status == "current" || status == "retired"
	case "proposal":
		return status == "queued" || status == "rejected" || applied.MatchString(status)
	}
	return status == "closed"
}

func statusGrammar(kind string) string {
	switch kind {
	case "bug":
		return "open, fixed, accepted, obsolete or graduated"
	case "adr":
		return "accepted or superseded:ADR-NNN"
	case "spec", "deploy", "guide":
		return "current or retired"
	case "proposal":
		return "queued, rejected or applied:<commit>"
	}
	return "closed"
}

// resolveStatus is the status a write stores: the given one when the kind
// takes it, else the kind's starting status; a plan's comes from its body
// and a handoff has none, so --status is refused for both.
func resolveStatus(kind, given, body string) (string, error) {
	switch kind {
	case "plan":
		if given != "" {
			return "", usage("a plan's status comes from its body (open while it has an unticked \"- [ ]\" outside a fence): drop --status")
		}
		return planStatus(body), nil
	case "handoff":
		if given != "" {
			return "", usage("a handoff has no status (the newest is the live one): drop --status")
		}
		return "", nil
	}
	if given == "" {
		switch kind {
		case "bug":
			return "open", nil
		case "adr":
			return "accepted", nil
		case "spec", "deploy", "guide":
			return "current", nil
		case "proposal":
			return "queued", nil
		}
		return "closed", nil
	}
	if !allowed(kind, given) {
		return "", usage("--status %q: a %s takes %s", given, kind, statusGrammar(kind))
	}
	return given, nil
}

func planStatus(body string) string {
	if search.Unticked(body) > 0 {
		return "open"
	}
	return "closed"
}

// openSQL is the "Open when" column of the status table (D6).
const openSQL = `((kind = 'plan' AND status = 'open') OR (kind = 'bug' AND status = 'open')
	OR (kind IN ('spec', 'deploy', 'guide') AND status = 'current') OR (kind = 'proposal' AND status = 'queued'))`

// slugify makes the slug of a generated ID from a title.
func slugify(title string) string {
	s := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-")
	}
	if s == "" {
		return "record"
	}
	return s
}

// cleanTitle is a title a record takes: one line of text.
func cleanTitle(t string) (string, error) {
	t = strings.TrimSpace(t)
	switch {
	case t == "":
		return "", usage("--title is required")
	case strings.ContainsAny(t, "\r\n"):
		return "", usage("--title must be one line")
	case !utf8.ValidString(t):
		return "", usage("--title is not UTF-8 text")
	case !titleText(t):
		return "", usage("--title holds a control character")
	case len(t) > maxTitle:
		return "", usage("--title is over %d bytes", maxTitle)
	}
	return t, nil
}

// bodyText reports whether s is text a body may hold: UTF-8 with no control
// character but tab and newline. \x01 and \x02 above all, which a search
// snippet uses as its match marks (security review 2026-10-25, finding 5).
func bodyText(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f {
			return false
		}
	}
	return true
}

// titleText is bodyText for one line: no newline and no tab either.
func titleText(s string) bool { return bodyText(s) && !strings.ContainsAny(s, "\t\n") }

// idLess orders IDs by prefix, then by number when both carry one (BUG-999
// before BUG-1000, ADR-047 before ADR-047a), else as text.
func idLess(a, b string) bool {
	pa, ra, _ := strings.Cut(a, "-")
	pb, rb, _ := strings.Cut(b, "-")
	if pa != pb {
		return pa < pb
	}
	na, sa, oka := numberPart(ra)
	nb, sb, okb := numberPart(rb)
	if oka && okb {
		if len(na) != len(nb) {
			return len(na) < len(nb)
		}
		if na != nb {
			return na < nb
		}
		return sa < sb
	}
	return ra < rb
}

// numberPart splits "047a" into its digits without leading zeros and its
// letter suffix.
func numberPart(s string) (digits, suffix string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || len(s)-i > 1 || (i < len(s) && (s[i] < 'a' || s[i] > 'z')) {
		return "", "", false
	}
	d := strings.TrimLeft(s[:i], "0")
	return d, s[i:], true
}

// FormatLine is one list line, `ID  kind  status  date  title` (a missing
// status as "-"), with the scope appended for list --all; printable, since a
// title is the record's own text.
func FormatLine(r Record, scope bool) string {
	st := r.Status
	if st == "" {
		st = "-"
	}
	s := fmt.Sprintf("%s  %s  %s  %s  %s", r.ID, r.Kind, st, r.Date, r.Title)
	if scope {
		s += "  [" + scopeLabel(r.Scope) + "]"
	}
	return search.Printable(s)
}

func scopeLabel(scope string) string {
	if scope == "" {
		return "global"
	}
	return scope
}

// FormatGet is what `record get` prints for one record: its list line, the
// project a global record is about, then the body, every line through
// printable with its tabs kept (a tab moves the cursor and nothing else).
func FormatGet(r Record) string {
	var b strings.Builder
	b.WriteString(FormatLine(r, false))
	if r.Project != "" {
		b.WriteString(search.Printable("  (project " + r.Project + ")"))
	}
	b.WriteString("\n")
	for _, l := range strings.Split(r.Body, "\n") {
		parts := strings.Split(l, "\t")
		for i := range parts {
			parts[i] = search.Printable(parts[i])
		}
		b.WriteString(strings.Join(parts, "\t") + "\n")
	}
	return b.String()
}
