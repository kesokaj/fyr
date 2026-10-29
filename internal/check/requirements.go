package check

import (
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
)

var (
	// itemRe is a Must or Should item of the init-project template:
	// `- **R1** — <one sentence>.`, struck as `- **R3** — struck <date>: …`.
	itemRe     = regexp.MustCompile(`^- \*\*([A-Za-z][0-9]+)\*\*\s*—\s*(.*)$`)
	testNameRe = regexp.MustCompile(`^Test\w+$`)
	testDeclRe = regexp.MustCompile(`(?m)^func (Test\w+)\(`)
)

const enforcedBy = "enforced by:"

// requirementsRule is rule (c) (plan D8, D22, D30, D31): every Must and
// Should item of the requirements file names, in its comment, an `enforced
// by:` site that resolves. The file is PRD.md, or REQUIREMENTS.md in a
// project not yet migrated (PLAN-2026-10-29-root-docs-and-fyr-serve D6). A
// file with no such field anywhere is not checked; a note says so.
func (k *checker) requirementsRule(recs []brain.Record) ([]Finding, []string) {
	name, notes := "PRD.md", []string(nil)
	if _, err := k.root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		name = "REQUIREMENTS.md"
	} else if _, err := k.root.Lstat("REQUIREMENTS.md"); !errors.Is(err, fs.ErrNotExist) {
		notes = append(notes, "REQUIREMENTS.md beside PRD.md: not read")
	}
	data, err := k.readFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, append(notes, "no PRD.md or REQUIREMENTS.md: requirements not checked")
	}
	if err != nil {
		return nil, append(notes, name+" is not a regular file of at most 16 MiB: requirements not checked")
	}
	text := string(data)
	if !strings.Contains(text, enforcedBy) {
		return nil, append(notes, name+" has no enforced-by field: not checked")
	}
	ids := map[string]bool{}
	for _, r := range recs {
		ids[r.ID] = true
	}
	var out []Finding
	for _, it := range requirementItems(text) {
		why := k.unenforced(name, it.block, ids)
		if why != "" {
			out = append(out, Finding{name, it.line, "requirement", it.id + ": " + why})
		}
	}
	return out, notes
}

// item is one Must or Should item that is not struck: its ID, its line and
// the text from its line to the next item or heading.
type item struct {
	id    string
	line  int
	block string
}

// requirementItems reads the template's shape: items under a `## Must` or
// `## Should` heading, each running to the next item or heading. `## Could`
// and every other section are not read, and a struck item is skipped.
func requirementItems(text string) []item {
	var out []item
	var cur *item
	read := false
	end := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	for i, l := range strings.Split(text, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if strings.HasPrefix(l, "#") {
			end()
			if h, ok := strings.CutPrefix(l, "## "); ok {
				read = strings.HasPrefix(h, "Must") || strings.HasPrefix(h, "Should")
			}
			continue
		}
		if m := itemRe.FindStringSubmatch(l); m != nil {
			end()
			if read && !strings.HasPrefix(m[2], "struck") {
				cur = &item{id: m[1], line: i + 1}
			}
		}
		if cur != nil {
			cur.block += l + "\n"
		}
	}
	end()
	return out
}

// unenforced is why an item's block in the file where names no enforcement
// site that resolves, or "".
func (k *checker) unenforced(where, block string, ids map[string]bool) string {
	value, ok := "", false
	for rest := block; ; {
		_, after, open := strings.Cut(rest, "<!--")
		if !open {
			break
		}
		comment, tail, _ := strings.Cut(after, "-->")
		if _, v, has := strings.Cut(comment, enforcedBy); has {
			value, ok = strings.TrimSpace(v), true
			break
		}
		rest = tail
	}
	switch {
	case !ok:
		return "no `enforced by:` comment"
	case value == "":
		return "an empty `enforced by:`"
	case strings.Contains(value, "<"):
		return "`enforced by:` is the template's placeholder"
	case strings.HasPrefix(value, "not yet enforced:"):
		return "`enforced by:` is not yet enforced"
	}
	var bad []string
	for _, part := range strings.Split(value, ",") {
		tok := siteToken(part)
		if tok != "" && !k.resolves(where, tok, ids) {
			bad = append(bad, "`"+tok+"`")
		}
	}
	if len(bad) > 0 {
		return "`enforced by:` does not resolve: " + strings.Join(bad, ", ")
	}
	return ""
}

// siteToken is an enforced-by part's first token (D22): up to whitespace,
// `::` or `#`, so ` § note` and `::case` are the reader's, not the site;
// backticks around it are dropped.
func siteToken(part string) string {
	f := strings.Fields(part)
	if len(f) == 0 {
		return ""
	}
	tok := f[0]
	for _, sep := range []string{"::", "#"} {
		tok, _, _ = strings.Cut(tok, sep)
	}
	return strings.Trim(tok, "`")
}

// resolves reports whether a site token names a Go test declared in a
// tracked *_test.go, a record of the trail, or a path of the checkout (with
// the path rule's existence and :N checks); where is the file that names it.
func (k *checker) resolves(where, tok string, ids map[string]bool) bool {
	if testNameRe.MatchString(tok) {
		return k.testDeclared(tok)
	}
	if ids[tok] {
		return true
	}
	if strings.HasPrefix(tok, "/") || strings.Contains(tok, "..") {
		return false
	}
	c := candidate{where: where, token: tok}
	c.p, c.n, c.m, c.hasLine = splitLine(tok)
	if c.p == "" || c.p == "." || !k.isTracked(c.p) {
		return false
	}
	_, bad := k.checkPath(c)
	return !bad
}

// isTracked reports whether git tracks p, or p is a directory holding a
// tracked file: an ignored local file resolves nothing on another checkout
// (IN54 of the 2026-10-27 audit).
func (k *checker) isTracked(p string) bool {
	p = strings.TrimSuffix(filepath.ToSlash(p), "/")
	for _, t := range k.tracked {
		if t == p || strings.HasPrefix(t, p+"/") {
			return true
		}
	}
	return false
}

// testDeclared reports whether `func <name>(` opens a line of a tracked
// *_test.go; the set is read once per run.
func (k *checker) testDeclared(name string) bool {
	if k.tests == nil {
		k.tests = map[string]bool{}
		for _, p := range k.tracked {
			if !strings.HasSuffix(p, "_test.go") {
				continue
			}
			data, err := k.readFile(filepath.FromSlash(p))
			if err != nil {
				k.skipped = append(k.skipped, p)
				continue
			}
			for _, m := range testDeclRe.FindAllStringSubmatch(string(data), -1) {
				k.tests[m[1]] = true
			}
		}
	}
	return k.tests[name]
}
