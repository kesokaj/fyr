package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

func init() {
	def := verbs["memory"]
	prev := def.flags
	def.flags = func(c *ctx) {
		prev(c)
		c.fs.String("provenance", "operator", "add: the memory's provenance `P`: operator or derived")
	}
	verbs["memory"] = def
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
	if args[0] == "add" && given["json"] {
		fmt.Fprintln(c.out, "fyr memory: add takes no --json")
		return exitCode(2)
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	o := brain.Options{Home: c.home, Dir: wd}
	if args[0] == "show" {
		if c.jsonOut {
			text, serr := brain.MemoryShowJSON(o)
			return memoryFinish(serr, text, c.out)
		}
		text, serr := brain.MemoryShow(o)
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
	e := brain.MemoryEntry{Type: c.memType, Name: c.memName, Description: c.memDesc, Provenance: prov}
	if c.jsonIn {
		if given["body-file"] {
			fmt.Fprintln(c.out, "fyr memory: --json-in and --body-file cannot be used together")
			return exitCode(2)
		}
		parsed, body, err := brain.ParseMemoryJSON(stdin)
		if err != nil {
			return memoryFinish(err, "", c.out)
		}
		applyMemoryStdinEntry(&e, parsed, given)
		text, err := brain.MemoryAdd(o, e, body)
		if err == nil {
			text = "saved " + text + "\n"
		}
		return memoryFinish(err, text, c.out)
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
		Skeleton: c.skeletonPath, Path: c.pathFrom, To: c.pathTo, Affected: c.affectedTarget, Map: c.repoMap,
		Vet: c.vetPrompt, Hook: c.hookEvent, HookIn: stdin,
		BrainRefs:        makeBrainRefs(cache, wd),
		BrainRefsForDir:  makeBrainRefsForDir(cache),
		BrainReprime:     makeBrainReprime(),
		BrainTurnContext: makeBrainTurnContext(cache),
		SkillCatalog:     makeSkillCatalog(),
	}
	if c.cmd == "index" && c.register {
		return runRegister(c, o)
	} else if c.cmd == "index" {
		err = search.Index(o, c.out)
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
	return o.Slice != "" || o.Skeleton != "" || o.Path != "" || o.Affected != "" || o.Map || o.Vet != "" || o.Hook != ""
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
