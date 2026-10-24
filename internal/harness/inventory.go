package harness

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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

// Agent is one Claude Code subagent definition under agents/.
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
	Agents   []Agent // agents/<name>.md
	Commands []Doc   // commands/<name>.md and workflows/<name>.md
	Rules    []Doc   // rules/<name>.md
}

// validName is the parse boundary for a frontmatter `name:` — the value
// `plan.go` joins into ~/.gemini/config/skills/<name>/… and
// ~/.gemini/config/agents/<name>/agent.md. A name is ONE path segment: nothing that filepath.Join
// would carry out of the install root (`../x`, an absolute path), and no
// separator at all (`a/b` would nest a skill inside another's directory),
// and only the characters an identifier needs (`[A-Za-z0-9._-]`, leading
// alphanumeric): the same value is rendered verbatim into the consumer's
// AGENTS.md, so a backtick or a newline in it would inject prose into the
// global context (security review 2026-09-14).
// Refused with the file named, so an author sees it at `sh-agent install`
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

// time rather than finding a file written beside their home.
func validName(kind, file, name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("%s %s: name %q is not a path segment", kind, file, name)
	case strings.ContainsAny(name, `/\`), !filepath.IsLocal(name), !identLike(name):
		return fmt.Errorf("%s %s: name %q must be a single local path segment", kind, file, name)
	}
	return nil
}

// checkManagedTree refuses a symbolic link anywhere sh-agent reads the repo's
// own bytes: each managed directory, and every entry directly inside the flat
// ones. os.DirFS follows a directory link on ReadDir and Glob and a file link
// on ReadFile, so either deploys bytes from outside the repo into every
// consumer home — and for context/ into the AGENTS.md both CLIs send to the
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
	if inv.Skills, err = LoadSkills(src); err != nil {
		return inv, err
	}
	if inv.Agents, err = LoadAgents(src); err != nil {
		return inv, err
	}
	if inv.Commands, err = loadDocs(src, "commands", "workflows"); err != nil {
		return inv, err
	}
	if inv.Rules, err = loadDocs(src, "rules"); err != nil {
		return inv, err
	}
	return inv, nil
}

// LoadSkills reads every skills/<name>/SKILL.md. A directory without a
// SKILL.md is ignored; one with a SKILL.md but no description is an error,
// because the description is the only thing either CLI shows the model.
func LoadSkills(src Source) ([]Skill, error) {
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
		fm, err := ParseStrict(front)
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
		// A skill's frontmatter is a permission surface: allowed-tools lets every
		// named tool run unprompted while the skill is active, so the operator's
		// permission mode decides, never a skill (BUG-143).
		if k := permissionKey(front, "allowed-tools", "hooks"); k != "" {
			return nil, fmt.Errorf("skill %s: %s is never set in a harness skill; the operator's permission mode decides what runs unprompted (BUG-143)", name, k)
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

// agentKeys is every frontmatter key a harness agent may set. Claude Code
// ships an agent file byte for byte, so a key it honours outside this list
// (hooks, permissionMode, mcpServers, memory, initialPrompt, and whatever a
// vendor adds next) would act on every place unreviewed: an allow-list
// refuses the next one by default (ADR-056). disallowedTools is not on it:
// the Antigravity agent DeriveAgy writes drops it, so the restriction would
// hold on one CLI only (ADR-056a).
var agentKeys = []string{"name", "description", "tools", "model", "maxTurns", "skills", "effort", "color"}

// LoadAgents reads every agents/*.md in Claude Code's subagent format.
func LoadAgents(src Source) ([]Agent, error) {
	paths, err := fs.Glob(src.FS, "agents/*.md")
	if err != nil {
		return nil, err
	}
	var agents []Agent
	for _, p := range paths {
		raw, err := fs.ReadFile(src.FS, p)
		if err != nil {
			return nil, err
		}
		front, body, err := Split(string(raw))
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", path.Base(p), err)
		}
		fm, err := ParseStrict(front)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", path.Base(p), err)
		}
		name := fm["name"]
		if name == "" {
			name = strings.TrimSuffix(path.Base(p), ".md")
		}
		if err := validName("agent", path.Base(p), name); err != nil {
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
		agents = append(agents, Agent{Name: name, Description: fm["description"], Path: src.path(p), Front: fm, Body: body, Raw: raw})
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
}

// loadDocs reads every *.md directly under the named source directories.
// Missing directories are fine; the same name in two of them is not.
func loadDocs(src Source, dirs ...string) ([]Doc, error) {
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
			if prev, dup := seen[name]; dup {
				return nil, fmt.Errorf("%s and %s both define %q", prev, shown, name)
			}
			seen[name] = shown
			desc := ""
			text := string(raw)
			if front, body, err := Split(text); err == nil {
				fm, err := ParseStrict(front)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", shown, err)
				}
				if k := permissionKey(front, "allowed-tools", "hooks"); k != "" {
					return nil, fmt.Errorf("%s: %s is never set in a harness command or rule; the operator's permission mode decides (BUG-143)", shown, k)
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
	fmt.Fprintf(&b, "Generated by `sh-agent` from the harness repo; `sh-agent list` prints the same.%s\n\n", slashNote(inv))
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
		model := a.Front["model"]
		if model == "" {
			model = "inherit"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", a.Name, model, cell(a.Description, 160))
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

// RenderInventoryCompact is the form embedded in the deployed context: names
// only. Both CLIs inject every skill's description at session start anyway,
// so repeating descriptions there would spend tokens on what is already
// present; `sh-agent list` prints the full table.
func RenderInventoryCompact(inv Inventory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Installed\n\nGenerated by `sh-agent`; `sh-agent list` prints the descriptions.%s\n\n", slashNote(inv))
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
		model := a.Front["model"]
		if model == "" {
			model = "inherit"
		}
		agents = append(agents, "`"+a.Name+"` ("+model+")")
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
