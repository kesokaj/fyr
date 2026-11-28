package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
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
