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

func memoryVerb() verb {
	return verb{flags: registerMemoryFlags, positional: true, run: runMemory}
}

func registerMemoryFlags(c *ctx) {
	c.fs.StringVar(&c.memType, "type", "", "add: the memory's type `T`: semantic, procedural or episodic")
	c.fs.StringVar(&c.memName, "name", "", "add: the memory's slug, `SLUG`")
	c.fs.StringVar(&c.memDesc, "description", "", "add: the memory in one line, `TEXT`")
	c.fs.StringVar(&c.memBodyFile, "body-file", "", "add: read the body from `FILE` instead of stdin")
	c.fs.BoolVar(&c.global, "global", false, "shared global memory scope across projects")
	c.fs.BoolVar(&c.memIncludeLow, "include-low", false, "show: include cards with validity < 0.40")
	c.fs.BoolVar(&c.jsonIn, "json-in", false, "add: read JSON payload from stdin")
	c.fs.BoolVar(&c.jsonOut, "json", false, "show: print the memory index as JSON")
	c.fs.IntVar(&c.limit, "limit", 0, "show: print at most `N` memory cards")
	c.fs.String("provenance", "operator", "add: the memory's provenance `P`: operator or derived")
}

// runMemory needs no harness tree (R10).
func runMemory(c *ctx) error {
	given := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	args := c.fs.Args()
	if len(args) != 1 || !slices.Contains(memorySubverbs, args[0]) {
		return usageErr(c.out, "memory", "")
	}
	if args[0] == "show" && (given["type"] || given["name"] || given["description"] || given["provenance"] || given["body-file"] || given["json-in"]) {
		fmt.Fprintln(c.out, "fyr memory: show takes no --type, --name, --description, --body-file, --json-in or --provenance")
		return exitCode(2)
	}
	if args[0] == "add" && (given["json"] || given["include-low"] || given["limit"]) {
		switch {
		case given["include-low"]:
			fmt.Fprintln(c.out, "fyr memory: add takes no --include-low")
		case given["limit"]:
			fmt.Fprintln(c.out, "fyr memory: add takes no --limit")
		default:
			fmt.Fprintln(c.out, "fyr memory: add takes no --json")
		}
		return exitCode(2)
	}
	if args[0] == "show" && c.limit < 0 {
		fmt.Fprintln(c.out, "fyr memory: --limit must be >= 0")
		return exitCode(2)
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	o := brain.Options{Home: c.home, Dir: wd, Global: c.global}
	if args[0] == "show" {
		showOpts := brain.MemoryShowOptions{IncludeLow: c.memIncludeLow, Limit: c.limit}
		if c.jsonOut {
			text, serr := brain.MemoryShowJSONWithOptions(o, showOpts)
			return memoryFinish(serr, text, c.out)
		}
		text, serr := brain.MemoryShowWithOptions(o, showOpts)
		return memoryFinish(serr, text, c.out)
	}
	return c.runMemoryAdd(o, given)
}

func (c *ctx) runMemoryAdd(o brain.Options, given map[string]bool) error {
	prov := "operator"
	if f := c.fs.Lookup("provenance"); f != nil {
		prov = f.Value.String()
	}
	if given["provenance"] && prov == "" {
		fmt.Fprintln(c.out, `fyr memory: --provenance must be operator or derived, not ""`)
		return exitCode(2)
	}
	e := brain.MemoryEntry{Type: c.memType, Name: c.memName, Description: c.memDesc, Provenance: prov, Global: c.global}
	if c.jsonIn {
		return c.runMemoryAddJSON(o, e, given)
	}
	body, closeBody, err := openBodyReader(c.memBodyFile, given["body-file"])
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

func (c *ctx) runMemoryAddJSON(o brain.Options, e brain.MemoryEntry, given map[string]bool) error {
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

func (c *ctx) saveMemoryEntry(o brain.Options, e brain.MemoryEntry, body io.Reader) error {
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

// runSearch serves `search` and `index`; neither needs a harness tree, and a
// release-binary or zip place has none (plan D10).
func runSearch(c *ctx) error {
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	cache := newTurnBrainCache(c.home, c.global)
	o := search.Options{
		Dir: wd, Home: c.home, Words: c.fs.Args(), Refs: c.refs, Open: c.openRecords,
		Kind: c.kind, Limit: c.limit, Global: c.global, Rebuild: c.rebuild,
		Slice: c.sliceQuery, Hops: c.sliceHops, Budget: c.sliceBudget, Session: c.sliceSession,
		Contracts: c.contractsTarget,
		Skeleton:  c.skeletonPath, Path: c.pathFrom, To: c.pathTo, Affected: c.affectedTarget, Map: c.repoMap,
		IncludeDocs: c.searchIncludeDocs, Vet: c.vetPrompt, Hook: c.hookEvent, HookIn: stdin,
		BrainRefs:           makeBrainRefs(cache, wd),
		BrainRefsForDir:     makeBrainRefsForDir(cache),
		BrainReprime:        makeBrainReprime(),
		BrainTurnContext:    makeBrainTurnContext(cache),
		BrainTurnContextCtx: makeBrainTurnContextCtx(cache),
		BrainSpecBodies:     makeBrainSpecBodies(cache),
		SkillCatalog:        makeSkillCatalog(),
	}
	if c.cmd == "index" && c.register {
		return runRegister(c, o)
	} else if c.cmd == "index" {
		err = c.runIndex(o)
	} else if err = search.Validate(o); err == nil {
		err = c.execSearch(o)
	}
	if search.IsUsage(err) {
		fmt.Fprintln(c.out, "fyr", err)
		return exitCode(2)
	}
	return err
}

func (c *ctx) execSearch(o search.Options) error {
	if !c.jsonOut || o.Hook != "" {
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
