package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// recordFlags is the flags each record subverb takes, besides --home and
// --repo: one it does not take is a usage error, never parsed and ignored
// (IN5's rule, per subverb).
var recordFlags = map[string]map[string]bool{
	"add":    {"title": true, "title-file": true, "body-file": true, "status": true, "id": true, "project": true, "global": true, "json-in": true},
	"get":    {"global": true, "out": true, "json": true},
	"edit":   {"status": true, "title": true, "body": true, "body-file": true, "global": true, "json-in": true},
	"list":   {"kind": true, "open": true, "project": true, "all": true, "global": true, "limit": true, "json": true},
	"export": {},
	"sync":   {},
	"import": {"dry-run": true, "global": true},
}

// runRecord serves `record add|get|edit|list|export|sync|import` (spec
// the record SPEC-fyr-record); it needs no harness tree. Exit 0 with
// or without output, 1 a refusal, 2 a usage error, the reason on the writer.
func runRecord(c *ctx) error {
	var sub string
	rest := c.fs.Args()
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	takes, ok := recordFlags[sub] // no subcommand is not one of them
	if !ok {
		return usageErr(c.out, "record", unknownRecordSub(sub))
	}
	set, err := recordFlagsSet(c, sub, takes)
	if err != nil {
		return err
	}
	wd, err := c.projectDir()
	if err != nil {
		return err
	}
	return dispatchRecord(c, brain.Options{Home: c.home, Dir: wd, Repo: c.repoFlag, Global: c.global}, sub, rest, set)
}

func dispatchRecord(c *ctx, o brain.Options, sub string, rest []string, set map[string]bool) error {
	switch sub {
	case "add":
		if len(rest) == 1 {
			return recordAdd(c, o, rest[0], set)
		}
	case "get":
		if len(rest) > 0 {
			return recordGet(c, o, rest, set)
		}
	case "edit":
		if len(rest) == 1 {
			return recordEdit(c, o, rest[0], set)
		}
	case "list":
		if len(rest) == 0 {
			return recordList(c, o, set)
		}
	case "export":
		if len(rest) == 0 {
			return recordExport(c, o)
		}
	case "sync":
		if len(rest) == 0 {
			return recordSync(c, o)
		}
	case "import":
		if len(rest) <= 1 && (!c.global || len(rest) == 0) {
			return recordImport(c, o, rest)
		}
	}
	return usageErr(c.out, "record", "")
}

// unknownRecordSub is the reason line for a subverb record does not have; a
// bare `fyr record` has none (the operator's reading, plan ledger D24).
func unknownRecordSub(sub string) string {
	if sub == "" {
		return ""
	}
	return fmt.Sprintf("fyr record: unknown subcommand %q", sub)
}

// recordFlagsSet returns the flags set on the command line; a flag sub does
// not take is a usage error, said on the writer.
func recordFlagsSet(c *ctx, sub string, takes map[string]bool) (map[string]bool, error) {
	set := map[string]bool{}
	var stray []string
	c.fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if !isGlobalFlag(f.Name) && !takes[f.Name] {
			stray = append(stray, "--"+f.Name)
		}
	})
	if len(stray) > 0 {
		sort.Strings(stray)
		return nil, usageErr(c.out, "record", fmt.Sprintf("fyr record %s does not take %s", sub, strings.Join(stray, ", ")))
	}
	return set, nil
}

// recordAdd serves `record add`: the new record's ID, and the redactor
// warning when the given ID, the stored title or the body would trip it.
func recordAdd(c *ctx, o brain.Options, kind string, set map[string]bool) error {
	body, closeBody, err := c.resolveRecordAddInput(set)
	if err != nil {
		return err
	}
	defer closeBody()
	var seen strings.Builder
	id, err := brain.Add(o, brain.AddInput{Kind: kind, Title: c.recTitle, Status: c.recStatus, ID: c.recID, Project: c.recProject, Body: teeBodyFrom(body, &seen)})
	if err == nil {
		fmt.Fprintln(c.out, id)
		warnRedactable(c.recID, c.recTitle, seen.String())
	}
	return recordErr(c, err)
}

func (c *ctx) resolveRecordAddInput(set map[string]bool) (io.Reader, func(), error) {
	noop := func() {}
	if c.jsonIn {
		if set["body-file"] || set["title-file"] {
			return nil, noop, usageErr(c.out, "record", "fyr record add: --json-in takes no --body-file or --title-file")
		}
		doc, err := brain.ParseRecordJSON(stdin)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		c.applyRecordStdinDoc(doc, set)
		return strings.NewReader(doc.Body), noop, nil
	}
	if c.recTitleFile != "" {
		if c.recTitle != "" {
			return nil, noop, usageErr(c.out, "record", "fyr record add takes --title or --title-file, not both")
		}
		t, err := readTitleFile(c.recTitleFile)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		c.recTitle = t
	}
	body, closeBody, err := openBodyReader(c.recBodyFile, set["body-file"])
	if err != nil {
		return nil, noop, recordErr(c, err)
	}
	if !set["body-file"] {
		doc, rest, perr := brain.ParseRecordStdin(body)
		if perr != nil {
			closeBody()
			return nil, noop, recordErr(c, perr)
		}
		c.applyRecordStdinDoc(doc, set)
		body = rest
	}
	return body, closeBody, nil
}

func (c *ctx) applyRecordStdinDoc(doc brain.RecordStdinDoc, set map[string]bool) {
	if !set["title"] && c.recTitle == "" && doc.Title != "" {
		c.recTitle = doc.Title
	}
	if !set["status"] && doc.Status != "" {
		c.recStatus = doc.Status
	}
	if !set["id"] && doc.ID != "" {
		c.recID = doc.ID
	}
	if !set["project"] && doc.Project != "" {
		c.recProject = doc.Project
	}
}

// recordList serves `record list`: one line per record, or JSON with --json.
func recordList(c *ctx, o brain.Options, set map[string]bool) error {
	if set["limit"] && c.limit < 1 {
		return usageErr(c.out, "record", "fyr record list: --limit must be at least 1")
	}
	recs, err := brain.List(o, brain.ListFilter{Kind: c.kind, Open: c.openRecords, Project: c.recProject, All: c.recAll})
	if err != nil {
		return recordErr(c, err)
	}
	if c.limit > 0 && len(recs) > c.limit {
		recs = recs[:c.limit]
	}
	if c.jsonOut {
		js, jerr := brain.FormatRecordsJSON(recs, false)
		if jerr != nil {
			return recordErr(c, jerr)
		}
		fmt.Fprint(c.out, js)
		return nil
	}
	for _, r := range recs {
		fmt.Fprintln(c.out, brain.FormatLine(r, c.recAll))
	}
	return nil
}

// recordGet serves `record get`: each record found, a blank line between,
// then the refusal naming any missing; with --out writes one record's
// body under .agents/scratch/; or with --json prints a JSON array.
func recordGet(c *ctx, o brain.Options, ids []string, set map[string]bool) error {
	if set["out"] && c.jsonOut {
		return usageErr(c.out, "record", "fyr record get takes --out or --json, not both")
	}
	if set["out"] {
		if len(ids) != 1 {
			return usageErr(c.out, "record", "fyr record get --out takes one id")
		}
		if _, _, err := brain.CheckScratchOut(o.Dir, c.recOut); err != nil {
			return recordErr(c, err)
		}
	}
	recs, _, err := brain.Get(o, ids)
	if set["out"] {
		if err != nil {
			return recordErr(c, err)
		}
		return recordErr(c, brain.WriteScratch(o.Dir, c.recOut, recs[0].Body))
	}
	if c.jsonOut {
		if len(recs) > 0 || err == nil {
			js, jerr := brain.FormatRecordsJSON(recs, true)
			if jerr != nil {
				return recordErr(c, jerr)
			}
			fmt.Fprint(c.out, js)
		}
		return recordErr(c, err)
	}
	for i, r := range recs {
		if i > 0 {
			fmt.Fprintln(c.out)
		}
		fmt.Fprint(c.out, brain.FormatGet(r))
	}
	return recordErr(c, err)
}

// recordEdit serves `record edit`: only what the command line set changes;
// a new title or body gets the redactor warning, a kept one does not.
func recordEdit(c *ctx, o brain.Options, id string, set map[string]bool) error {
	if c.recBody && set["body-file"] {
		return usageErr(c.out, "record", "fyr record edit takes --body or --body-file, not both")
	}
	if c.jsonIn && (c.recBody || set["body-file"]) {
		return usageErr(c.out, "record", "fyr record edit: --json-in takes no --body or --body-file")
	}
	in := brain.EditInput{ID: id}
	if set["status"] {
		in.Status = &c.recStatus
	}
	if set["title"] {
		in.Title = &c.recTitle
	}
	var seen strings.Builder
	closeBody, err := c.resolveRecordEditInput(&in, &seen, set)
	if err != nil {
		return err
	}
	defer closeBody()
	err = brain.Edit(o, in)
	if err == nil {
		title := ""
		if in.Title != nil {
			title = *in.Title
		}
		warnRedactable("", title, seen.String())
	}
	return recordErr(c, err)
}

func (c *ctx) resolveRecordEditInput(in *brain.EditInput, seen *strings.Builder, set map[string]bool) (func(), error) {
	noop := func() {}
	if c.jsonIn {
		doc, err := brain.ParseRecordEditJSON(stdin)
		if err != nil {
			return noop, recordErr(c, err)
		}
		if in.Status == nil && doc.Status != nil {
			in.Status = doc.Status
		}
		if in.Title == nil && doc.Title != nil {
			in.Title = doc.Title
		}
		if doc.Body != nil {
			in.Body = teeBodyFrom(strings.NewReader(*doc.Body), seen)
		}
		return noop, nil
	}
	if !c.recBody && !set["body-file"] {
		return noop, nil
	}
	body, closeBody, err := openBodyReader(c.recBodyFile, set["body-file"])
	if err != nil {
		return noop, recordErr(c, err)
	}
	if !set["body-file"] {
		doc, rest, perr := brain.ParseRecordStdin(body)
		if perr != nil {
			closeBody()
			return noop, recordErr(c, perr)
		}
		if in.Title == nil && doc.Title != "" {
			t := doc.Title
			in.Title = &t
		}
		if in.Status == nil && doc.Status != "" {
			s := doc.Status
			in.Status = &s
		}
		body = rest
	}
	in.Body = teeBodyFrom(body, seen)
	return closeBody, nil
}

// recordExport serves `record export`: writes the project's exported-kind
// records from brain.db to .agents/trail.jsonl on demand.
func recordExport(c *ctx, o brain.Options) error {
	n, err := brain.Export(o)
	if err == nil {
		fmt.Fprintf(c.out, "export: %d records written to .agents/trail.jsonl\n", n)
	}
	return recordErr(c, err)
}

// recordSync serves `record sync`: loads .agents/trail.jsonl into brain.db and
// its FTS5 index on demand.
func recordSync(c *ctx, o brain.Options) error {
	recs, err := brain.Sync(o)
	if err == nil {
		fmt.Fprintf(c.out, "sync: %d records loaded from .agents/trail.jsonl\n", len(recs))
		warnSyncRedactable(recs)
	}
	return recordErr(c, err)
}

// recordImport serves `record import`: the project's trail, or with
// --global the home's old logs.
func recordImport(c *ctx, o brain.Options, rest []string) error {
	var res *brain.ImportResult
	var err error
	if c.global {
		res, err = brain.ImportGlobal(o, c.dryRun)
	} else {
		docs := ""
		if len(rest) == 1 {
			docs = rest[0]
		}
		res, err = brain.Import(o, docs, c.dryRun)
	}
	if err == nil {
		printImport(c, res)
		warnImportRedactable(res, c.dryRun)
	}
	return recordErr(c, err)
}

// printImport is what an import did, and every file the caller removes by
// name afterwards (D13): the import itself removes nothing. Each path is one
// shell word (brain.ShellQuote), and git is told to read them literally: a
// tracked name can hold a glob or start with a dash.
func printImport(c *ctx, res *brain.ImportResult) {
	p := func(format string, a ...any) { fmt.Fprintln(c.out, search.Printable(fmt.Sprintf(format, a...))) }
	if c.dryRun {
		// A dry run opens no store, so it cannot say what the store holds:
		// its records are what the sources hold (PLAN.md item 58).
		p("import (dry run: nothing written, the store not read): %s; %d records in the sources, %d dropped (git history keeps them)", brain.ImportSummary(res), len(res.Records), res.Dropped)
	} else {
		p("import: %s; %d records, %d dropped (git history keeps them), %d already stored", brain.ImportSummary(res), len(res.Records), res.Dropped, res.Skipped)
	}
	for _, r := range res.Records {
		st := r.Status
		if st == "" {
			st = "-"
		}
		p("record  %s  %s  %s  %s", r.ID, r.Kind, st, r.Path)
	}
	for _, s := range res.Problems {
		p("problem  %s", s)
	}
	if len(res.Remove) > 0 {
		p("remove with git --literal-pathspecs rm -- and these paths (%d files):", len(res.Remove))
		for _, f := range res.Remove {
			p("%s", brain.ShellQuote(f))
		}
	}
	if len(res.Memory) > 0 {
		p("remove with rm by name, untracked memory store files (%d):", len(res.Memory))
		for _, f := range res.Memory {
			p("%s", brain.ShellQuote(f))
		}
	}
	if len(res.Sources) > 0 {
		p("read from ~/.config/fyr; remove by name once the records are checked (%d):", len(res.Sources))
		for _, f := range res.Sources {
			p("%s", brain.ShellQuote(f))
		}
	}
}

// recordErr prints a refusal (exit 1) or a usage error (exit 2) on the
// writer, through printable: the message can quote a record's own text.
func recordErr(c *ctx, err error) error {
	switch {
	case err == nil:
		return nil
	case brain.IsUsage(err):
		fmt.Fprintln(c.out, search.Printable("fyr record: "+err.Error()))
		return exitCode(2)
	case brain.IsRefusal(err):
		fmt.Fprintln(c.out, search.Printable("fyr record: "+err.Error()))
		return exitCode(1)
	}
	// Any other error can carry a checkout's path, which a hand-extracted
	// archive can fill with escape bytes: filtered too (security review
	// 2026-10-25, finding 8).
	return errors.New(search.Printable(err.Error()))
}

// recordHits prints the brain store's hits before the file hits (ADR-063):
// for a words search the ranked records, for --refs the records naming the
// ID or path, for --open the open records (D30). Nothing when there are
// none, and a one-line note when the records could not be searched (a
// trail.jsonl that does not parse), so the file search still answers.
func recordHits(c *ctx, o search.Options) {
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

// maxTitleFile caps what --title-file reads: past brain's 512-byte title,
// so an over-long title is refused by the store's own check, not cut here.
const maxTitleFile = 4 << 10

// readTitleFile is --title-file's title: the file's text with one trailing
// newline dropped, so a title the /work lead copies from a worker's
// records never passes through a shell (ADR-501). Every other check is the
// store's, as for --title.
func readTitleFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTitleFile+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxTitleFile {
		return "", fmt.Errorf("--title-file %s is over %d bytes", path, maxTitleFile)
	}
	s := strings.TrimSuffix(string(b), "\n")
	return strings.TrimSuffix(s, "\r"), nil
}
