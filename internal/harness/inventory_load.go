package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// readSkill reads skills/<entry>/SKILL.md into a Skill. ok is false for an
// entry that is not a directory and for a directory without a SKILL.md.
func readSkill(src Source, entry string) (s Skill, ok bool, err error) {
	dir := path.Join("skills", entry)
	// fs.Stat follows a link, so a skills/<name> link to a directory is a
	// skill and a dangling one is refused out loud.
	info, err := fs.Stat(src.FS, dir)
	if err != nil {
		return Skill{}, false, fmt.Errorf("resolving skill %s: %w", entry, err)
	}
	if !info.IsDir() {
		return Skill{}, false, nil
	}
	data, err := src.readRegular(path.Join(dir, "SKILL.md"))
	if err != nil {
		// No SKILL.md means the directory is not a skill. Any other
		// error, a symlinked SKILL.md above all, is refused out loud:
		// plan.go's walk only visits skills this loader returned, so a
		// silent skip would drop the refusal altogether (BUG-036).
		if errors.Is(err, fs.ErrNotExist) {
			return Skill{}, false, nil
		}
		return Skill{}, false, err
	}
	s, err = parseSkill(entry, dir, data)
	return s, err == nil, err
}

// parseSkill checks a SKILL.md's frontmatter and returns its Skill.
func parseSkill(entry, dir string, data []byte) (Skill, error) {
	front, _, err := Split(string(data))
	if err != nil {
		return Skill{}, fmt.Errorf("skill %s: %w", entry, err)
	}
	fm, err := parseStrict(front)
	if err != nil {
		return Skill{}, fmt.Errorf("skill %s: %w", entry, err)
	}
	name := fm["name"]
	if name == "" {
		name = entry
	}
	if err := validName("skill", entry+"/SKILL.md", name); err != nil {
		return Skill{}, err
	}
	if fm["description"] == "" {
		return Skill{}, fmt.Errorf("skill %s: SKILL.md has no description", entry)
	}
	if k := unlistedKey(front, skillKeys); k != "" {
		return Skill{}, fmt.Errorf("skill %s: %s is not a key a harness skill sets (%s); a new one is added to skillKeys by a D-row, and a key that grants a permission never is — the operator's permission mode decides what runs unprompted (IN23, BUG-143)", name, k, strings.Join(skillKeys, ", "))
	}
	return Skill{
		Name:        name,
		Description: fm["description"],
		Dir:         dir,
		SlashOnly:   fm["disable-model-invocation"] == "true",
	}, nil
}

// readAgent reads agents/<entry>/agent.md into an Agent.
func readAgent(src Source, entry string) (Agent, error) {
	p := path.Join("agents", entry, "agent.md")
	shown := entry + "/agent.md"
	raw, err := src.readRegular(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Agent{}, fmt.Errorf("%s has no agent.md", src.path(path.Join("agents", entry)))
	}
	if err != nil {
		return Agent{}, err
	}
	front, body, err := Split(string(raw))
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", shown, err)
	}
	fm, err := parseStrict(front)
	if err != nil {
		return Agent{}, fmt.Errorf("agent %s: %w", shown, err)
	}
	name := fm["name"]
	if name == "" {
		name = entry
	}
	if err := validName("agent", shown, name); err != nil {
		return Agent{}, err
	}
	if err := checkAgent(name, front, fm); err != nil {
		return Agent{}, err
	}

	return Agent{Name: name, Description: fm["description"], Path: src.path(p), Front: fm, Body: body, Raw: raw}, nil
}

// checkAgent is the lint of an agent's frontmatter after its name.
func checkAgent(name, front string, fm Frontmatter) error {
	if fm["description"] == "" {
		return fmt.Errorf("agent %s: no description", name)
	}
	// One line, dispatch-worthy, and free of the two phrases that
	// described a mechanism that no longer exists (TDD.md § T6;
	// until 2026-10-01 only inspection enforced this).
	if descriptionIsMultiLine(front) {
		return fmt.Errorf("agent %s: description must be one line", name)
	}
	if k := unlistedKey(front, agentKeys); k != "" {
		return fmt.Errorf("agent %s: %s is not a key a harness agent sets (%s); a new one is added to agentKeys by hand, and a key that grants a permission never is (ADR-056, BUG-143)", name, k, strings.Join(agentKeys, ", "))
	}
	for _, banned := range []string{"dispatch prompt", "harness binary"} {
		if strings.Contains(strings.ToLower(fm["description"]), banned) {
			return fmt.Errorf("agent %s: description names %q, a mechanism that no longer exists", name, banned)
		}
	}
	// What the derivation guaranteed, now the author's: a subagent, never
	// a main agent.
	if fm["subagent"] != "true" || fm["mainAgent"] != "false" {
		return fmt.Errorf("agent %s: a harness agent sets subagent: true and mainAgent: false", name)
	}
	for _, t := range splitList(fm["tools"]) {
		if !slices.Contains(knownTools, t) {
			return fmt.Errorf("agent %s: tool %q is not one the harness knows (%s)", name, t, strings.Join(knownTools, ", "))
		}
	}
	return nil
}

// loadDocs reads every *.md directly under the named source directories as
// one kind, whose frontmatter may set only allowed. Missing directories are
// fine; the same name in two of them is not.
func loadDocs(src Source, kind string, allowed []string, dirs ...string) ([]Doc, error) {
	seen := map[string]string{}
	var docs []Doc
	for _, dir := range dirs {
		paths, err := fs.Glob(src.FS, dir+"/*.md")
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			raw, err := src.readRegular(p)
			if err != nil {
				return nil, err
			}
			shown := src.path(p)
			name := strings.TrimSuffix(path.Base(p), ".md")
			if err := validName(kind, shown, name); err != nil {
				return nil, err
			}
			if prev, dup := seen[name]; dup {
				return nil, fmt.Errorf("%s and %s both define %q", prev, shown, name)
			}
			seen[name] = shown
			desc, trigger, text, err := readDocFrontmatter(kind, shown, string(raw), allowed)
			if err != nil {
				return nil, err
			}
			if desc == "" {
				desc = firstLine(text)
			}
			docs = append(docs, Doc{Name: name, Description: desc, Trigger: trigger, Path: shown, Raw: raw})
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

func readDocFrontmatter(kind, shown, text string, allowed []string) (desc, trigger, body string, err error) {
	front, body, err := Split(text)
	if err != nil {
		if opensFrontmatter(text) {
			// Not "no frontmatter": a block that starts and cannot be
			// read would deploy as written with its keys unchecked.
			return "", "", "", fmt.Errorf("%s: frontmatter starts but cannot be read (the opening --- must be the very first line, alone on it, and a closing --- must follow); a command or rule has a readable frontmatter block or none", shown)
		}
		return "", "", text, nil
	}
	fm, err := parseStrict(front)
	if err != nil {
		return "", "", "", fmt.Errorf("%s: %w", shown, err)
	}
	if k := unlistedKey(front, allowed); k != "" {
		return "", "", "", fmt.Errorf("%s: %s is not a key a harness %s sets (%s); a new one is added by a D-row, and a key that grants a permission never is — the operator's permission mode decides (IN23, BUG-143)", shown, k, kind, strings.Join(allowed, ", "))
	}
	trigger = fm["trigger"]
	if kind == "rule" {
		if err := validateRuleTrigger(shown, fm); err != nil {
			return "", "", "", err
		}
	}
	return fm["description"], trigger, body, nil
}

func validateRuleTrigger(shown string, fm Frontmatter) error {
	trigger := fm["trigger"]
	switch trigger {
	case "", "always_on", "model_decision", "glob":
	default:
		return fmt.Errorf("%s: unknown rule trigger %q (supported: always_on, model_decision, glob)", shown, trigger)
	}
	globs, hasGlobs := fm["globs"]
	if hasGlobs && trigger != "glob" {
		return fmt.Errorf("%s: rule setting globs requires trigger: glob", shown)
	}
	if trigger == "glob" {
		if err := validateRuleGlobs(shown, globs); err != nil {
			return err
		}
	}
	if trigger == "model_decision" && strings.TrimSpace(fm["description"]) == "" {
		return fmt.Errorf("%s: rule with trigger: model_decision requires a non-empty description", shown)
	}
	return nil
}

func validateRuleGlobs(shown, globs string) error {
	if strings.TrimSpace(globs) == "" {
		return fmt.Errorf("%s: rule with trigger: glob requires a non-empty globs pattern", shown)
	}
	for _, pat := range strings.Split(globs, ",") {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			return fmt.Errorf("%s: rule with trigger: glob has an empty pattern segment in %q", shown, globs)
		}
		norm := strings.ReplaceAll(pat, "\\", "/")
		hasDrive := len(norm) >= 2 && unicode.IsLetter(rune(norm[0])) && norm[1] == ':'
		if strings.HasPrefix(norm, "/") || strings.HasPrefix(norm, "~/") || hasDrive || filepath.IsAbs(pat) {
			return fmt.Errorf("%s: rule glob %q must be repo-relative, not absolute", shown, pat)
		}
		for _, seg := range strings.Split(norm, "/") {
			if seg == ".." {
				return fmt.Errorf("%s: rule glob %q must not contain '..' path traversal", shown, pat)
			}
		}
	}
	return nil
}

// loadWorkflowScripts reads workflows/*.py files compiled into embedded
// installations and deployed alongside markdown workflow prompts.
func loadWorkflowScripts(src Source) ([]Doc, error) {
	paths, err := fs.Glob(src.FS, "workflows/*.py")
	if err != nil {
		return nil, err
	}
	var out []Doc
	for _, p := range paths {
		base := path.Base(p)
		shown := src.path(p)
		if SecretShaped(base) {
			return nil, fmt.Errorf("workflow %s: secret-shaped filename is forbidden", shown)
		}
		name := strings.TrimSuffix(base, ".py")
		if err := validName("workflow", shown, name); err != nil {
			return nil, err
		}
		raw, err := src.readRegular(p)
		if err != nil {
			return nil, err
		}
		out = append(out, Doc{
			Name:        name,
			Description: workflowScriptSummary(string(raw)),
			Path:        shown,
			Raw:         raw,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func workflowScriptSummary(src string) string {
	s := strings.TrimSpace(src)
	for _, q := range []string{`"""`, `'''`} {
		if strings.HasPrefix(s, q) {
			s = strings.TrimPrefix(s, q)
			if end := strings.Index(s, q); end >= 0 {
				s = s[:end]
			}
			return firstLine(s)
		}
	}
	s = strings.TrimLeft(s, "# ")
	return firstLine(s)
}
