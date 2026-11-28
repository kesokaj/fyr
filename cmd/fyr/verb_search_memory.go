package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// memoryOpts holds parsed flags for `fyr memory`.
type memoryOpts struct {
	memType, memName, memDesc, memBodyFile string
	global, includeLow, jsonIn, jsonOut    bool
	limit                                  int
}

func memoryOptsOf(c *runtimeCtx) *memoryOpts {
	if o, ok := c.opts.(*memoryOpts); ok {
		return o
	}
	o := &memoryOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func memoryVerb() verb {
	return verb{
		flags:       registerMemoryFlags,
		writesState: func(c *runtimeCtx) bool { return c.fs.Arg(0) == "add" },
		positional:  true,
		run:         runMemory,
	}
}

func registerMemoryFlags(c *runtimeCtx) {
	o := &memoryOpts{}
	c.opts = o
	c.fs.StringVar(&o.memType, "type", "", "add: the memory's type `T`: semantic, procedural or episodic")
	c.fs.StringVar(&o.memName, "name", "", "add: the memory's slug, `SLUG`")
	c.fs.StringVar(&o.memDesc, "description", "", "add: the memory in one line, `TEXT`")
	c.fs.StringVar(&o.memBodyFile, "body-file", "", "add: read the body from `FILE` instead of stdin")
	c.fs.BoolVar(&o.global, "global", false, "shared global memory scope across projects")
	c.fs.BoolVar(&o.includeLow, "include-low", false, "show: include cards with validity < 0.40")
	c.fs.BoolVar(&o.jsonIn, "json-in", false, "add: read JSON payload from stdin")
	c.fs.BoolVar(&o.jsonOut, "json", false, "show: print the memory index as JSON")
	c.fs.IntVar(&o.limit, "limit", 0, "show: print at most `N` memory cards")
	c.fs.String("provenance", "operator", "add: the memory's provenance `P`: operator or derived")
}

// runMemory needs no harness tree (R10).
func runMemory(c *runtimeCtx) error {
	mo := memoryOptsOf(c)
	given := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	args := c.fs.Args()
	if len(args) != 1 || !slices.Contains(memorySubverbs, args[0]) {
		return usageErr(c.out, "memory", "")
	}
	if err := validateMemoryFlags(args[0], mo, given, c.out); err != nil {
		return err
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	o := brain.Options{Home: c.home, Dir: wd, Global: mo.global}
	if args[0] == "show" {
		showOpts := brain.MemoryShowOptions{IncludeLow: mo.includeLow, Limit: mo.limit}
		if mo.jsonOut {
			text, serr := brain.MemoryShowJSONWithOptions(o, showOpts)
			return memoryFinish(serr, text, c.out)
		}
		text, serr := brain.MemoryShowWithOptions(o, showOpts)
		return memoryFinish(serr, text, c.out)
	}
	return c.runMemoryAdd(o, given)
}

func validateMemoryFlags(sub string, mo *memoryOpts, given map[string]bool, out io.Writer) error {
	if sub == "show" {
		if given["type"] || given["name"] || given["description"] || given["provenance"] || given["body-file"] || given["json-in"] {
			fmt.Fprintln(out, "fyr memory: show takes no --type, --name, --description, --body-file, --json-in or --provenance")
			return exitCode(2)
		}
		if mo.limit < 0 {
			fmt.Fprintln(out, "fyr memory: --limit must be >= 0")
			return exitCode(2)
		}
		return nil
	}
	switch {
	case given["include-low"]:
		fmt.Fprintln(out, "fyr memory: add takes no --include-low")
	case given["limit"]:
		fmt.Fprintln(out, "fyr memory: add takes no --limit")
	case given["json"]:
		fmt.Fprintln(out, "fyr memory: add takes no --json")
	default:
		return nil
	}
	return exitCode(2)
}

func (c *runtimeCtx) runMemoryAdd(o brain.Options, given map[string]bool) error {
	mo := memoryOptsOf(c)
	prov := "operator"
	if f := c.fs.Lookup("provenance"); f != nil {
		prov = f.Value.String()
	}
	if given["provenance"] && prov == "" {
		fmt.Fprintln(c.out, `fyr memory: --provenance must be operator or derived, not ""`)
		return exitCode(2)
	}
	e := brain.MemoryEntry{Type: mo.memType, Name: mo.memName, Description: mo.memDesc, Provenance: prov, Global: mo.global}
	if mo.jsonIn {
		return c.runMemoryAddJSON(o, e, given)
	}
	body, closeBody, err := openBodyReader(mo.memBodyFile, given["body-file"])
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("fyr memory: "+err.Error()))
		return exitCode(2)
	}
	defer closeBody()
	if !given["body-file"] {
		parsed, rest, perr := brain.ParseMemoryStdin(body)
		if perr != nil {
			return memoryFinish(perr, "", c.out)
		}
		applyMemoryStdinEntry(&e, parsed, given)
		body = rest
	}
	return c.saveMemoryEntry(o, e, body)
}

func (c *runtimeCtx) runMemoryAddJSON(o brain.Options, e brain.MemoryEntry, given map[string]bool) error {
	if given["body-file"] {
		fmt.Fprintln(c.out, "fyr memory: --json-in and --body-file cannot be used together")
		return exitCode(2)
	}
	parsed, body, err := brain.ParseMemoryJSON(stdin)
	if err != nil {
		return memoryFinish(err, "", c.out)
	}
	applyMemoryStdinEntry(&e, parsed, given)
	return c.saveMemoryEntry(o, e, body)
}

func (c *runtimeCtx) saveMemoryEntry(o brain.Options, e brain.MemoryEntry, body io.Reader) error {
	if e.Global {
		o.Global = true
	}
	text, err := brain.MemoryAdd(o, e, body)
	if err == nil {
		text = "saved " + text + "\n"
	}
	return memoryFinish(err, text, c.out)
}

func applyMemoryStdinEntry(dst *brain.MemoryEntry, src brain.MemoryEntry, given map[string]bool) {
	if !given["type"] && dst.Type == "" && src.Type != "" {
		dst.Type = src.Type
	}
	if !given["name"] && dst.Name == "" && src.Name != "" {
		dst.Name = src.Name
	}
	if !given["description"] && dst.Description == "" && src.Description != "" {
		dst.Description = src.Description
	}
	if !given["provenance"] && src.Provenance != "" {
		dst.Provenance = src.Provenance
	}
	if !given["global"] && src.Global {
		dst.Global = true
	}
}

// searchOpts holds parsed flags for `fyr search`.
type searchOpts struct {
	limit, sliceHops, sliceBudget                                                             int
	kind, refs                                                                                string
	openRecords, global, jsonOut                                                              bool
	sliceQuery, sliceSession, contractsTarget, skeletonPath, pathFrom, pathTo, affectedTarget string
	vetPrompt, hookEvent                                                                      string
	repoMap, includeDocs                                                                      bool
}

func searchOptsOf(c *runtimeCtx) *searchOpts {
	if o, ok := c.opts.(*searchOpts); ok {
		return o
	}
	o := &searchOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func searchVerb() verb {
	return verb{
		flags:       registerSearchFlags,
		writesState: func(*runtimeCtx) bool { return true },
		positional:  true,
		run:         runSearch,
	}
}

func registerSearchFlags(c *runtimeCtx) {
	o := &searchOpts{}
	c.opts = o
	c.fs.IntVar(&o.limit, "limit", 0, "print at most `N` records and as many files, 1-50 (default 10)")
	c.fs.StringVar(&o.kind, "kind", "", "only hits of kind `K`: bug adr plan change handoff review testing retro spec deploy trail code doc feedback proposal learning guide memory")
	c.fs.StringVar(&o.refs, "refs", "", "every record naming `REF`: an ID, a file, or a directory (trailing /)")
	c.fs.BoolVar(&o.openRecords, "open", false, "open bugs, plans with unticked boxes, and the other open records")
	c.fs.BoolVar(&o.global, "global", false, "also the global records and every project registered with index --register, from the brain store")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print hits or query results as JSON")
	c.fs.StringVar(&o.sliceQuery, "slice", "", "0/1/2-hop context capsule for `QUERY` ranked by PageRank (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.contractsTarget, "contracts", "", "deterministic AST signatures, linked SPEC-* contracts and ADR guardrails for `TARGET` (deprecated: use fyr ast; v10.0.0)")
	c.fs.IntVar(&o.sliceHops, "hops", 0, "slice: maximum graph hops `N`, 0-6 (default 2) (deprecated: use fyr ast; v10.0.0)")
	c.fs.IntVar(&o.sliceBudget, "budget", 0, "slice, contracts: token budget `N` estimated as chars/4 (default 2000; 1500 for --contracts) (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.sliceSession, "session", "", "slice: conversation `ID` whose read files get the 50x boost (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.skeletonPath, "skeleton", "", "declaration-only start-end signature lines for `FILE` (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.pathFrom, "path", "", "shortest call/type/interface path from `SYM` (<= 6 hops) (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.pathTo, "to", "", "path: target symbol `SYM` (or give as positional argument) (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.affectedTarget, "affected", "", "reverse-BFS blast radius (<= 2 hops) for `TARGET` path or symbol (deprecated: use fyr ast; v10.0.0)")
	c.fs.BoolVar(&o.repoMap, "map", false, "top-10 God Nodes by PageRank, package deps and cycle check (deprecated: use fyr ast; v10.0.0)")
	c.fs.BoolVar(&o.includeDocs, "include-docs", false, "include Markdown docs and headings in AST maps and slices (deprecated: use fyr ast; v10.0.0)")
	c.fs.StringVar(&o.vetPrompt, "vet", "", "9-mode intent, 6-anchor vetter and skill/record match for `PROMPT` (deprecated: use fyr vet; v10.0.0)")
	c.fs.StringVar(&o.hookEvent, "hook", "", "Antigravity lifecycle hook `EVENT`: session-start, pre-invocation, pre-tool-use, post-tool-use or stop (deprecated: use fyr hook; v10.0.0)")
}

// indexOpts holds parsed flags for `fyr index`.
type indexOpts struct {
	global, rebuild, register, jsonOut bool
}

func indexOptsOf(c *runtimeCtx) *indexOpts {
	if o, ok := c.opts.(*indexOpts); ok {
		return o
	}
	o := &indexOpts{}
	if c.opts == nil {
		c.opts = o
	}
	return o
}

func indexVerb() verb {
	return verb{
		flags:       registerIndexFlags,
		writesState: func(*runtimeCtx) bool { return true },
		run:         runIndexVerb,
	}
}

func registerIndexFlags(c *runtimeCtx) {
	o := &indexOpts{}
	c.opts = o
	c.fs.BoolVar(&o.global, "global", false, "the global search index, ~/.config/fyr/search.db, instead of this checkout's")
	c.fs.BoolVar(&o.rebuild, "rebuild", false, "delete the index and build it again")
	c.fs.BoolVar(&o.register, "register", false, "register this checkout (the operator's consent, ADR-068) so cross-project readers take it (fyr search --global, fyr record import --global, and the metrics attribution join) once it has records in brain.db or .agents/trail.jsonl, then index it")
	c.fs.BoolVar(&o.jsonOut, "json", false, "print the result as JSON")
}

// runIndexVerb serves `index`; it needs no harness tree, and a release-binary
// or zip place has none (plan D10).
func runIndexVerb(c *runtimeCtx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	opts := indexOptsOf(c)
	o := search.Options{Dir: wd, Home: c.home, Global: opts.global, Rebuild: opts.rebuild}
	if opts.register {
		return runRegister(c, o)
	}
	err = c.runIndex(o)
	if search.IsUsage(err) {
		fmt.Fprintln(c.out, "fyr", err)
		return exitCode(2)
	}
	return err
}

func buildSearchOptions(c *runtimeCtx, wd string, o search.Options) search.Options {
	cache := newTurnBrainCache(c.home, o.Global)
	o.Dir = wd
	o.Home = c.home
	o.HookIn = stdin
	o.BrainRefs = makeBrainRefs(cache, wd)
	o.BrainRefsForDir = makeBrainRefsForDir(cache)
	o.BrainReprime = makeBrainReprime()
	o.BrainTurnContext = makeBrainTurnContext(cache)
	o.BrainTurnContextCtx = makeBrainTurnContextCtx(cache)
	o.BrainSpecBodies = makeBrainSpecBodies(cache)
	o.SkillCatalog = makeSkillCatalog()
	return o
}

// runSearch serves `search`; it needs no harness tree, and a release-binary or
// zip place has none (plan D10).
func runSearch(c *runtimeCtx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	so := searchOptsOf(c)
	o := buildSearchOptions(c, wd, search.Options{
		Words: c.fs.Args(), Refs: so.refs, Open: so.openRecords,
		Kind: so.kind, Limit: so.limit, Global: so.global,
		Slice: so.sliceQuery, Hops: so.sliceHops, Budget: so.sliceBudget, Session: so.sliceSession,
		Contracts: so.contractsTarget,
		Skeleton:  so.skeletonPath, Path: so.pathFrom, To: so.pathTo, Affected: so.affectedTarget, Map: so.repoMap,
		IncludeDocs: so.includeDocs, Vet: so.vetPrompt, Hook: so.hookEvent,
	})
	if err = search.Validate(o); err == nil {
		err = c.execSearchWithJSON(o, so.jsonOut)
	}
	return finishSearchErr(c.out, err)
}

func (c *runtimeCtx) execSearchWithJSON(o search.Options, asJSON bool) error {
	if !asJSON || o.Hook != "" {
		if !isGraphSearchMode(o) {
			recordHits(c, o)
		}
		return search.Search(o, c.out)
	}
	var hits []brain.Hit
	var recsErr string
	if !isGraphSearchMode(o) {
		var err error
		hits, _, err = recordHitsData(o)
		if err != nil {
			recsErr = search.Printable(err.Error())
		}
	}
	var buf bytes.Buffer
	if err := search.Search(o, &buf); err != nil {
		return err
	}
	js, err := brain.FormatSearchJSON(hits, buf.String(), recsErr)
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, js)
	return nil
}

func isGraphSearchMode(o search.Options) bool {
	return o.Slice != "" || o.Contracts != "" || o.Skeleton != "" || o.Path != "" || o.Affected != "" || o.Map || o.Vet != "" || o.Hook != ""
}

// stdin is where `memory add` reads the body; a test replaces it.
var stdin io.Reader = os.Stdin

// memorySubverbs is what `fyr memory` accepts; the check vocabulary reads it,
// and a test pins it against the dispatcher (plan fyr-check D14).
var memorySubverbs = []string{"add", "show"}

func memoryFinish(err error, text string, out io.Writer) error {
	if brain.IsRefusal(err) || brain.IsUsage(err) {
		fmt.Fprintln(out, search.Printable("fyr memory: "+err.Error())) // on the caller's writer, where a test reads it (IN13)
		return exitCode(2)
	}
	if err != nil {
		return errors.New(search.Printable(err.Error()))
	}
	fmt.Fprint(out, text)
	return nil
}

var specADRCiteRe = regexp.MustCompile(`\bADR-\d{3}[a-z]?\b`)

func buildSpecContractSources(specs, turnRecs []brain.Record, paths []string) []search.SpecContractSource {
	tokens := expandSpecPathTokens(paths)
	if len(tokens) == 0 {
		return nil
	}
	cited := map[string]bool{}
	var out []search.SpecContractSource
	for _, sp := range specs {
		if sp.Kind == "spec" && sp.Status == "current" && recordMatchesAnyToken(sp, tokens) {
			for _, id := range specADRCiteRe.FindAllString(sp.Body, -1) {
				cited[strings.ToUpper(id)] = true
			}
			out = append(out, search.SpecContractSource{ID: sp.ID, Kind: "spec", Status: sp.Status, Title: sp.Title, Body: sp.Body})
		}
	}
	for _, r := range turnRecs {
		if r.Kind == "adr" && r.Status == "accepted" && (cited[strings.ToUpper(r.ID)] || recordMatchesAnyToken(r, tokens)) {
			out = append(out, search.SpecContractSource{ID: r.ID, Kind: "adr", Status: r.Status, Title: r.Title})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func expandSpecPathTokens(paths []string) []string {
	seen, out := map[string]bool{}, []string(nil)
	add := func(t string) {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, raw := range paths {
		n := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(raw)), "./")
		if n == "" || n == "." {
			continue
		}
		trimmed := strings.TrimSuffix(n, "/")
		add(n)
		add(trimmed)
		if strings.HasSuffix(n, "/") || path.Ext(trimmed) == "" {
			if strings.Contains(trimmed, "/") {
				add(trimmed + "/")
			}
			continue
		}
		if dir := path.Dir(trimmed); dir != "." && dir != "/" {
			add(dir + "/")
		}
	}
	sort.Strings(out)
	return out
}

func recordMatchesAnyToken(r brain.Record, tokens []string) bool {
	hay := strings.ToLower(r.ID + "\n" + r.Title + "\n" + r.Body)
	for _, tok := range tokens {
		if containsPathBoundary(hay, tok) {
			return true
		}
	}
	return false
}

func containsPathBoundary(hay, tok string) bool {
	for start := 0; tok != ""; {
		idx := strings.Index(hay[start:], tok)
		if idx < 0 {
			return false
		}
		pos, end := start+idx, start+idx+len(tok)
		prevOK := pos == 0 || !isPathWordByte(hay[pos-1])
		nextOK := strings.HasSuffix(tok, "/") || end == len(hay) || !isPathWordByte(hay[end])
		if prevOK && nextOK {
			return true
		}
		start = pos + 1
	}
	return false
}

func isPathWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}
