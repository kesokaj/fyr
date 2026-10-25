package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Skill is one directory under skills/ holding a SKILL.md.
type Skill struct {
	Name        string
	Description string
	Dir         string // skills/<dir> inside the Source
	SlashOnly   bool   // disable-model-invocation: the user types /name
}

// Agent is one Antigravity subagent, agents/<name>/agent.md, installed as
// written (ADR-062).
type Agent struct {
	Name        string
	Description string
	Path        string
	Front       Frontmatter
	Body        string
	Raw         []byte
}

// Doc is a plain markdown customization — a command or a rule. Frontmatter
// is optional; without a description the first line of text stands in.
type Doc struct {
	Name        string
	Description string
	Path        string
	Raw         []byte
}

// Inventory is everything the repo contributes. Each field is a directory:
// drop a file into it and the next install deploys it — no code changes.
type Inventory struct {
	Skills   []Skill // skills/<name>/SKILL.md
	Agents   []Agent // agents/<name>/agent.md
	Commands []Doc   // commands/<name>.md and workflows/<name>.md
	Rules    []Doc   // rules/<name>.md
}

// identLike reports whether name holds only the characters an identifier
// needs: `[A-Za-z0-9._-]`, with an alphanumeric first.
func identLike(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case i > 0 && (r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// validName is the parse boundary for a customization's name — a skill's or
// an agent's frontmatter `name:`, a command's or a rule's file name — the
// value `plan.go` joins into ~/.gemini/config/skills/<name>/…,
// ~/.gemini/config/agents/<name>/agent.md, workflows/<name>.md and
// rules/<name>.md. A name is ONE path segment: nothing that filepath.Join
// would carry out of the install root (`../x`, an absolute path), and no
// separator at all (`a/b` would nest a skill inside another's directory),
// and only the characters identLike allows: the same value is rendered
// verbatim into the consumer's AGENTS.md, so a backtick or a newline in it
// would inject prose into the global context (security review 2026-09-14;
// commands and rules since IN25). Refused with the file named, so an author
// sees it at `fyr install` time rather than finding a file written beside
// their home.
func validName(kind, file, name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("%s %s: name %q is not a path segment", kind, file, name)
	case strings.ContainsAny(name, `/\`), !filepath.IsLocal(name), !identLike(name):
		return fmt.Errorf("%s %s: name %q must be a single local path segment", kind, file, name)
	}
	return nil
}

// checkManagedTree refuses a symbolic link anywhere fyr reads the repo's
// own bytes: each managed directory, and every entry directly inside the flat
// ones. os.DirFS follows a directory link on ReadDir and Glob and a file link
// on ReadFile, so either deploys bytes from outside the repo into every
// consumer home — and for context/ into the AGENTS.md the CLI sends to the
// model (BUG-036). One check at the boundary, so no reader can forget it. It
// runs only for a tree on disk: the embedded files hold no link.
func checkManagedTree(src Source) error {
	if src.Dir == "" {
		return nil
	}
	for _, dir := range []string{"context", "agents", "commands", "rules", "workflows", "skills"} {
		p := src.path(dir)
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink — the repo ships regular directories only", p)
		}
		if !info.IsDir() || dir == "skills" {
			// skills/<name> may be a link: `npx skills add -g` leaves one and
			// os.DirFS follows it. A skill's own files are walked in plan.go,
			// and its SKILL.md is read with readRegular.
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s is a symlink — the repo ships regular files only", filepath.Join(p, e.Name()))
			}
		}
	}
	return nil
}

// LoadInventory reads every customization directory of the source.
func LoadInventory(src Source) (Inventory, error) {
	var inv Inventory
	if err := checkManagedTree(src); err != nil {
		return inv, err
	}
	var err error
	if inv.Skills, err = loadSkills(src); err != nil {
		return inv, err
	}
	if inv.Agents, err = loadAgents(src); err != nil {
		return inv, err
	}
	if inv.Commands, err = loadDocs(src, "command", commandKeys, "commands", "workflows"); err != nil {
		return inv, err
	}
	if inv.Rules, err = loadDocs(src, "rule", ruleKeys, "rules"); err != nil {
		return inv, err
	}
	return inv, nil
}

// loadSkills reads every skills/<name>/SKILL.md. A directory without a
// SKILL.md is ignored; one with a SKILL.md but no description is an error,
// because the description is the only thing the CLI shows the model.
func loadSkills(src Source) ([]Skill, error) {
	entries, err := fs.ReadDir(src.FS, "skills")
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", src.path("skills"), err)
	}
	var skills []Skill
	// Two skills of the same name install to one destination, so the second
	// silently overwrites the first and `list` shows a roster that is not
	// what was deployed. A duplicate means something is wrong in skills/ —
	// an interrupted copy, a directory whose frontmatter name belongs to
	// another skill — and is refused rather than resolved by walk order
	// (code review 2026-09-16).
	byName := map[string]string{}
	for _, e := range entries {
		dir := path.Join("skills", e.Name())
		// fs.Stat follows a link, so a skills/<name> link to a directory is a
		// skill and a dangling one is refused out loud.
		info, err := fs.Stat(src.FS, dir)
		if err != nil {
			return nil, fmt.Errorf("resolving skill %s: %w", e.Name(), err)
		}
		if !info.IsDir() {
			continue
		}
		data, err := src.readRegular(path.Join(dir, "SKILL.md"))
		if err != nil {
			// No SKILL.md means the directory is not a skill. Any other
			// error, a symlinked SKILL.md above all, is refused out loud:
			// plan.go's walk only visits skills this loader returned, so a
			// silent skip would drop the refusal altogether (BUG-036).
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		front, _, err := Split(string(data))
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", e.Name(), err)
		}
		fm, err := parseStrict(front)
		if err != nil {
			return nil, fmt.Errorf("skill %s: %w", e.Name(), err)
		}
		name := fm["name"]
		if name == "" {
			name = e.Name()
		}
		if err := validName("skill", e.Name()+"/SKILL.md", name); err != nil {
			return nil, err
		}
		if fm["description"] == "" {
			return nil, fmt.Errorf("skill %s: SKILL.md has no description", e.Name())
		}
		if k := unlistedKey(front, skillKeys); k != "" {
			return nil, fmt.Errorf("skill %s: %s is not a key a harness skill sets (%s); a new one is added to skillKeys by a D-row, and a key that grants a permission never is — the operator's permission mode decides what runs unprompted (IN23, BUG-143)", name, k, strings.Join(skillKeys, ", "))
		}
		if first, dup := byName[name]; dup {
			return nil, fmt.Errorf("two skills are named %q: skills/%s and skills/%s — one install destination, so one would silently overwrite the other", name, first, e.Name())
		}
		byName[name] = e.Name()
		skills = append(skills, Skill{
			Name:        name,
			Description: fm["description"],
			Dir:         dir,
			SlashOnly:   fm["disable-model-invocation"] == "true",
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// agentKeys is every frontmatter key a harness agent may set: the keys the
// derivation wrote until ADR-062. An agent file
// ships byte for byte, so a key the CLI honours outside this list (hooks,
// permissionMode, mcpServers, and whatever a vendor adds next) would act on
// every place unreviewed: an allow-list refuses the next one by default
// (ADR-056). disallowedTools is not on it: a restriction the CLI drops would
// read as one and hold nowhere (ADR-056a). The old format's own keys
// (maxTurns, effort, skills, color) are refused with it: a file carrying one
// was written for another CLI.
var agentKeys = []string{"name", "description", "subagent", "mainAgent", "model", "tools"}

// skillKeys, commandKeys and ruleKeys are the same allow list for the other
// kinds (IN23, plan audit-fix D7). A skill's, command's or rule's frontmatter
// is a permission surface too: allowed-tools lets every named tool run
// unprompted while a skill is active, and a vendor key nobody listed
// (permissionMode, mcpServers, a respelt Allowed-Tools) would act on every
// place unreviewed, so the next one is refused by default (BUG-143). Each
// list is the keys the tree carries, plus disable-model-invocation, which the
// loader reads (SlashOnly). A vendored skill is never edited, so one that
// needs a new key grows its list by a D-row; a permission key never joins.
var (
	skillKeys   = []string{"name", "description", "license", "metadata", "argument-hint", "disable-model-invocation"}
	commandKeys = []string{"description"}
	ruleKeys    = []string{"description", "trigger"}
)

// knownTools is every tool name an agent may list. An unknown name fails the
// install rather than passing through: Antigravity hangs a subagent whose
// tools list names something it does not recognise.
var knownTools = []string{"view_file", "grep_search", "find_by_name", "run_command", "write_to_file", "replace_file_content"}

// loadAgents reads every agents/<name>/agent.md, in Antigravity's format. A
// flat agents/<name>.md, the format before ADR-062, is refused, not skipped:
// skipped, the agent would vanish from every place without a word (F6).
func loadAgents(src Source) ([]Agent, error) {
	entries, err := fs.ReadDir(src.FS, "agents")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var agents []Agent
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		if !e.IsDir() {
			return nil, fmt.Errorf("%s: an agent is agents/<name>/agent.md in Antigravity's format; a flat agents/<name>.md is the format before ADR-062", src.path(path.Join("agents", e.Name())))
		}
		p := path.Join("agents", e.Name(), "agent.md")
		shown := e.Name() + "/agent.md"
		raw, err := src.readRegular(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s has no agent.md", src.path(path.Join("agents", e.Name())))
		}
		if err != nil {
			return nil, err
		}
		front, body, err := Split(string(raw))
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", shown, err)
		}
		fm, err := parseStrict(front)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", shown, err)
		}
		name := fm["name"]
		if name == "" {
			name = e.Name()
		}
		if err := validName("agent", shown, name); err != nil {
			return nil, err
		}
		if fm["description"] == "" {
			return nil, fmt.Errorf("agent %s: no description", name)
		}
		// One line, dispatch-worthy, and free of the two phrases that
		// described a mechanism that no longer exists (AGENTS.md § Rules;
		// until 2026-10-01 only inspection enforced this).
		if descriptionIsMultiLine(front) {
			return nil, fmt.Errorf("agent %s: description must be one line", name)
		}
		if k := unlistedKey(front, agentKeys); k != "" {
			return nil, fmt.Errorf("agent %s: %s is not a key a harness agent sets (%s); a new one is added to agentKeys by hand, and a key that grants a permission never is (ADR-056, BUG-143)", name, k, strings.Join(agentKeys, ", "))
		}
		for _, banned := range []string{"dispatch prompt", "harness binary"} {
			if strings.Contains(strings.ToLower(fm["description"]), banned) {
				return nil, fmt.Errorf("agent %s: description names %q, a mechanism that no longer exists", name, banned)
			}
		}
		// What the derivation guaranteed, now the author's: a subagent, never
		// a main agent.
		if fm["subagent"] != "true" || fm["mainAgent"] != "false" {
			return nil, fmt.Errorf("agent %s: a harness agent sets subagent: true and mainAgent: false", name)
		}
		for _, t := range splitList(fm["tools"]) {
			if !slices.Contains(knownTools, t) {
				return nil, fmt.Errorf("agent %s: tool %q is not one the harness knows (%s)", name, t, strings.Join(knownTools, ", "))
			}
		}

		agents = append(agents, Agent{Name: name, Description: fm["description"], Path: src.path(p), Front: fm, Body: body, Raw: raw})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
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
			raw, err := fs.ReadFile(src.FS, p)
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
			desc := ""
			text := string(raw)
			if front, body, err := Split(text); err == nil {
				fm, err := parseStrict(front)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", shown, err)
				}
				if k := unlistedKey(front, allowed); k != "" {
					return nil, fmt.Errorf("%s: %s is not a key a harness %s sets (%s); a new one is added by a D-row, and a key that grants a permission never is — the operator's permission mode decides (IN23, BUG-143)", shown, k, kind, strings.Join(allowed, ", "))
				}
				desc = fm["description"]
				text = body
			} else if opensFrontmatter(text) {
				// Not "no frontmatter": a block that starts and cannot be
				// read would deploy as written with its keys unchecked.
				return nil, fmt.Errorf("%s: frontmatter starts but cannot be read (the opening --- must be the very first line, alone on it, and a closing --- must follow); a command or rule has a readable frontmatter block or none", shown)
			}
			if desc == "" {
				desc = firstLine(text)
			}
			docs = append(docs, Doc{Name: name, Description: desc, Path: shown, Raw: raw})
		}
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(l, "# ")); l != "" {
			return l
		}
	}
	return ""
}

// RenderInventory produces the markdown section appended to the deployed
// context so the model knows what is installed without listing directories.
func RenderInventory(inv Inventory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Installed skills, agents, commands, and rules\n\n")
	fmt.Fprintf(&b, "Generated by `fyr` from the harness repo; `fyr list` prints the same.%s\n\n", slashNote(inv))
	fmt.Fprintf(&b, "### Skills (%d)\n\n| Skill | Invoke | Use for |\n|---|---|---|\n", len(inv.Skills))
	for _, s := range inv.Skills {
		invoke := "auto"
		if s.SlashOnly {
			invoke = "`/" + s.Name + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.Name, invoke, cell(s.Description, 140))
	}
	fmt.Fprintf(&b, "\n### Subagents (%d)\n\n| Agent | Model | Does |\n|---|---|---|\n", len(inv.Agents))
	for _, a := range inv.Agents {
		// The model key as written; none means the CLI's default.
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", a.Name, a.Front["model"], cell(a.Description, 160))
	}
	if len(inv.Commands) > 0 {
		fmt.Fprintf(&b, "\n### Commands (%d)\n\n| Command | Does |\n|---|---|\n", len(inv.Commands))
		for _, c := range inv.Commands {
			fmt.Fprintf(&b, "| `/%s` | %s |\n", c.Name, cell(c.Description, 160))
		}
	}
	if len(inv.Rules) > 0 {
		fmt.Fprintf(&b, "\n### Rules (%d)\n\n| Rule | About |\n|---|---|\n", len(inv.Rules))
		for _, r := range inv.Rules {
			fmt.Fprintf(&b, "| `%s` | %s |\n", r.Name, cell(r.Description, 160))
		}
	}
	return b.String()
}

// renderInventoryCompact is the form embedded in the deployed context: names
// only. The CLI injects every skill's description at session start anyway,
// so repeating descriptions there would spend tokens on what is already
// present; `fyr list` prints the full table.
func renderInventoryCompact(inv Inventory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Installed\n\nGenerated by `fyr`; `fyr list` prints the descriptions.%s\n\n", slashNote(inv))
	var skills []string
	for _, s := range inv.Skills {
		if s.SlashOnly {
			skills = append(skills, "`/"+s.Name+"`")
		} else {
			skills = append(skills, "`"+s.Name+"`")
		}
	}
	fmt.Fprintf(&b, "- Skills (%d): %s\n", len(inv.Skills), strings.Join(skills, ", "))
	var agents []string
	for _, a := range inv.Agents {
		if model := a.Front["model"]; model != "" {
			agents = append(agents, "`"+a.Name+"` ("+model+")")
		} else {
			agents = append(agents, "`"+a.Name+"`")
		}
	}
	fmt.Fprintf(&b, "- Subagents (%d): %s\n", len(inv.Agents), strings.Join(agents, ", "))
	if len(inv.Commands) > 0 {
		var names []string
		for _, c := range inv.Commands {
			names = append(names, "`/"+c.Name+"`")
		}
		fmt.Fprintf(&b, "- Commands (%d): %s\n", len(inv.Commands), strings.Join(names, ", "))
	}
	if len(inv.Rules) > 0 {
		var names []string
		for _, r := range inv.Rules {
			names = append(names, "`"+r.Name+"`")
		}
		fmt.Fprintf(&b, "- Rules (%d): %s\n", len(inv.Rules), strings.Join(names, ", "))
	}
	return b.String()
}

// opensFrontmatter reports whether the first non-blank line looks like a
// frontmatter fence to any reader more lenient than Split: `---` after
// byte-order marks, zero-width characters or blank lines, or followed by
// spaces of any kind, a tag or a comment. Split wants the bare line first; a
// file that is not that, yet looks like this, is refused rather than read as
// "no frontmatter". Four dashes or more are a markdown rule, not a fence.
func opensFrontmatter(text string) bool {
	invisible := func(r rune) bool {
		return unicode.IsSpace(r) || r == '\ufeff' || r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\u2060'
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimFunc(line, invisible)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "---") && !strings.HasPrefix(line, "----")
	}
	return false
}

// slashNote explains the `/name` marking, but only when some skill carries
// `disable-model-invocation`; with none, the sentence would describe nothing.
func slashNote(inv Inventory) string {
	for _, s := range inv.Skills {
		if s.SlashOnly {
			return " Skills marked `/name` are typed by the user; the rest load when their description matches the task."
		}
	}
	return " Every skill loads when its description matches the task and can also be typed as `/name`."
}

// cell shortens text for a table cell and escapes the column separator.
func cell(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	// By runes: a byte cut can split a multi-byte character (security audit
	// 2026-09-18).
	if r := []rune(s); len(r) > max {
		head := string(r[:max])
		cut := strings.LastIndex(head, " ")
		if cut < len(head)/2 {
			cut = len(head)
		}
		s = head[:cut] + "…"
	}
	return strings.ReplaceAll(s, "|", `\|`)
}

// descriptionIsMultiLine reports whether the frontmatter's description is a
// block (`|`, `>`) or carries continuation lines: an agent's description is
// one line.
func descriptionIsMultiLine(front string) bool {
	for _, e := range entries(front) {
		if e.key != "description" {
			continue
		}
		return isBlockIndicator(e.val) || len(e.cont) > 0
	}
	return false
}
