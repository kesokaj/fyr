package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentDescriptionRules: an agent's description is one line and names
// neither "dispatch prompt" nor "harness binary" (TDD.md § T6; audit
// 2026-09-30, AG12). Until now only inspection enforced it.
func TestAgentDescriptionRules(t *testing.T) {
	for name, front := range map[string]string{
		"two lines":       "name: x\ndescription: |\n  first line\n  second line\nsubagent: true\nmainAgent: false\n",
		"dispatch prompt": "name: x\ndescription: \"the dispatch prompt names the file\"\nsubagent: true\nmainAgent: false\n",
		"harness binary":  "name: x\ndescription: \"run by the harness binary\"\nsubagent: true\nmainAgent: false\n",
	} {
		repo := newRepo(t)
		writeAgent(t, repo, "x", "---\n"+front+"---\nbody\n")
		if _, err := loadAgents(DirSource(repo)); err == nil {
			t.Errorf("%s: loadAgents accepted the description", name)
		}
	}
	// The fixture's own agent (one line, plain words) and the real agents pass.
	if _, err := loadAgents(DirSource(newRepo(t))); err != nil {
		t.Errorf("the fixture's agents: %v", err)
	}
	if _, err := loadAgents(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Errorf("the checkout's agents: %v", err)
	}
}

// TestLintDescriptionLimit: a skill or agent description over 1,024
// characters, the Agent Skills spec's limit, is refused; a multi-line
// description is measured after its continuation lines are joined (audit
// 2026-09-30, D19).
func TestLintDescriptionLimit(t *testing.T) {
	long := strings.Repeat("word ", 206) // 1,029 characters after the trailing space is trimmed
	if _, err := parseStrict("name: x\ndescription: \"" + long + "\"\n"); err == nil {
		t.Error("a 1,025-character description passed Lint")
	}
	block := "name: x\ndescription: |\n"
	for i := 0; i < 11; i++ {
		block += "  " + strings.Repeat("word ", 20) + "\n" // 11 × 100 = 1,100 joined
	}
	if _, err := parseStrict(block); err == nil {
		t.Error("a 1,100-character block description passed Lint")
	}
	if _, err := parseStrict("name: x\ndescription: \"" + strings.Repeat("word ", 204) + "\"\n"); err != nil {
		t.Errorf("a 1,020-character description was refused: %v", err)
	}
	// A block of exactly 1,023 joined characters passes: the `|` indicator
	// and its space are not counted (code review of the fix).
	block = "name: x\ndescription: |\n"
	for i := 0; i < 10; i++ {
		block += "  " + strings.Repeat("word ", 20) + "\n"
	}
	block += "  " + strings.Repeat("w", 13) + "\n" // 10 × 99 (joined by spaces) + 1 + 13 = 1,004 + 19 → under the limit
	if _, err := parseStrict(block); err != nil {
		t.Errorf("a block description under the limit was refused: %v", err)
	}
}

// TestLintRefusesAllowedTools: a skill's frontmatter is a permission surface;
// `allowed-tools` lets every named tool run unprompted while the skill is
// active, so a harness skill never sets it — the operator's permission mode
// decides (BUG-143, graduated here).
func TestLintRefusesAllowedTools(t *testing.T) {
	repo := newRepo(t)
	dir := filepath.Join(repo, "skills", "x")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := loadSkills(DirSource(repo)); err == nil || !strings.Contains(err.Error(), "allowed-tools") {
		t.Errorf("a skill with allowed-tools passed: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n---\nbody\n"), 0o644)
	if _, err := loadSkills(DirSource(repo)); err != nil {
		t.Errorf("the same skill without the key was refused: %v", err)
	}
	if _, err := loadSkills(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Errorf("the checkout's skills: %v", err)
	}
}

// TestLintPermissionKeysByKind: the permission-granting frontmatter keys are
// refused per file kind, and a top-level line the installer's key grammar
// cannot read is refused rather than skipped — a quoted or spaced key is the
// same key to a YAML parser (security review 2026-10-01; one rule per kind).
func TestLintPermissionKeysByKind(t *testing.T) {
	skill := func(front string) error {
		repo := newRepo(t)
		dir := filepath.Join(repo, "skills", "x")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\ndescription: a skill\n"+front+"---\nbody\n"), 0o644)
		_, err := loadSkills(DirSource(repo))
		return err
	}
	for name, front := range map[string]string{
		"double-quoted key": "\"allowed-tools\": Bash\n",
		"single-quoted key": "'allowed-tools': Bash\n",
		"spaced colon":      "allowed-tools : Bash\n",
		"block list":        "allowed-tools:\n  - Bash\n",
		"hooks":             "hooks:\n  PreToolUse: []\n",
	} {
		if err := skill(front); err == nil {
			t.Errorf("skill with %s loaded", name)
		}
	}
	for name, front := range map[string]string{
		"metadata map":  "metadata:\n  version: \"1.0\"\n",
		"license":       "license: Apache-2.0\n",
		"comment line":  "# a comment\n",
		"column-0 list": "metadata:\n- agy\n- gemini\n",
	} {
		if err := skill(front); err != nil {
			t.Errorf("skill with %s refused: %v", name, err)
		}
	}
	agent := func(front string) error {
		repo := newRepo(t)
		writeAgent(t, repo, "x", "---\nname: x\ndescription: an agent\nsubagent: true\nmainAgent: false\ntools: [view_file]\n"+front+"---\nbody\n")
		_, err := loadAgents(DirSource(repo))
		return err
	}
	// mcpServers: an inline MCP server is a command line the CLI starts
	// when the agent is used, outside the permission mode (project review
	// 2026-10-09, security row 1; left open by the 2026-10-01 gate).
	// An agent key outside the allow-list is refused, whatever it does:
	// `memory` and `initialPrompt` passed the deny-list (the project review's
	// security round 1, finding 5), and a vendor's next key would too
	// (ADR-056).
	for _, front := range []string{"permissionMode: bypassPermissions\n", "hooks:\n  Stop: []\n", "mcpServers:\n  x:\n    command: sh\n", "mcpServers: {x: {command: sh}}\n",
		"memory: project\n", "initialPrompt: run the tests\n", "\"memory\": user\n", "isolation: worktree\n", "futureKey: x\n",
		// A restriction Antigravity does not honour would leave the agent
		// unrestricted while reading as restricted (ADR-056a).
		"disallowedTools: Write\n",
		// The old format's keys: Antigravity has no equivalent, so a file
		// carrying one was written for another CLI (ADR-062, F6).
		"maxTurns: 10\n", "skills: [golang]\n", "effort: high\n", "color: blue\n"} {
		if err := agent(front); err == nil {
			t.Errorf("agent with %q loaded", front)
		}
	}
	for _, front := range []string{"model: pro\n", "model: flash\n"} {
		if err := agent(front); err != nil {
			t.Errorf("agent with %q refused: %v", front, err)
		}
	}
	repo := newRepo(t)
	os.MkdirAll(filepath.Join(repo, "commands"), 0o755)
	os.WriteFile(filepath.Join(repo, "commands", "x.md"), []byte("---\ndescription: a command\nallowed-tools: Bash\n---\nbody\n"), 0o644)
	if _, err := LoadInventory(DirSource(repo)); err == nil {
		t.Error("a command with allowed-tools loaded")
	}
	if _, err := LoadInventory(DirSource(filepath.Join("..", ".."))); err != nil {
		t.Errorf("the checkout: %v", err)
	}
}

// TestLintRefusesUnreadableFrontmatterInDocs: a command or rule whose first
// line opens a frontmatter block that Split cannot read was once treated as
// having none, so the permission-key lint never saw it and the file deployed
// as written (the v2.1.0 gate's L5). A file with no frontmatter still loads.
func TestLintRefusesUnreadableFrontmatterInDocs(t *testing.T) {
	load := func(kind, text string) error {
		repo := newRepo(t)
		os.MkdirAll(filepath.Join(repo, kind), 0o755)
		os.WriteFile(filepath.Join(repo, kind, "x.md"), []byte(text), 0o644)
		_, err := LoadInventory(DirSource(repo))
		return err
	}
	for _, kind := range []string{"commands", "rules"} {
		for name, text := range map[string]string{
			"a byte-order mark":           "\ufeff---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"spaces after the opening":    "---  \ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a tab after the opening":     "---\t\ndescription: d\nhooks:\n  Stop: []\n---\nbody\n",
			"an unterminated block":       "---\ndescription: d\nallowed-tools: Bash\nbody with no closing line\n",
			"a carriage return and space": "--- \r\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			// Spellings a lenient reader could still take for a fence
			// (security review of v2.1.0..a62eb9d).
			"a no-break space after":   "---\u00a0\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a form feed after":        "---\f\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"an ideographic space":     "---\u3000\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"two byte-order marks":     "\ufeff\ufeff---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a leading blank line":     "\n---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a tag on the fence":       "---yaml\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a comment on the fence":   "--- # c\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
			"a zero-width space first": "\u200b---\ndescription: d\nallowed-tools: Bash\n---\nbody\n",
		} {
			err := load(kind, text)
			if err == nil || !strings.Contains(err.Error(), "frontmatter starts but cannot be read") {
				t.Errorf("%s with %s: want the unreadable-frontmatter refusal, got %v", kind, name, err)
			}
		}
		for name, text := range map[string]string{
			"no frontmatter":             "# A title\n\nbody\n",
			"a rule line further down":   "A first line.\n\n---\n\nmore\n",
			"a readable block":           "---\ndescription: d\n---\nbody\n",
			"four dashes (a rule, text)": "----\nbody\n",
		} {
			if err := load(kind, text); err != nil {
				t.Errorf("%s with %s refused: %v", kind, name, err)
			}
		}
	}
}

// TestLintSeesEveryKeyAYAMLReaderSees: three shapes hid a permission key
// from the lint while a YAML reader still read it, or may (security reviews
// of v2.2.0..1fc0d4c and 1fc0d4c..4165f81): a block whose lines are all
// indented (YAML reads a top-level map; the key grammar took the lines for
// continuations of nothing), a line break the lint does not split on inside a
// key line (a lone carriage return, NEL, the Unicode line and paragraph
// separators), and a line that starts with a tab (no YAML indentation: a
// reader errors, nests it, or reads a top-level key). Refused in every kind,
// whatever else the file gets right.
func TestLintSeesEveryKeyAYAMLReaderSees(t *testing.T) {
	const hidden = "allowed-tools: Bash(*)"
	// Every kind sets only the keys of its allow-list (ADR-056, IN23), so its
	// keep-loading shapes use them; only a skill's holds a list or a map.
	kinds := map[string]struct {
		keys  []string  // the kind's own required lines
		extra [3]string // a key the kind loads with a value, a list and a map ("" for none)
		load  func(front string) error
	}{
		"skill": {[]string{"name: x", "description: d"}, [3]string{"license", "metadata", "metadata"}, func(front string) error {
			repo := newRepo(t)
			dir := filepath.Join(repo, "skills", "x")
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\n"+front+"---\nbody\n"), 0o644)
			_, err := loadSkills(DirSource(repo))
			return err
		}},
		"agent": {[]string{"name: x", "description: d", "subagent: true", "mainAgent: false"}, [3]string{"model", "", ""}, func(front string) error {
			repo := newRepo(t)
			writeAgent(t, repo, "x", "---\n"+front+"---\nbody\n")
			_, err := loadAgents(DirSource(repo))
			return err
		}},
		"command": {[]string{"description: d"}, [3]string{"description", "", ""}, func(front string) error {
			repo := newRepo(t)
			os.MkdirAll(filepath.Join(repo, "commands"), 0o755)
			os.WriteFile(filepath.Join(repo, "commands", "x.md"), []byte("---\n"+front+"---\nbody\n"), 0o644)
			_, err := LoadInventory(DirSource(repo))
			return err
		}},
		"rule": {[]string{"description: d"}, [3]string{"trigger", "", ""}, func(front string) error {
			repo := newRepo(t)
			os.MkdirAll(filepath.Join(repo, "rules"), 0o755)
			os.WriteFile(filepath.Join(repo, "rules", "x.md"), []byte("---\n"+front+"---\nbody\n"), 0o644)
			_, err := LoadInventory(DirSource(repo))
			return err
		}},
	}
	for kind, k := range kinds {
		plain := strings.Join(k.keys, "\n") + "\n"
		refused := map[string]string{
			"an indented block":      "  " + strings.Join(k.keys, "\n  ") + "\n  " + hidden + "\n",
			"an indented first line": "  " + hidden + "\n" + plain,
			// A tab is no YAML indentation: a strict reader errors on the
			// line, a lenient one nests it, an older one reads a top-level
			// key (security review of 1fc0d4c..4165f81).
			"a tab-indented first line":        "\t" + hidden + "\n" + plain,
			"a tab-led key after a key":        plain + "\t" + hidden + "\n",
			"a tab-led key after a nested map": plain + "metadata:\n  version: \"1.0\"\n\t" + hidden + "\n",
			"a tab-led key after a list":       plain + "compatibility:\n  - agy\n\t" + hidden + "\n",
			"a tab-led key after an empty key": plain + "metadata:\n\t" + hidden + "\n",
		}
		for name, sep := range map[string]string{
			"a lone carriage return":         "\r",
			"a next-line character (U+0085)": "\u0085",
			"a line separator (U+2028)":      "\u2028",
			"a paragraph separator (U+2029)": "\u2029",
		} {
			refused[name] = strings.TrimSuffix(plain, "\n") + sep + hidden + "\n"
		}
		for name, front := range refused {
			err := k.load(front)
			if err == nil {
				t.Errorf("%s with %s loaded", kind, name)
				continue
			}
			// The refusal is the lint's, not a side effect such as a
			// missing description.
			if msg := err.Error(); !strings.Contains(msg, "indented") && !strings.Contains(msg, "line break") && !strings.Contains(msg, "starts with a tab") {
				t.Errorf("%s with %s refused for another reason: %v", kind, name, err)
			}
		}
		// What must keep loading: a nested map, a list, a CRLF file.
		value, list, nested := k.extra[0], k.extra[1], k.extra[2]
		// The required lines without the value key's own, so a kind whose
		// only allowed key is required still gets the value shapes.
		var rest []string
		for _, l := range k.keys {
			if !strings.HasPrefix(l, value+":") {
				rest = append(rest, l)
			}
		}
		base := ""
		if len(rest) > 0 {
			base = strings.Join(rest, "\n") + "\n"
		}
		for name, front := range map[string]string{
			"a nested map":     plain + nested + ":\n  version: \"1.0\"\n",
			"an indented list": plain + list + ":\n  - agy\n",
			"a CRLF file":      strings.ReplaceAll(base, "\n", "\r\n") + value + ": MIT\r\n",
			"a comment first":  "# a comment\n" + plain,
			// A tab inside a value, and a line that is only a tab, are not
			// tab indentation.
			"a tab inside a value": base + value + ": MIT\tor Apache\n",
			"a tab-only line":      base + "\t\n" + value + ": MIT\n",
		} {
			if nested == "" && name == "a nested map" || list == "" && name == "an indented list" {
				continue
			}
			if err := k.load(front); err != nil {
				t.Errorf("%s with %s refused: %v", kind, name, err)
			}
		}
	}
	// A folded value under its key is a continuation, not a hidden key
	// (skills allow one; agents want a one-line description).
	if err := kinds["skill"].load("name: x\ndescription: >-\n  a description\n  over two lines\n"); err != nil {
		t.Errorf("a skill with a folded description refused: %v", err)
	}
}
