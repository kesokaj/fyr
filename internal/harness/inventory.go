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
	Trigger     string // frontmatter trigger, "" when unset
	Path        string
	Raw         []byte
}

// Inventory is everything the repo holds in its four customization
// directories plus workflows/: adding a skill, agent, command, workflow script,
// or rule is adding a file, so any session that installs from this repo picks
// up new customizations automatically.
type Inventory struct {
	Skills          []Skill // skills/<name>/SKILL.md
	Agents          []Agent // agents/<name>/agent.md
	Commands        []Doc   // commands/<name>.md and workflows/<name>.md
	WorkflowScripts []Doc   // workflows/<name>.py
	Rules           []Doc   // rules/<name>.md
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
	for _, dir := range ManagedDirs {
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
	if inv.WorkflowScripts, err = loadWorkflowScripts(src); err != nil {
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
		s, ok, err := readSkill(src, e.Name())
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if first, dup := byName[s.Name]; dup {
			return nil, fmt.Errorf("two skills are named %q: skills/%s and skills/%s — one install destination, so one would silently overwrite the other", s.Name, first, e.Name())
		}
		byName[s.Name] = e.Name()
		skills = append(skills, s)
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
	ruleKeys    = []string{"description", "trigger", "globs"}
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
		a, err := readAgent(src, e.Name())
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(strings.TrimLeft(l, "# ")); l != "" {
			return l
		}
	}
	return ""
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

// descriptionIsMultiLine reports whether the frontmatter's description is a
// block (`|`, `>`) or carries continuation lines: an agent's description is
// one line.
func descriptionIsMultiLine(front string) bool {
	for _, e := range entries(front) {
		if e.key == "description" && (isBlockIndicator(e.val) || len(e.cont) > 0) {
			return true
		}
	}
	return false
}
