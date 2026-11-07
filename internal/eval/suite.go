// Package eval runs a project's behavioural rows against the Antigravity CLI,
// each in its own sandbox: a clone of the fixture, a throwaway HOME with the
// harness installed and the operator's login linked in, and every check the
// run kit's preamble made around the run (plan
// 2026-10-26-0659-plan-fyr-eval-the-antigravity-run-kit-as-a-verb). It
// imports no SQLite driver (D10): the suite comes in as a record body, and
// the stores are written by cmd/fyr after Run returns.
package eval

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// suiteFileLimit bounds a --suite-file read so an oversized file cannot exhaust memory.
const suiteFileLimit = 1 << 20

// ReadSuiteFile reads a suite file in GUIDE-eval's format from path, which must
// be a regular file (no symlink) outside every git checkout (D15).
func ReadSuiteFile(path string) ([]Row, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("--suite-file: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("--suite-file: %v", err)
	}
	f, err := sandbox.OpenRegular(abs, os.O_RDONLY)
	if err != nil {
		return nil, fmt.Errorf("--suite-file %s: %v", abs, err)
	}
	defer f.Close()
	dir := filepath.Dir(abs)
	if err := sandbox.OutsideCheckout(dir); err != nil {
		return nil, fmt.Errorf("--suite-file %s is inside a git checkout; use a file outside every checkout", abs)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("--suite-file %s: %v", abs, err)
	}
	if resolvedDir != dir {
		if err := sandbox.OutsideCheckout(resolvedDir); err != nil {
			return nil, fmt.Errorf("--suite-file %s is inside a git checkout; use a file outside every checkout", abs)
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, suiteFileLimit+1))
	if err != nil {
		return nil, fmt.Errorf("--suite-file %s: %v", abs, err)
	}
	if len(b) > suiteFileLimit {
		return nil, fmt.Errorf("--suite-file %s: over %d bytes", abs, suiteFileLimit)
	}
	return ParseSuite(string(b))
}

// Row is one scenario of the suite: a setup, one or two prompts, a check,
// and optionally an agent the conversation store must show invoked. The
// next four fields are the long header's optional columns, defaulted when
// the table or the cell has none (ADR-085); Expect is the entries joined by
// ",". Timeout and Turns are the 13-column benchmark header's optional
// columns, 0 when unset (ADR-509).
type Row struct {
	ID, Setup, Prompt, Prompt2, Check, Agent, Grants string
	Version                                          int
	Kind, Cluster, Expect                            string
	Timeout                                          time.Duration
	Turns                                            int
}

// Wide reports whether the row runs with command(*), and why.
func (r Row) Wide() (reason string, ok bool) {
	return strings.CutPrefix(r.Grants, "wide: ")
}

var suiteHeader = []string{"id", "setup", "prompt", "prompt2", "check", "agent", "grants"}

var (
	// An id names a sandbox directory, a metrics label and the default
	// cluster, so it is one path element of plain characters, at most 64
	// bytes as the identifier rule allows (ADR-085).
	validID    = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	validAgent = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	leadingID  = regexp.MustCompile("^`([^`]+)`")
	// Any {{NAME}} in a prompt: the four eval fills, or a typo refused at parse.
	placeholder = regexp.MustCompile(`\{\{[^{}]*\}\}`)
)

// Placeholders are the names a prompt may carry as {{NAME}}; eval fills them
// itself, never through a shell, so a prompt is never code (D21).
var Placeholders = []string{"CLONE", "SB", "FIXDIR", "STAMP"}

// suiteLongHeader is suiteHeader followed by the four optional columns.
var suiteLongHeader = append(append([]string{}, suiteHeader...), optionalHeader...)

// suiteBenchHeader is suiteLongHeader followed by timeout and turns (ADR-509).
var suiteBenchHeader = append(append([]string{}, suiteLongHeader...), benchmarkHeader...)

// ParseSuite reads the one table of GUIDE-eval's body whose header is
// `| id | setup | prompt | prompt2 | check | agent | grants |`, or that
// followed by `| version | kind | cluster | expect |` (ADR-085), or that
// followed by `| timeout | turns |` (ADR-509). Any prose may
// surround it. A table that does not parse is refused with its line number.
func ParseSuite(body string) ([]Row, error) {
	lines := strings.Split(body, "\n")
	start, width, err := suiteStart(lines)
	if err != nil {
		return nil, err
	}
	var rows []Row
	seen := map[string]int{}
	for i := start + 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(l, "|") {
			break
		}
		n := i + 1
		if isSeparator(l) {
			continue
		}
		r, err := parseRow(l, n, width)
		if err != nil {
			return nil, err
		}
		if first, dup := seen[r.ID]; dup {
			return nil, fmt.Errorf("GUIDE-eval line %d: duplicate id %s (first at line %d)", n, r.ID, first)
		}
		seen[r.ID] = n
		if err := checkRow(r, n); err != nil {
			return nil, err
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("GUIDE-eval line %d: the suite table has no rows", start+1)
	}
	return rows, nil
}

// suiteStart is the index of the suite table's header line, the one table
// with suiteHeader, suiteLongHeader or suiteBenchHeader, and that header's width.
func suiteStart(lines []string) (int, int, error) {
	start, width := -1, 0
	for i, l := range lines {
		cells, ok := splitCells(l)
		if !ok || !(equalCells(cells, suiteHeader) || equalCells(cells, suiteLongHeader) || equalCells(cells, suiteBenchHeader)) {
			continue
		}
		if start >= 0 {
			return 0, 0, fmt.Errorf("GUIDE-eval line %d: a second suite table (the first is at line %d)", i+1, start+1)
		}
		start, width = i, len(cells)
	}
	if start < 0 {
		return 0, 0, fmt.Errorf("GUIDE-eval has no table with the header | %s |, or that followed by | %s |", strings.Join(suiteHeader, " | "), strings.Join(optionalHeader, " | "))
	}
	return start, width, nil
}

// parseRow reads table line l, line n of the body, into a Row of a table
// width columns wide.
func parseRow(l string, n, width int) (Row, error) {
	if !strings.HasPrefix(l, "| `") {
		return Row{}, fmt.Errorf("GUIDE-eval line %d: a row starts with | `id`", n)
	}
	cells, _ := splitCells(l)
	if len(cells) != width {
		return Row{}, fmt.Errorf("GUIDE-eval line %d: %d columns, want %d (%s)", n, len(cells), width, strings.Join(suiteBenchHeader[:width], ", "))
	}
	m := leadingID.FindStringSubmatch(cells[0])
	if m == nil || !validID.MatchString(m[1]) {
		return Row{}, fmt.Errorf("GUIDE-eval line %d: the id is not one backticked name of a-z, 0-9, '.', '_' or '-', at most 64 bytes", n)
	}
	r := Row{
		ID:      m[1],
		Setup:   codeCell(cells[1]),
		Prompt:  textCell(cells[2]),
		Prompt2: textCell(cells[3]),
		Check:   codeCell(cells[4]),
		Agent:   textCell(cells[5]),
		Grants:  textCell(cells[6]),
	}
	cells = append(cells[len(suiteHeader):], "", "", "", "", "", "")
	err := optionalCells(&r, cells, n)
	return r, err
}

// checkRow refuses a row with no prompt or check, an agent that is not a
// name, grants that are neither named nor a reasoned wide, an incompatible
// turns/prompt2 combination, or a placeholder eval does not fill.
func checkRow(r Row, n int) error {
	if r.Prompt == "" {
		return fmt.Errorf("GUIDE-eval line %d: row %s has no prompt", n, r.ID)
	}
	if r.Check == "" {
		return fmt.Errorf("GUIDE-eval line %d: row %s has no check", n, r.ID)
	}
	if r.Agent != "" && !validAgent.MatchString(r.Agent) {
		return fmt.Errorf("GUIDE-eval line %d: row %s: agent %q is not an agent name", n, r.ID, r.Agent)
	}
	if reason, wide := r.Wide(); !(r.Grants == "named" || wide && strings.TrimSpace(reason) != "") {
		return fmt.Errorf("GUIDE-eval line %d: row %s: grants is %q, want named or wide: <reason>", n, r.ID, r.Grants)
	}
	if r.Turns == 1 && r.Prompt2 != "" {
		return fmt.Errorf("GUIDE-eval line %d: row %s: turns 1 cannot have prompt2", n, r.ID)
	}
	if r.Turns > 1 && r.Prompt2 == "" {
		return fmt.Errorf("GUIDE-eval line %d: row %s: turns %d requires prompt2", n, r.ID, r.Turns)
	}
	for _, p := range []string{r.Prompt, r.Prompt2} {
		for _, ph := range placeholder.FindAllString(p, -1) {
			if !known(ph) {
				return fmt.Errorf("GUIDE-eval line %d: row %s: unknown placeholder %s (known: {{%s}})", n, r.ID, ph, strings.Join(Placeholders, "}}, {{"))
			}
		}
	}
	return nil
}

// SelectRows returns the named rows in the order named; an unknown or a
// repeated name is refused before anything runs (D20).
func SelectRows(suite []Row, names []string) ([]Row, error) {
	byID := map[string]Row{}
	for _, r := range suite {
		byID[r.ID] = r
	}
	var out []Row
	picked := map[string]bool{}
	for _, n := range names {
		r, ok := byID[n]
		if !ok {
			return nil, fmt.Errorf("no row %q in GUIDE-eval", n)
		}
		if picked[n] {
			return nil, fmt.Errorf("row %s is named twice", n)
		}
		picked[n] = true
		out = append(out, r)
	}
	return out, nil
}

// Expand fills a prompt's placeholders from vars (keyed by name, without
// braces). It is plain substitution: nothing in a value is interpreted.
func Expand(prompt string, vars map[string]string) string {
	var pairs []string
	for _, name := range Placeholders {
		pairs = append(pairs, "{{"+name+"}}", vars[name])
	}
	return strings.NewReplacer(pairs...).Replace(prompt)
}

func known(ph string) bool {
	for _, name := range Placeholders {
		if ph == "{{"+name+"}}" {
			return true
		}
	}
	return false
}

// splitCells splits a table line on every | that is not escaped as \| and
// drops the empty cells outside the first and last bar; each cell is trimmed
// and its \| unescaped.
func splitCells(l string) ([]string, bool) {
	l = strings.TrimSpace(l)
	if !strings.HasPrefix(l, "|") || !strings.HasSuffix(l, "|") || len(l) < 2 {
		return nil, false
	}
	var cells []string
	var cur strings.Builder
	for i := 1; i < len(l); i++ {
		switch {
		case l[i] == '\\' && i+1 < len(l) && l[i+1] == '|':
			cur.WriteByte('|')
			i++
		case l[i] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(l[i])
		}
	}
	return cells, true
}

func equalCells(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isSeparator(l string) bool {
	return strings.Trim(strings.TrimSpace(l), "|-: ") == ""
}

// textCell strips one pair of backticks that wraps the whole cell and
// nothing more: `a` and `b` stays as written.
func textCell(c string) string {
	if len(c) >= 2 && c[0] == '`' && c[len(c)-1] == '`' && !strings.Contains(c[1:len(c)-1], "`") {
		return c[1 : len(c)-1]
	}
	return c
}

// codeCell is a setup or check cell: when it starts with a backticked span,
// only that span is kept, so a note after the check ("(changed …)") never
// reaches bash, where its backticks would run as a command substitution
// (run.sh:190-192, D23).
func codeCell(c string) string {
	if strings.HasPrefix(c, "`") {
		if end := strings.IndexByte(c[1:], '`'); end >= 0 {
			return c[1 : 1+end]
		}
	}
	return c
}
