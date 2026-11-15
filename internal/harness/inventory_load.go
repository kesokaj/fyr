package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"
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
