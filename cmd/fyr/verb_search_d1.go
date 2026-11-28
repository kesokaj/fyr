package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

func hookVerb() verb {
	return verb{
		flags:       registerJSONFlag,
		writesState: func(*runtimeCtx) bool { return true },
		positional:  true,
		run:         runHookVerb,
	}
}

func runHookVerb(c *runtimeCtx) error {
	args := c.fs.Args()
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return usageErr(c.out, "hook", "")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	o := buildSearchOptions(c, wd, search.Options{Hook: args[0]})
	if err := search.Validate(o); err != nil {
		return finishSearchErr(c.out, err)
	}
	if !jsonOptsOf(c).jsonOut {
		return finishSearchErr(c.out, search.Search(o, c.out))
	}
	var buf bytes.Buffer
	if err := search.Search(o, &buf); err != nil {
		return finishSearchErr(c.out, err)
	}
	trimmed := bytes.TrimSpace(buf.Bytes())
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed) {
		_, err := c.out.Write(buf.Bytes())
		return err
	}
	return writeVerbJSON(c.out, dirOutputJSON{OK: true, Dir: wd, Output: buf.String()})
}

// astOpts holds parsed flags for `fyr ast`.
type astOpts struct {
	sliceQuery, contractsTarget, skeletonPath, pathFrom, pathTo, affectedTarget, sliceSession string
	sliceHops, sliceBudget                                                                    int
	repoMap, includeDocs, global, jsonOut                                                     bool
}

func astOptsOf(c *runtimeCtx) *astOpts {
	if o, ok := c.opts.(*astOpts); ok {
		return o
	}
	o := &astOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func astVerb() verb {
	return verb{
		flags:       registerASTFlags,
		writesState: func(*runtimeCtx) bool { return true },
		positional:  true,
		run:         runASTVerb,
	}
}

func registerASTFlags(c *runtimeCtx) {
	o := &astOpts{}
	c.opts = o
	c.fs.StringVar(&o.sliceQuery, "slice", "", "0/1/2-hop context capsule for `QUERY` ranked by PageRank")
	c.fs.StringVar(&o.contractsTarget, "contracts", "", "deterministic AST signatures, linked SPEC-* contracts and ADR guardrails for `TARGET`")
	c.fs.StringVar(&o.skeletonPath, "skeleton", "", "declaration-only start-end signature lines for `FILE`")
	c.fs.StringVar(&o.pathFrom, "path", "", "shortest call/type/interface path from `SYM` (<= 6 hops)")
	c.fs.StringVar(&o.pathTo, "to", "", "path: target symbol `SYM` (or give as positional argument)")
	c.fs.StringVar(&o.affectedTarget, "affected", "", "reverse-BFS blast radius (<= 2 hops) for `TARGET` path or symbol")
	c.fs.BoolVar(&o.repoMap, "map", false, "top-10 God Nodes by PageRank, package deps and cycle check")
	c.fs.IntVar(&o.sliceHops, "hops", 0, "slice: maximum graph hops `N`, 0-6 (default 2)")
	c.fs.IntVar(&o.sliceBudget, "budget", 0, "slice, contracts: token budget `N` estimated as chars/4 (default 2000; 1500 for --contracts)")
	c.fs.StringVar(&o.sliceSession, "session", "", "slice: conversation `ID` whose read files get the 50x boost")
	c.fs.BoolVar(&o.includeDocs, "include-docs", false, "include Markdown docs and headings in AST maps and slices")
	c.fs.BoolVar(&o.global, "global", false, "also read global records and registered projects from the brain store")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

func hasASTMode(ao *astOpts) bool {
	return ao.sliceQuery != "" || ao.contractsTarget != "" || ao.skeletonPath != "" ||
		ao.pathFrom != "" || ao.affectedTarget != "" || ao.repoMap
}

func runASTVerb(c *runtimeCtx) error {
	ao := astOptsOf(c)
	if !hasASTMode(ao) {
		return usageErr(c.out, "ast", "fyr ast: give --slice, --contracts, --skeleton, --path, --affected or --map")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	o := buildSearchOptions(c, wd, search.Options{
		Words:       c.fs.Args(),
		Global:      ao.global,
		Slice:       ao.sliceQuery,
		Hops:        ao.sliceHops,
		Budget:      ao.sliceBudget,
		Session:     ao.sliceSession,
		Contracts:   ao.contractsTarget,
		Skeleton:    ao.skeletonPath,
		Path:        ao.pathFrom,
		To:          ao.pathTo,
		Affected:    ao.affectedTarget,
		Map:         ao.repoMap,
		IncludeDocs: ao.includeDocs,
	})
	if err = search.Validate(o); err == nil {
		err = c.execSearchWithJSON(o, ao.jsonOut)
	}
	return finishSearchErr(c.out, err)
}

// vetOpts holds parsed flags for `fyr vet`.
type vetOpts struct {
	global, jsonOut bool
}

func vetOptsOf(c *runtimeCtx) *vetOpts {
	if o, ok := c.opts.(*vetOpts); ok {
		return o
	}
	o := &vetOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func vetVerb() verb {
	return verb{
		flags:       registerVetFlags,
		writesState: func(*runtimeCtx) bool { return true },
		positional:  true,
		run:         runVetVerb,
	}
}

func registerVetFlags(c *runtimeCtx) {
	o := &vetOpts{}
	c.opts = o
	c.fs.BoolVar(&o.global, "global", false, "also read global records and registered projects from the brain store")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

func runVetVerb(c *runtimeCtx) error {
	prompt := strings.TrimSpace(strings.Join(c.fs.Args(), " "))
	if prompt == "" {
		return usageErr(c.out, "vet", "")
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	vo := vetOptsOf(c)
	o := buildSearchOptions(c, wd, search.Options{
		Vet:    prompt,
		Global: vo.global,
	})
	if err = search.Validate(o); err == nil {
		err = c.execSearchWithJSON(o, vo.jsonOut)
	}
	return finishSearchErr(c.out, err)
}

func finishSearchErr(out io.Writer, err error) error {
	if search.IsUsage(err) {
		fmt.Fprintln(out, "fyr", err)
		return exitCode(2)
	}
	return err
}

// recordHits prints the brain store's hits before the file hits (ADR-063):
// for a words search the ranked records, for --refs the records naming the
// ID or path, for --open the open records (D30). Nothing when there are
// none, and a one-line note when the records could not be searched (a
// trail.jsonl that does not parse), so the file search still answers.
func recordHits(c *runtimeCtx, o search.Options) {
	hits, head, err := recordHitsData(o)
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("records: not searched: "+err.Error()))
		return
	}
	if len(hits) == 0 {
		return
	}
	fmt.Fprintln(c.out, search.Printable(head))
	for _, h := range hits {
		fmt.Fprintln(c.out, brain.FormatHit(h))
		if h.Snippet != "" {
			fmt.Fprintf(c.out, "    %s\n", h.Snippet)
		}
	}
}

func recordHitsData(o search.Options) ([]brain.Hit, string, error) {
	limit := o.Limit
	if limit == 0 {
		limit = 10
	}
	bo := brain.Options{Home: o.Home, Dir: o.Dir, Global: o.Global}
	switch {
	case o.Refs != "":
		hits, err := brain.Refs(bo, o.Refs, o.Kind, limit)
		return hits, fmt.Sprintf("records: %d naming %s", len(hits), o.Refs), err
	case o.Open:
		hits, err := brain.OpenRecords(bo, o.Kind, limit)
		return hits, fmt.Sprintf("records: %d open", len(hits)), err
	default:
		hits, err := brain.Search(bo, o.Words, o.Kind, limit)
		return hits, fmt.Sprintf("records: %d hits for %q", len(hits), strings.Join(o.Words, " ")), err
	}
}

// now is the clock `index --register` dates the marker with; a test replaces it.
var now = time.Now

// runRegister is `fyr index --register`: the operator's consent that this
// checkout reaches the cross-project consumers (ADR-068). It skips ephemeral
// worktree/smoke/sandbox/scratch paths (ADR-532), writes the marker in the
// checkout's git common directory, then indexes as a plain `fyr index`, whose
// prepare registers the checkout in the global search index now that the marker
// exists. The marker comes first, so a failed index leaves a valid one, and the
// error says so.
func runRegister(c *runtimeCtx, o search.Options) error {
	if o.Global {
		fmt.Fprintln(c.out, "fyr index: --register and --global do not go together")
		return exitCode(2)
	}
	main, top, err := trail.Roots(o.Dir)
	if errors.Is(err, trail.ErrNotCheckout) {
		fmt.Fprintln(c.out, "fyr index: --register needs a git checkout")
		return exitCode(2)
	}
	if err != nil {
		return fmt.Errorf("index --register: %w", err)
	}
	jsonOut := indexOptsOf(c).jsonOut
	var buf bytes.Buffer
	out := c.out
	if jsonOut {
		out = &buf
	}
	if eph := ephemeralRegisterTarget(o.Dir, top, main); eph != "" {
		fmt.Fprintln(out, search.Printable("note: skipping registration for ephemeral path "+eph))
		if jsonOut {
			return writeIndexJSON(c, o.Dir, buf.String())
		}
		return nil
	}
	if _, err = trail.Register(filepath.Join(c.home, harness.StateDir), o.Dir, now()); err != nil {
		return fmt.Errorf("index --register: %w", err)
	}
	if err := search.Index(o, out); err != nil {
		return fmt.Errorf("registered %s, but the index failed: %w", main, err)
	}
	fmt.Fprintf(out, "registered %s\n", main)
	// The marker is consent, not yet a reader: say so when nothing takes it.
	root := search.RegistryRoot(o.Dir)
	if ok, err := search.HasScopeRecords(c.home, root); err == nil && !ok {
		fmt.Fprintln(out, search.Printable("note: "+root+" has no .agents/trail.jsonl, so no cross-project read takes it until it has records (fyr record import, or the first fyr record add)"))
	}
	if jsonOut {
		return writeIndexJSON(c, o.Dir, buf.String())
	}
	return nil
}

func ephemeralRegisterTarget(dir, top, main string) string {
	for _, p := range []string{top, main, dir} {
		if auto.IsEphemeralPath(p) {
			return p
		}
	}
	return ""
}

func makeSkillCatalog() func(home string) []search.SkillMeta {
	return LoadSkillCatalog
}

// LoadSkillCatalog reads installed SKILL.md metadata under
// <home>/.gemini/config/skills/<name>/SKILL.md. Returns nil when home is empty
// or no skills are installed (zero phantom fallback).
func LoadSkillCatalog(home string) []search.SkillMeta {
	if home == "" {
		return nil
	}
	skillsDir := filepath.Join(home, ".gemini", "config", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []search.SkillMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(skillsDir, e.Name(), "SKILL.md")
		if meta, ok := parseSkillMetaFile(e.Name(), p); ok {
			out = append(out, meta)
		}
	}
	return out
}

func parseSkillMetaFile(name, path string) (search.SkillMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return search.SkillMeta{}, false
	}
	raw := string(data)
	desc := extractSkillDescription(raw)
	digest := extractActionableSkillDigest(raw)
	if digest == "" {
		digest = desc
	}
	if desc == "" && digest == "" {
		return search.SkillMeta{}, false
	}
	desc = truncateUTF8(desc, 600)
	digest = truncateUTF8(digest, 600)
	return search.SkillMeta{
		Name:        name,
		Description: desc,
		Path:        "~/.gemini/config/skills/" + name + "/SKILL.md",
		Digest:      digest,
	}, true
}

func isYAMLBlockHeader(s string) bool {
	if s == "" || (s[0] != '|' && s[0] != '>') {
		return false
	}
	for _, r := range s[1:] {
		if r != '+' && r != '-' && (r < '1' || r > '9') {
			return false
		}
	}
	return true
}

func unquoteYAMLValue(s string) string {
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func skillFrontmatterLines(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return nil
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return lines[1:i]
		}
	}
	return lines[1:]
}

func collectSkillDescLines(fm []string) ([]string, bool) {
	var descLines []string
	inDesc, isBlock := false, false
	for _, line := range fm {
		if rest, ok := strings.CutPrefix(line, "description:"); ok {
			inDesc = true
			rest = strings.TrimSpace(rest)
			isBlock = isYAMLBlockHeader(rest)
			if !isBlock && rest != "" {
				descLines = append(descLines, rest)
			}
			continue
		}
		if !inDesc || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			descLines = append(descLines, strings.TrimSpace(line))
			continue
		}
		break
	}
	return descLines, isBlock
}

func extractSkillDescription(text string) string {
	fm := skillFrontmatterLines(text)
	if len(fm) == 0 {
		return ""
	}
	descLines, isBlock := collectSkillDescLines(fm)
	joined := strings.Join(descLines, " ")
	if !isBlock {
		joined = unquoteYAMLValue(joined)
	}
	return joined
}

func skillBodyStartLine(lines []string) int {
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i + 1
		}
	}
	return 0
}

func isSkillDigestSkippable(t string) bool {
	return t == "" || strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "<!--")
}

func extractActionableSkillDigest(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var parts []string
	inFence, total := false, 0
	for i := skillBodyStartLine(lines); i < len(lines) && total < 580; i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || isSkillDigestSkippable(t) {
			continue
		}
		if len(t) > 180 {
			t = truncateUTF8(t, 180) + "..."
		}
		parts = append(parts, t)
		total += len(t) + 3
		if len(parts) >= 6 {
			break
		}
	}
	return truncateUTF8(strings.Join(parts, " | "), 600)
}

func truncateUTF8(s string, maxBytes int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxBytes {
		return s
	}
	i := maxBytes
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return strings.TrimSpace(s[:i])
}
