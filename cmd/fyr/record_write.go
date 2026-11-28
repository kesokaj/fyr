package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

// recordAdd serves `record add`: the new record's ID, and the redactor
// warning when the given ID, the stored title or the body would trip it.
func recordAdd(c *runtimeCtx, o brain.Options, kind string, set map[string]bool) error {
	opts := recordOptsOf(c)
	body, closeBody, err := c.resolveRecordAddInput(set)
	if err != nil {
		return err
	}
	defer closeBody()
	var seen strings.Builder
	id, err := brain.Add(o, brain.AddInput{Kind: kind, Title: opts.title, Status: opts.status, ID: opts.id, Project: opts.project, Body: teeBodyFrom(body, &seen)})
	if err == nil {
		fmt.Fprintln(c.out, id)
		warnRedactable(opts.id, opts.title, seen.String())
	}
	return recordErr(c, err)
}

func (c *runtimeCtx) resolveRecordAddInput(set map[string]bool) (io.Reader, func(), error) {
	opts := recordOptsOf(c)
	noop := func() {}
	if opts.jsonIn {
		if set["body-file"] || set["title-file"] {
			return nil, noop, usageErr(c.out, "record", "fyr record add: --json-in takes no --body-file or --title-file")
		}
		doc, err := brain.ParseRecordJSON(stdin)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		applyRecordStdinDoc(opts, doc, set)
		return strings.NewReader(doc.Body), noop, nil
	}
	if opts.titleFile != "" {
		if opts.title != "" {
			return nil, noop, usageErr(c.out, "record", "fyr record add takes --title or --title-file, not both")
		}
		t, err := readTitleFile(opts.titleFile)
		if err != nil {
			return nil, noop, recordErr(c, err)
		}
		opts.title = t
	}
	body, closeBody, err := openBodyReader(opts.bodyFile, set["body-file"])
	if err != nil {
		return nil, noop, recordErr(c, err)
	}
	if !set["body-file"] {
		doc, rest, perr := brain.ParseRecordStdin(body)
		if perr != nil {
			closeBody()
			return nil, noop, recordErr(c, perr)
		}
		applyRecordStdinDoc(opts, doc, set)
		body = rest
	}
	return body, closeBody, nil
}

func applyRecordStdinDoc(opts *recordOpts, doc brain.RecordStdinDoc, set map[string]bool) {
	if !set["title"] && opts.title == "" && doc.Title != "" {
		opts.title = doc.Title
	}
	if !set["status"] && doc.Status != "" {
		opts.status = doc.Status
	}
	if !set["id"] && doc.ID != "" {
		opts.id = doc.ID
	}
	if !set["project"] && doc.Project != "" {
		opts.project = doc.Project
	}
}

// recordEdit serves `record edit`: only what the command line set changes;
// a new title or body gets the redactor warning, a kept one does not.
func recordEdit(c *runtimeCtx, o brain.Options, id string, set map[string]bool) error {
	opts := recordOptsOf(c)
	if opts.body && set["body-file"] {
		return usageErr(c.out, "record", "fyr record edit takes --body or --body-file, not both")
	}
	if opts.jsonIn && (opts.body || set["body-file"]) {
		return usageErr(c.out, "record", "fyr record edit: --json-in takes no --body or --body-file")
	}
	in := brain.EditInput{ID: id}
	if set["status"] {
		in.Status = &opts.status
	}
	if set["title"] {
		in.Title = &opts.title
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

func (c *runtimeCtx) resolveRecordEditInput(in *brain.EditInput, seen *strings.Builder, set map[string]bool) (func(), error) {
	opts := recordOptsOf(c)
	noop := func() {}
	if opts.jsonIn {
		return noop, c.resolveRecordEditJSON(in, seen)
	}
	if !opts.body && !set["body-file"] {
		return noop, nil
	}
	body, closeBody, err := openBodyReader(opts.bodyFile, set["body-file"])
	if err != nil {
		return noop, recordErr(c, err)
	}
	if !set["body-file"] {
		rest, perr := applyRecordEditStdin(in, body)
		if perr != nil {
			closeBody()
			return noop, recordErr(c, perr)
		}
		body = rest
	}
	in.Body = teeBodyFrom(body, seen)
	return closeBody, nil
}

func (c *runtimeCtx) resolveRecordEditJSON(in *brain.EditInput, seen *strings.Builder) error {
	doc, err := brain.ParseRecordEditJSON(stdin)
	if err != nil {
		return recordErr(c, err)
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
	return nil
}

func applyRecordEditStdin(in *brain.EditInput, body io.Reader) (io.Reader, error) {
	doc, rest, err := brain.ParseRecordStdin(body)
	if err != nil {
		return nil, err
	}
	if in.Title == nil && doc.Title != "" {
		t := doc.Title
		in.Title = &t
	}
	if in.Status == nil && doc.Status != "" {
		s := doc.Status
		in.Status = &s
	}
	return rest, nil
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

// teeBodyFrom wraps r so what the brain store reads is kept in seen for the
// redactor warning.
func teeBodyFrom(r io.Reader, seen *strings.Builder) io.Reader { return io.TeeReader(r, seen) }

// openBodyReader returns the reader for a record or memory body: stdin when
// --body-file was not set, or the regular file at path when it was, refusing
// piped stdin alongside --body-file.
func openBodyReader(path string, set bool) (io.Reader, func(), error) {
	if !set {
		return stdin, func() {}, nil
	}
	if brain.StdinPiped(stdin) {
		return nil, nil, brain.RefusePipedBodyFile()
	}
	f, err := brain.OpenBodyFile(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}

// warnRedactable names on stderr, after the store, an ID given with --id, a
// title or a body that holds text the redactor would change: a project's
// records reach .agents/trail.jsonl when exported, and the operator chose a
// warning over a refusal (SD42 of the 2026-10-27 audit; the ID, ledger D31
// 3b). The record stays stored and the exit status is kept. The text itself
// is never printed, the ID's neither (stdout still prints it, as add's
// output): for the body, only its first line, as read from stdin, where the
// redacted body differs. A field that was not set is passed as "" and never
// warns.
func warnRedactable(id, title, body string) {
	if redact.Redact(id) != id {
		fmt.Fprintln(stderr, "fyr record: warning: the ID holds text the redactor would change; stored as written")
	}
	if redact.Redact(title) != title {
		fmt.Fprintln(stderr, "fyr record: warning: the title holds text the redactor would change; stored as written")
	}
	if n := firstRedacted(body); n > 0 {
		fmt.Fprintf(stderr, "fyr record: warning: the body holds text the redactor would change (first at line %d); stored as written\n", n)
	}
}

type redactWarnTarget struct {
	sub          string
	id           string
	extraID      string
	fallbackName string
	idLabel      string
	title        string
	body         string
	tail         string
}

// warnRecordRedactable emits per-record ID/title/body redaction warnings to
// stderr without printing any secret text.
func warnRecordRedactable(t redactWarnTarget) {
	idHot := redact.Redact(t.id) != t.id || (t.extraID != "" && redact.Redact(t.extraID) != t.extraID)
	titleHot := redact.Redact(t.title) != t.title
	name := t.id
	if idHot || titleHot {
		name = t.fallbackName
	}
	warn := func(what, where string) {
		fmt.Fprintf(stderr, "fyr record %s: warning: %s: %s holds text the redactor would change%s%s\n", t.sub, name, what, where, t.tail)
	}
	if idHot {
		warn(t.idLabel, "")
	}
	if titleHot {
		warn("the title", "")
	}
	if n := firstRedacted(t.body); n > 0 {
		warn("the body", fmt.Sprintf(" (first at line %d)", n))
	}
}

// warnImportRedactable is warnRedactable for `record import`: one line per
// record it stored (or, in a dry run, would store) whose ID, source path,
// title or body holds text the redactor would change, never printing that
// text (ledger item 47). An ID is made from the file name or, for a global
// log line, the title, and slugging can hide a secret from the redactor, so
// a record whose ID, path or title trips it is named by its place in the
// list printImport printed, never by its ID (the phase's security review,
// rounds 1 and 2); any other is named by its ID. The exit status is kept.
func warnImportRedactable(res *brain.ImportResult, dryRun bool) {
	tail := "; stored as written"
	if dryRun {
		tail = ""
	}
	for i, r := range res.Records {
		title, body := r.Text()
		warnRecordRedactable(redactWarnTarget{
			sub:          "import",
			id:           r.ID,
			extraID:      r.Path,
			fallbackName: fmt.Sprintf("record %d in the list above", i+1),
			idLabel:      "the ID or the source path",
			title:        title,
			body:         body,
			tail:         tail,
		})
	}
}

// warnSyncRedactable warns on stderr when a record loaded by `record sync`
// holds text the redactor would change, without printing the text itself.
func warnSyncRedactable(recs []brain.Record) {
	for i, r := range recs {
		warnRecordRedactable(redactWarnTarget{
			sub:          "sync",
			id:           r.ID,
			fallbackName: fmt.Sprintf("record %d in .agents/trail.jsonl", i+1),
			idLabel:      "the ID",
			title:        r.Title,
			body:         r.Body,
			tail:         "; stored as written",
		})
	}
}

// firstRedacted is the 1-based line of the first change the redactor makes
// to body, or 0 when it makes none.
func firstRedacted(body string) int {
	red := redact.Redact(body)
	if red == body {
		return 0
	}
	a, b := strings.Split(body, "\n"), strings.Split(red, "\n")
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n + 1
}

// recordExport serves `record export`: writes the project's exported-kind
// records from brain.db to .agents/trail.jsonl on demand.
func recordExport(c *runtimeCtx, o brain.Options) error {
	n, err := brain.Export(o)
	if err == nil {
		fmt.Fprintf(c.out, "export: %d records written to .agents/trail.jsonl\n", n)
	}
	return recordErr(c, err)
}

// recordSync serves `record sync`: loads .agents/trail.jsonl into brain.db and
// its FTS5 index on demand.
func recordSync(c *runtimeCtx, o brain.Options) error {
	priorCount := 0
	if !o.Global {
		if prior, ok, rerr := brain.ReadScope(o.Home, o.Dir); rerr == nil && ok {
			priorCount = len(prior)
		}
	}
	recs, err := brain.Sync(o)
	if err == nil {
		fmt.Fprintf(c.out, "sync: %d records loaded from .agents/trail.jsonl\n", len(recs))
		warnSyncRedactable(recs)
		if priorCount > len(recs) {
			fmt.Fprintf(stderr, "fyr record sync: hint: brain.db had %d exported records for this project vs %d in .agents/trail.jsonl; run `fyr record export` when brain.db is newer\n", priorCount, len(recs))
		}
	}
	return recordErr(c, err)
}

// recordImport serves `record import`: the project's trail, or with
// --global the home's old logs.
func recordImport(c *runtimeCtx, o brain.Options, rest []string) error {
	opts := recordOptsOf(c)
	var res *brain.ImportResult
	var err error
	if opts.global {
		res, err = brain.ImportGlobal(o, opts.dryRun)
	} else {
		docs := ""
		if len(rest) == 1 {
			docs = rest[0]
		}
		res, err = brain.Import(o, docs, opts.dryRun)
	}
	if err == nil {
		printImport(c, res)
		warnImportRedactable(res, opts.dryRun)
	}
	return recordErr(c, err)
}

// printImport is what an import did, and every file the caller removes by
// name afterwards (D13): the import itself removes nothing. Each path is one
// shell word (brain.ShellQuote), and git is told to read them literally: a
// tracked name can hold a glob or start with a dash.
func printImport(c *runtimeCtx, res *brain.ImportResult) {
	p := func(format string, a ...any) { fmt.Fprintln(c.out, search.Printable(fmt.Sprintf(format, a...))) }
	if recordOptsOf(c).dryRun {
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
	printImportPaths(p, "remove with git --literal-pathspecs rm -- and these paths (%d files):", res.Remove)
	printImportPaths(p, "remove with rm by name, untracked memory store files (%d):", res.Memory)
	printImportPaths(p, "read from ~/.config/fyr; remove by name once the records are checked (%d):", res.Sources)
}

func printImportPaths(p func(string, ...any), header string, paths []string) {
	if len(paths) == 0 {
		return
	}
	p(header, len(paths))
	for _, f := range paths {
		p("%s", brain.ShellQuote(f))
	}
}
