package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

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
	if args[0] == "show" && (given["type"] || given["name"] || given["description"] || given["provenance"] || given["body-file"]) {
		fmt.Fprintln(c.out, "fyr memory: show takes no --type, --name, --description, --body-file or --provenance")
		return exitCode(2)
	}
	prov := "operator"
	if f := c.fs.Lookup("provenance"); f != nil {
		prov = f.Value.String()
	}
	if given["provenance"] && prov == "" {
		fmt.Fprintln(c.out, `fyr memory: --provenance must be operator or derived, not ""`)
		return exitCode(2)
	}
	body, closeBody, err := openBodyReader(c.memBodyFile, given["body-file"])
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("fyr memory: "+err.Error()))
		return exitCode(2)
	}
	defer closeBody()
	return memoryCmdWithReader(args, brain.MemoryEntry{Type: c.memType, Name: c.memName, Description: c.memDesc, Provenance: prov}, body, c.home, c.out)
}

// runSearch serves `search` and `index`; neither needs a harness tree, and a
// release-binary or zip place has none (plan D10).
func runSearch(c *ctx) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := search.Options{
		Dir: wd, Home: c.home, Words: c.fs.Args(), Refs: c.refs, Open: c.openRecords,
		Kind: c.kind, Limit: c.limit, Global: c.global, Rebuild: c.rebuild,
		Slice: c.sliceQuery, Hops: c.sliceHops, Budget: c.sliceBudget, Session: c.sliceSession,
		Skeleton: c.skeletonPath, Path: c.pathFrom, To: c.pathTo, Affected: c.affectedTarget, Map: c.repoMap,
		Hook: c.hookEvent, HookIn: stdin,
		BrainRefs:       makeBrainRefs(wd, c.home, c.global),
		BrainRefsForDir: makeBrainRefsForDir(c.home, c.global),
		BrainReprime:    makeBrainReprime(),
	}
	if c.cmd == "index" && c.register {
		return runRegister(c, o)
	} else if c.cmd == "index" {
		err = search.Index(o, c.out)
	} else if err = search.Validate(o); err == nil {
		if !isGraphSearchMode(o) {
			recordHits(c, o)
		}
		err = search.Search(o, c.out)
	}
	if search.IsUsage(err) {
		fmt.Fprintln(c.out, "fyr", err)
		return exitCode(2)
	}
	return err
}

func isGraphSearchMode(o search.Options) bool {
	return o.Slice != "" || o.Skeleton != "" || o.Path != "" || o.Affected != "" || o.Map || o.Hook != ""
}

func makeBrainReprime() func(dir, home string) string {
	return func(dir, home string) string {
		if home == "" {
			return ""
		}
		if _, err := os.Stat(brain.Path(home)); err != nil {
			return ""
		}
		bo := brain.Options{Home: home, Dir: dir}
		var parts []string
		if mem, err := brain.MemoryShow(bo); err == nil && strings.TrimSpace(mem) != "" {
			parts = append(parts, "# Re-primed Project Memory\n"+strings.TrimSpace(mem))
		}
		if plans, err := brain.List(bo, brain.ListFilter{Kind: "plan", Open: true}); err == nil && len(plans) > 0 {
			var lines []string
			for _, p := range plans {
				lines = append(lines, fmt.Sprintf("- %s: %s (%s)", p.ID, p.Title, summarizePlanReadyPhases(p.Body)))
			}
			parts = append(parts, "# Active Open Plans\n"+strings.Join(lines, "\n"))
		}
		return strings.Join(parts, "\n\n")
	}
}

func summarizePlanReadyPhases(body string) string {
	if line := brain.ReadyPhasesLine(body); line != "" {
		return line
	}
	return "open"
}

func makeBrainRefsForDir(home string, global bool) func(dir, target string) []search.LinkedRecord {
	return func(dir, target string) []search.LinkedRecord {
		return makeBrainRefs(dir, home, global)(target)
	}
}

func makeBrainRefs(dir, home string, global bool) func(string) []search.LinkedRecord {
	bo := brain.Options{Home: home, Dir: dir, Global: global}
	return func(target string) []search.LinkedRecord {
		hits, err := brain.Refs(bo, target, "", 10)
		if err != nil {
			return nil
		}
		var out []search.LinkedRecord
		for _, h := range hits {
			isOpenBug := h.Record.Kind == "bug" && (h.Record.Status == "open" || h.Record.Status == "")
			isActiveADR := h.Record.Kind == "adr" && (h.Record.Status == "accepted" || h.Record.Status == "open" || h.Record.Status == "")
			if isOpenBug || isActiveADR {
				out = append(out, search.LinkedRecord{ID: h.Record.ID, Status: h.Record.Status, Title: h.Record.Title})
			}
		}
		return out
	}
}

// stdin is where `memory add` reads the body; a test replaces it.
var stdin io.Reader = os.Stdin

// memorySubverbs is what memoryCmd accepts; the check vocabulary reads it,
// and a test pins it against the dispatcher (plan fyr-check D14).
var memorySubverbs = []string{"add", "show"}

// memoryCmd runs `memory add` and `memory show` in the checkout holding the
// working directory: an alias over the brain store's memory records (D14).
// A refusal prints its reason and exits 2.
func memoryCmd(args []string, e brain.MemoryEntry, home string, out io.Writer) error {
	return memoryCmdWithReader(args, e, stdin, home, out)
}

func memoryCmdWithReader(args []string, e brain.MemoryEntry, body io.Reader, home string, out io.Writer) error {
	if len(args) != 1 || !slices.Contains(memorySubverbs, args[0]) {
		// A usage error exits 2 like every other usage error here (dream's, a verb's unknown flag).
		return usageErr(out, "memory", "")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := brain.Options{Home: home, Dir: wd}
	var text string
	if args[0] == "add" {
		text, err = brain.MemoryAdd(o, e, body)
		if err == nil {
			text = "saved " + text + "\n"
		}
	} else {
		text, err = brain.MemoryShow(o)
	}
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
