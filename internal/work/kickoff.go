package work

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// Kickoff is one Kickoff file: the worker's prompt and its scope.
type Kickoff struct {
	Path  string   // the file as given
	Name  string   // its base name without .md; the branch is work/<Name>
	Text  string   // the whole file, passed to agy verbatim
	Files []string // the Files: line's entries, as written
	Model string   // the Model: line's ID, trimmed; "" when the file has none
}

// Branch is the worker's branch, in the clone and, once fetched, in the
// lead's checkout.
func (k Kickoff) Branch() string { return "work/" + k.Name }

const (
	maxKickoff = 64 << 10
	trailRel   = ".agents/trail.jsonl"
)

var (
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	recordID = regexp.MustCompile(`^[A-Z]+-[0-9A-Za-z-]+$`)
	// modelRe is what a model ID looks like, for the Model: line and
	// --model: it refuses a flag, a space and a control character offline,
	// and agy models is the authority (refusal 21). An agy that lists an ID
	// outside it widens it here, with its test (D126).
	modelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ParseKickoff reads path as a Kickoff, or refuses it (refusal 3, and
// refusal 4's name rule): a missing, unreadable or oversized file, no single
// Files: line, an entry that is neither a repo-relative path nor a record
// ID, more than one Model: line or one that is not a model ID, or a name
// outside the branch alphabet (D40).
func ParseKickoff(path string) (Kickoff, error) {
	return parseKickoff(path, false)
}

func parseKickoff(path string, auto bool) (Kickoff, error) {
	k := Kickoff{Path: path, Name: strings.TrimSuffix(filepath.Base(path), ".md")}
	f, err := sandbox.OpenRegular(path, os.O_RDONLY)
	if err != nil {
		return k, fmt.Errorf("the Kickoff %s: %v", path, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxKickoff+1))
	if err != nil {
		return k, fmt.Errorf("the Kickoff %s: %v", path, err)
	}
	if len(b) > maxKickoff {
		return k, fmt.Errorf("the Kickoff %s is over %d KiB", path, maxKickoff>>10)
	}
	k.Text = string(b)
	if k.Files, err = filesLine(k.Text, auto); err != nil {
		return k, fmt.Errorf("the Kickoff %s: %v", path, err)
	}
	if k.Model, err = modelLine(k.Text); err != nil {
		return k, fmt.Errorf("the Kickoff %s: %v", path, err)
	}
	if !nameRe.MatchString(k.Name) {
		return k, fmt.Errorf("the Kickoff %s: the name %q is not lower-case letters, digits and '-', at most 63, starting with a letter or digit", path, k.Name)
	}
	return k, nil
}

// prefixed is the text after prefix of the lines that start with it, at
// column 0 (D16).
func prefixed(text, prefix string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimRight(l, "\r"), prefix); ok {
			out = append(out, v)
		}
	}
	return out
}

// modelLine is the ID on the one line that starts "Model:", "" when there
// is none (PLAN-2026-10-29-workers § Interfaces § A model per worker).
func modelLine(text string) (string, error) {
	lines := prefixed(text, "Model:")
	switch {
	case len(lines) == 0:
		return "", nil
	case len(lines) > 1:
		return "", fmt.Errorf("%d Model: lines; give one", len(lines))
	}
	if m := strings.TrimSpace(lines[0]); modelRe.MatchString(m) {
		return m, nil
	}
	return "", fmt.Errorf("the Model: line %q is not a model ID", strings.TrimSpace(lines[0]))
}

// filesLine is the entries of the one line that starts "Files:" (D16). When
// auto is true, a bare "Files:" line with no entry is a drafts-only scope
// (PLAN-2026-10-31-1636-plan-fyr-auto-mode-idle-time-self-improvement-of D37).
func filesLine(text string, auto bool) ([]string, error) {
	lines := prefixed(text, "Files:")
	n := len(lines)
	switch {
	case n == 0:
		return nil, errors.New("no Files: line; name the files and records the worker may change")
	case n > 1:
		return nil, fmt.Errorf("%d Files: lines; give one", n)
	}
	if auto && strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}
	var out []string
	for _, e := range strings.Split(lines[0], ",") {
		e = strings.TrimSpace(e)
		if err := checkEntry(e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// checkEntry accepts a record ID or a clean repo-relative path, a directory
// ending in '/'. Globs are refused (D17), and so is the trail itself: its
// records are named by ID.
func checkEntry(e string) error {
	if recordID.MatchString(e) {
		return nil
	}
	switch {
	case e == "":
		return errors.New("an empty Files: entry")
	case sandbox.Printable(e) != e:
		return fmt.Errorf("the Files: entry %q holds a control character", e)
	case strings.HasPrefix(e, "/"):
		return fmt.Errorf("the Files: entry %s is absolute; give a repo-relative path", e)
	case strings.ContainsAny(e, `*?[\`):
		return fmt.Errorf("the Files: entry %s is a glob; name each file or a directory ending in /", e)
	case e == trailRel:
		return fmt.Errorf("the Files: entry %s: name its records by ID instead", e)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(e, "/"), "/") {
		switch seg {
		case "..":
			return fmt.Errorf("the Files: entry %s contains ..", e)
		case "", ".":
			return fmt.Errorf("the Files: entry %s is not a clean repo-relative path", e)
		}
	}
	return nil
}

// covers reports whether the scope holds the repo-relative path p: an equal
// file entry, or a directory entry it lies under.
func (k Kickoff) covers(p string) bool {
	for _, e := range k.Files {
		if e == p || (strings.HasSuffix(e, "/") && strings.HasPrefix(p, e)) {
			return true
		}
	}
	return false
}

// names reports whether the scope names the record id.
func (k Kickoff) names(id string) bool {
	for _, e := range k.Files {
		if e == id {
			return true
		}
	}
	return false
}
