package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	o := search.Options{
		Dir: wd, Home: c.home, Words: c.fs.Args(), Refs: c.refs, Open: c.openRecords,
		Kind: c.kind, Limit: c.limit, Global: c.global, Rebuild: c.rebuild,
		Slice: c.sliceQuery, Hops: c.sliceHops, Budget: c.sliceBudget, Session: c.sliceSession,
		Skeleton: c.skeletonPath, Path: c.pathFrom, To: c.pathTo, Affected: c.affectedTarget, Map: c.repoMap,
		Vet: c.vetPrompt, Hook: c.hookEvent, HookIn: stdin,
		BrainRefs:        makeBrainRefs(wd, c.home, c.global),
		BrainRefsForDir:  makeBrainRefsForDir(c.home, c.global),
		BrainReprime:     makeBrainReprime(),
		BrainTurnContext: makeBrainTurnContext(),
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

func makeBrainTurnContext() func(dir, home, prompt string, files []string) search.TurnBrainContext {
	return func(dir, home, prompt string, files []string) search.TurnBrainContext {
		recs := loadTurnRecords(dir, home)
		if len(recs) == 0 {
			return search.TurnBrainContext{}
		}
		terms := extractTurnQueryTerms(prompt, files)
		var ctx search.TurnBrainContext
		for _, r := range recs {
			classifyTurnRecord(&ctx, dir, prompt, terms, r)
		}
		return ctx
	}
}

func loadTurnRecords(dir, home string) []brain.Record {
	if home != "" {
		if _, err := os.Stat(brain.Path(home)); err == nil {
			if recs, err := brain.List(brain.Options{Home: home, Dir: dir}, brain.ListFilter{}); err == nil && len(recs) > 0 {
				return recs
			}
		}
	}
	if recs, ok, err := brain.ReadTrail(dir); err == nil && ok {
		return recs
	}
	return nil
}

func classifyTurnRecord(ctx *search.TurnBrainContext, dir, prompt string, terms []string, r brain.Record) {
	switch r.Kind {
	case "plan":
		if (r.Status == "open" || r.Status == "") && ctx.ActivePlan == nil {
			ctx.ActivePlan = &search.MatchedRecord{
				ID: r.ID, Kind: "plan", Status: r.Status, Title: r.Title,
				Provenance: r.Provenance, Body: summarizePlanReadyPhases(r.Body),
			}
		}
	case "memory":
		if (r.Status == "closed" || r.Status == "") && len(ctx.Memories) < 3 && matchesRecordTerms(r, prompt, terms) {
			ctx.Memories = append(ctx.Memories, search.MatchedRecord{
				ID: r.ID, Kind: "memory", Status: r.Status, Title: r.Title,
				Provenance: r.Provenance, Body: r.Body, StaleTag: brain.MemoryStalenessTag(dir, r),
			})
		}
	case "bug":
		if (r.Status == "open" || r.Status == "") && len(ctx.Bugs) < 3 && matchesRecordTerms(r, prompt, terms) {
			ctx.Bugs = append(ctx.Bugs, search.MatchedRecord{
				ID: r.ID, Kind: "bug", Status: r.Status, Title: r.Title,
				Provenance: r.Provenance, Body: r.Body,
			})
		}
	case "adr":
		if r.Status != "accepted" && r.Status != "open" && r.Status != "" {
			return
		}
		rec := search.MatchedRecord{
			ID: r.ID, Kind: "adr", Status: r.Status, Title: r.Title,
			Provenance: r.Provenance, Body: r.Body,
		}
		conflict := search.CheckADRConflict(strings.ToLower(prompt), rec)
		if conflict != "" {
			ctx.Conflicts = append(ctx.Conflicts, conflict)
		}
		if (conflict != "" || matchesRecordTerms(r, prompt, terms)) && len(ctx.ADRs) < 3 {
			ctx.ADRs = append(ctx.ADRs, rec)
		}
	}
}

func extractTurnQueryTerms(prompt string, files []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) >= 4 && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, f := range files {
		add(f)
		add(filepath.Base(f))
	}
	for _, w := range strings.FieldsFunc(prompt, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '/' && r != '.'
	}) {
		add(w)
	}
	return out
}

func matchesRecordTerms(r brain.Record, prompt string, terms []string) bool {
	hay := strings.ToLower(r.ID + "\n" + r.Title + "\n" + r.Body)
	if r.ID != "" && strings.Contains(strings.ToLower(prompt), strings.ToLower(r.ID)) {
		return true
	}
	for _, t := range terms {
		if strings.Contains(hay, t) {
			return true
		}
	}
	return false
}

func makeSkillCatalog() func(home string) []search.SkillMeta {
	return func(home string) []search.SkillMeta {
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
}

func parseSkillMetaFile(name, path string) (search.SkillMeta, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return search.SkillMeta{}, false
	}
	desc := extractSkillDescription(string(data))
	if desc == "" {
		return search.SkillMeta{}, false
	}
	if len(desc) > 600 {
		desc = strings.TrimSpace(desc[:600])
	}
	return search.SkillMeta{
		Name:        name,
		Description: desc,
		Path:        "skills/" + name + "/SKILL.md",
		Digest:      desc,
	}, true
}

func extractSkillDescription(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	var descLines []string
	inDesc := false
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "description:") {
			inDesc = true
			rest := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			if rest != "" && rest != "|" && rest != ">" {
				descLines = append(descLines, strings.Trim(rest, `"'`))
			}
			continue
		}
		if inDesc {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				descLines = append(descLines, strings.TrimSpace(line))
			} else {
				break
			}
		}
	}
	return strings.Join(descLines, " ")
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
