// Package harness installs the fyr harness (global context, skills,
// subagents) into the Antigravity CLI, and reports drift
// between the repo and what is deployed.
package harness

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
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

// parseStrict lints and parses the block in one pass over its entries: the
// installer's form, so a file is read once and a value a strict YAML parser
// would read differently is refused before it is used.
func parseStrict(front string) (Frontmatter, error) {
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
// read. Blank lines, comments, and indented continuations are fine; anything
// else (`"key": v`, `key : v`, column-0 `- ` list items) is skipped by entries
// while a YAML parser reads it, so a lint keyed on names never saw it
// (security review 2026-10-01, IN79).
func unreadLine(front string) (string, bool) {
	for _, l := range strings.Split(front, "\n") {
		// One carriage return is the line ending (a second one was refused
		// by parseStrict's line-break check before this runs).
		l = strings.TrimSuffix(l, "\r")
		if l == "" || l[0] == ' ' || l[0] == '\t' || l[0] == '#' {
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

// keysOf are the top-level keys of a frontmatter block, whatever their values'
// shape: a nested map or a list is a key here though Parse drops its value.
func keysOf(front string) []string {
	var out []string
	for _, e := range entries(front) {
		out = append(out, e.key)
	}
	return out
}

// unlistedKey is the first key of front that allowed does not name: every
// kind's lint is an allow list (agentKeys, skillKeys, commandKeys, ruleKeys).
func unlistedKey(front string, allowed []string) string {
	for _, k := range keysOf(front) {
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
			// flow form so splitList reads both spellings the same way.
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
// single spaces (plan audit-fix D19). Agents keep this 1024-char cap;
// harness skills enforce SkillDescriptionLimit (140) in LintSkillFrontmatter.
const descriptionLimit = 1024

// SkillDescriptionLimit caps harness SKILL.md frontmatter descriptions
// (PRD R1, TDD T2, F-017) so skill listings stay compact.
const SkillDescriptionLimit = 140

// LintSkillFrontmatter validates a skills/<entry>/SKILL.md file's YAML
// frontmatter: strict YAML syntax, valid skill name, non-empty description
// within SkillDescriptionLimit runes/bytes, and the skillKeys allowlist
// (rejecting permission keys such as allowed-tools).
func LintSkillFrontmatter(entry string, data []byte) (Frontmatter, error) {
	front, _, err := Split(string(data))
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", entry, err)
	}
	fm, err := parseStrict(front)
	if err != nil {
		return nil, fmt.Errorf("skill %s: %w", entry, err)
	}
	name := fm["name"]
	if name == "" {
		name = entry
	}
	if err := validName("skill", entry+"/SKILL.md", name); err != nil {
		return nil, err
	}
	desc := strings.TrimSpace(fm["description"])
	if desc == "" {
		return nil, fmt.Errorf("skill %s: SKILL.md has no description", entry)
	}
	if n := utf8.RuneCountInString(desc); n > SkillDescriptionLimit || len(desc) > SkillDescriptionLimit {
		return nil, fmt.Errorf("skill %s: description is %d chars (%d bytes), limit is %d", name, n, len(desc), SkillDescriptionLimit)
	}
	if k := unlistedKey(front, skillKeys); k != "" {
		return nil, fmt.Errorf("skill %s: %s is not a key a harness skill sets (%s); a new one is added to skillKeys by a D-row, and a key that grants a permission never is — the operator's permission mode decides what runs unprompted (IN23, BUG-143)", name, k, strings.Join(skillKeys, ", "))
	}
	return fm, nil
}

// lintEntries rejects what this lenient parser would read differently from
// a strict YAML parser: a plain (unquoted, non-block) scalar starting with
// "#" or containing "#" after a space or tab, which YAML reads as a comment
// (the Antigravity CLI then drops the skill, BUG-004), unterminated quotes,
// unquoted ": " mapping indicators in plain scalars, and a description over
// the Agent Skills limit. parseStrict is its one caller.
func lintEntries(es []entry) error {
	seen := make(map[string]bool, len(es))
	for _, e := range es {
		if seen[e.key] {
			return fmt.Errorf("frontmatter key %q is duplicated", e.key)
		}
		seen[e.key] = true
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
		if err := lintEntryScalar(e); err != nil {
			return err
		}
	}
	return nil
}

func lintEntryScalar(e entry) error {
	val := e.val
	if isBlockIndicator(val) {
		return nil
	}
	if val == "" && len(e.cont) > 0 && strings.Contains(e.cont[0], ": ") {
		return nil // nested map; its own keys are the vendor's business
	}
	quoted := isQuoted(val)
	lines := e.cont
	if !quoted {
		lines = append([]string{val}, lines...)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "#") || strings.Contains(l, " #") || strings.Contains(l, "\t#") {
			return fmt.Errorf("frontmatter key %q: unquoted value contains \"#\", which YAML reads as a comment; quote the value", e.key)
		}
	}
	if !quoted && (strings.HasPrefix(val, "\"") || strings.HasPrefix(val, "'")) {
		return fmt.Errorf("frontmatter key %q: unterminated quoted value", e.key)
	}
	if !quoted && !strings.HasPrefix(val, "[") && !strings.HasPrefix(val, "{") {
		for _, l := range lines {
			if strings.Contains(l, ": ") || strings.HasSuffix(l, ":") {
				return fmt.Errorf("frontmatter key %q: unquoted value contains \":\", which YAML reads as a mapping; quote the value", e.key)
			}
		}
	}
	return nil
}

func isQuoted(v string) bool {
	if len(v) < 2 {
		return false
	}
	if v[0] == '"' {
		for i := 1; i < len(v); i++ {
			if v[i] == '\\' {
				i++
				continue
			}
			if v[i] == '"' {
				return i == len(v)-1
			}
		}
		return false
	}
	if v[0] == '\'' {
		for i := 1; i < len(v); i++ {
			if v[i] == '\'' {
				if i+1 < len(v) && v[i+1] == '\'' {
					i++
					continue
				}
				return i == len(v)-1
			}
		}
		return false
	}
	return false
}

func isBlockIndicator(v string) bool {
	switch v {
	case "|", "|-", "|+", ">", ">-", ">+":
		return true
	}
	return false
}

func unquote(v string) string {
	if !isQuoted(v) {
		return v
	}
	switch v[0] {
	case '"':
		inner := v[1 : len(v)-1]
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		return strings.ReplaceAll(inner, `\\`, `\`)
	case '\'':
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// splitList reads a YAML list written either as `[a, b]`, `a, b`, or `a b`.
func splitList(s string) []string {
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
