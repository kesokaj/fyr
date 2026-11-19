package main

import (
	"fmt"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

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
