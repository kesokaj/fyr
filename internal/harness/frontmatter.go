// Package harness installs the sh-agent harness (global context, skills,
// subagents) into the Antigravity CLI, and reports drift
// between the repo and what is deployed.
package harness

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Frontmatter is the YAML header of a SKILL.md or agent file, flattened to
// strings. Block scalars are folded onto one line, lists keep their bracketed
// source form, and nested maps are dropped. That is everything the installer
// needs, and it keeps the parser dependency-free.
type Frontmatter map[string]string

var keyLine = regexp.MustCompile(`^([A-Za-z0-9_-]+):(?:[ \t]+(.*))?$`)

// Split separates a leading `---` block from the body that follows it.
func Split(text string) (front, body string, err error) {
	// A fence is `---` and at most one carriage return before the newline:
	// `---\r\r` is some other line to a YAML reader, and taking it for the
	// closing fence left what followed unchecked.
	fence := func(l string) bool {
		return strings.TrimSuffix(strings.TrimSuffix(l, "\n"), "\r") == "---"
	}
	first, rest, ok := strings.Cut(text, "\n")
	if !ok || !fence(first) {
		return "", "", errors.New("no YAML frontmatter block")
	}
	lines := strings.SplitAfter(rest, "\n")
	for i, l := range lines {
		if fence(l) {
			return strings.Join(lines[:i], ""), strings.Join(lines[i+1:], ""), nil
		}
	}
	return "", "", errors.New("unterminated YAML frontmatter block")
}

// entry is one top-level key of a frontmatter block: the value on the key
// line and the indented lines that continue it (a block scalar, a nested
// map, a block list, or a plain scalar that wraps).
type entry struct {
	key, val string
	cont     []string
}

func entries(front string) []entry {
	var out []entry
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		m := keyLine.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if m == nil {
			continue
		}
		e := entry{key: m[1], val: strings.TrimSpace(m[2])}
		j := i + 1
		for ; j < len(lines); j++ {
			l := strings.TrimRight(lines[j], "\r")
			if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
				break
			}
			if t := strings.TrimSpace(l); t != "" {
				e.cont = append(e.cont, t)
			}
		}
		out = append(out, e)
		i = j - 1
	}
	return out
}

// Parse reads the top-level keys of a frontmatter block, leniently.
func Parse(front string) Frontmatter { return parseEntries(entries(front)) }

// ParseStrict lints and parses the block in one pass over its entries: the
// installer's form, so a file is read once and a value a strict YAML parser
// would read differently is refused before it is used.
func ParseStrict(front string) (Frontmatter, error) {
	// Three shapes hide a key from the grammar below while a YAML reader
	// still reads it, or may; each is refused before anything is parsed.
	if strings.ContainsAny(strings.ReplaceAll(front, "\r\n", "\n"), "\r\u0085\u2028\u2029") {
		return nil, errors.New("frontmatter holds a line break that is not a newline (a lone carriage return, U+0085, U+2028 or U+2029): a YAML reader starts a new line there, and a key after it would not be checked, so the block is refused")
	}
	if l, bad := tabLed(front); bad {
		return nil, fmt.Errorf("frontmatter line %q starts with a tab: YAML has no tab indentation, and readers disagree about such a line (an error, a nested value, a top-level key), so a key on it would not be checked and the block is refused; indent with spaces", l)
	}
	if l, bad := indentedBeforeKey(front); bad {
		return nil, fmt.Errorf("frontmatter line %q is indented before any key: a YAML reader takes an indented block for the top-level map, whose keys would not be checked here, so it is refused", l)
	}
	if l, bad := unreadLine(front); bad {
		// A `---` inside the block is a fence that Split did not take for
		// one (a trailing space, text after it): say that, not "key". Two
		// carriage returns never get here: the line-break check above
		// refuses the block first.
		if strings.HasPrefix(strings.TrimSpace(l), "---") {
			return nil, fmt.Errorf("frontmatter holds a `---` line that is not a fence (%q, as read): a fence is exactly --- on its own line, with at most one carriage return; what follows it would not be checked, so the block is refused", l)
		}
		return nil, fmt.Errorf("frontmatter line %q is not a plain `key: value` line: a quoted or spaced key is a key to a YAML parser and would be skipped here, so it is refused", l)
	}
	es := entries(front)
	if err := lintEntries(es); err != nil {
		return nil, err
	}
	return parseEntries(es), nil
}

// unreadLine is the first top-level frontmatter line the key grammar cannot
// read. Blank lines, comments, indented continuations and column-0 list items
// are fine; anything else (`"key": v`, `key : v`) is a key to a YAML parser
// that entries would silently skip, so a lint keyed on names never saw it
// (security review 2026-10-01).
func unreadLine(front string) (string, bool) {
	for _, l := range strings.Split(front, "\n") {
		// One carriage return is the line ending (a second one was refused
		// by ParseStrict's line-break check before this runs).
		l = strings.TrimSuffix(l, "\r")
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#' || l == "-" || strings.HasPrefix(l, "- ") {
			continue
		}
		if !keyLine.MatchString(l) {
			return l, true
		}
	}
	return "", false
}

// tabLed is the first frontmatter line that starts with a tab and holds more
// than white space. YAML has no tab indentation: a strict reader errors on
// such a line, a lenient one nests it under the key above, and an older one
// reads a key at the top level, while entries takes it for a continuation
// and never checks it (security review of 1fc0d4c..4165f81).
func tabLed(front string) (string, bool) {
	for _, l := range strings.Split(front, "\n") {
		if strings.HasPrefix(l, "\t") && strings.TrimSpace(l) != "" {
			return strings.TrimSuffix(l, "\r"), true
		}
	}
	return "", false
}

// indentedBeforeKey is the first indented line that no key line precedes.
// An indented line is a continuation of the key above it; with no key above,
// it is the start of a block a YAML reader reads as the top-level map, one
// level in, and unreadLine would skip every line of it.
func indentedBeforeKey(front string) (string, bool) {
	for _, l := range strings.Split(front, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if strings.TrimSpace(l) == "" || l[0] == '#' {
			continue
		}
		if l[0] == ' ' || l[0] == '\t' {
			return l, true
		}
		return "", false // the first content line starts at column 0
	}
	return "", false
}

// Keys are the top-level keys of a frontmatter block, whatever their values'
// shape: a nested map or a list is a key here though Parse drops its value.
func Keys(front string) []string {
	var out []string
	for _, e := range entries(front) {
		out = append(out, e.key)
	}
	return out
}

// permissionKey is the first key of front that grants a permission the
// operator's own mode must decide: named per file kind by the caller.
func permissionKey(front string, refused ...string) string {
	for _, k := range Keys(front) {
		for _, r := range refused {
			if k == r {
				return k
			}
		}
	}
	return ""
}

// unlistedKey is the first key of front that allowed does not name.
func unlistedKey(front string, allowed []string) string {
	for _, k := range Keys(front) {
		if !slices.Contains(allowed, k) {
			return k
		}
	}
	return ""
}

func parseEntries(es []entry) Frontmatter {
	fm := Frontmatter{}
	for _, e := range es {
		key, val, cont := e.key, e.val, e.cont
		switch {
		case isBlockIndicator(val):
			fm[key] = strings.Join(cont, " ")
		case val == "" && len(cont) > 0 && strings.HasPrefix(cont[0], "- "):
			// A block list such as `skills:\n  - a\n  - b`, normalised to the
			// flow form so SplitList reads both spellings the same way.
			var items []string
			for _, c := range cont {
				items = append(items, unquote(strings.TrimSpace(strings.TrimPrefix(c, "- "))))
			}
			fm[key] = "[" + strings.Join(items, ", ") + "]"
		case val == "" && len(cont) > 0 && strings.Contains(cont[0], ": "):
			// A nested map such as `metadata:`. Not needed; skipped whole.
		case len(cont) > 0:
			fm[key] = strings.Join(append([]string{unquote(val)}, cont...), " ")
		default:
			fm[key] = unquote(val)
		}
	}
	return fm
}

// descriptionLimit is the Agent Skills specification's limit on a
// description, measured after a block's continuation lines are joined with
// single spaces (plan audit-fix D19).
const descriptionLimit = 1024

// lintEntries rejects what this lenient parser would read differently from
// a strict YAML parser: a plain (unquoted, non-block) scalar starting with
// "#" or containing "#" after a space or tab, which YAML reads as a comment
// (the Antigravity CLI then drops the skill, BUG-004), and a description
// over the Agent Skills limit. Quoting the value fixes the first for every
// reader. ParseStrict is its one caller: it lints and parses in one pass.
func lintEntries(es []entry) error {
	for _, e := range es {
		if e.key == "description" {
			joined := ""
			if !isBlockIndicator(e.val) {
				joined = strings.TrimSpace(unquote(e.val))
			}
			for _, c := range e.cont {
				joined = strings.TrimSpace(joined + " " + strings.TrimSpace(c))
			}
			if n := len([]rune(joined)); n > descriptionLimit {
				return fmt.Errorf("frontmatter key %q: %d characters, the Agent Skills limit is %d", e.key, n, descriptionLimit)
			}
		}
		val := e.val
		if isBlockIndicator(val) || isQuoted(val) {
			continue
		}
		if val == "" && len(e.cont) > 0 && strings.Contains(e.cont[0], ": ") {
			continue // nested map; its own keys are the vendor's business
		}
		lines := append([]string{val}, e.cont...)
		for _, l := range lines {
			if strings.HasPrefix(l, "#") || strings.Contains(l, " #") || strings.Contains(l, "\t#") {
				return fmt.Errorf("frontmatter key %q: unquoted value contains \"#\", which YAML reads as a comment; quote the value", e.key)
			}
		}
	}
	return nil
}

func isQuoted(v string) bool {
	return len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\''))
}

func isBlockIndicator(v string) bool {
	switch v {
	case "|", "|-", "|+", ">", ">-", ">+":
		return true
	}
	return false
}

func unquote(v string) string {
	if len(v) >= 2 {
		switch {
		case v[0] == '"' && v[len(v)-1] == '"':
			inner := v[1 : len(v)-1]
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			return strings.ReplaceAll(inner, `\\`, `\`)
		case v[0] == '\'' && v[len(v)-1] == '\'':
			return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
	}
	return v
}

// SplitList reads a YAML list written either as `[a, b]`, `a, b`, or `a b`.
func SplitList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	sep := ","
	if !strings.Contains(s, ",") {
		sep = " "
	}
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = unquote(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}
