package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

const recordUsage = "usage: fyr record add <kind> --title T [--status S] [--id ID] [--global [--project ROOT]] (body on stdin)" +
	" | fyr record get <id>... [--global]" +
	" | fyr record edit <id> [--status S] [--title T] [--body] [--global]" +
	" | fyr record list [--kind K] [--open] [--global [--project ROOT]] [--all]"

// recordFlags is the flags each record subverb takes, besides --home and
// --repo: one it does not take is a usage error, never parsed and ignored
// (IN5's rule, per subverb).
var recordFlags = map[string]map[string]bool{
	"add":  {"title": true, "status": true, "id": true, "project": true, "global": true},
	"get":  {"global": true},
	"edit": {"status": true, "title": true, "body": true, "global": true},
	"list": {"kind": true, "open": true, "project": true, "all": true, "global": true},
}

// runRecord serves `record add|get|edit|list` (spec
// .agents/docs/specs/fyr/record.md); it needs no harness tree. Exit 0 with
// or without output, 1 a refusal, 2 a usage error, the reason on the writer.
func runRecord(c *ctx) error {
	args := c.fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(c.out, recordUsage)
		return exitCode(2)
	}
	sub, rest := args[0], args[1:]
	takes, ok := recordFlags[sub]
	if !ok {
		fmt.Fprintln(c.out, recordUsage)
		return exitCode(2)
	}
	set := map[string]bool{}
	var stray []string
	c.fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
		if f.Name != "home" && f.Name != "repo" && !takes[f.Name] {
			stray = append(stray, "--"+f.Name)
		}
	})
	if len(stray) > 0 {
		sort.Strings(stray)
		fmt.Fprintf(c.out, "fyr record %s does not take %s\n%s\n", sub, strings.Join(stray, ", "), recordUsage)
		return exitCode(2)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	o := brain.Options{Home: c.home, Dir: wd, Global: c.global}
	switch sub {
	case "add":
		if len(rest) != 1 {
			break
		}
		id, err := brain.Add(o, brain.AddInput{Kind: rest[0], Title: c.recTitle, Status: c.recStatus, ID: c.recID, Project: c.recProject, Body: stdin})
		if err == nil {
			fmt.Fprintln(c.out, id)
		}
		return recordErr(c, err)
	case "get":
		if len(rest) == 0 {
			break
		}
		recs, _, err := brain.Get(o, rest)
		for i, r := range recs {
			if i > 0 {
				fmt.Fprintln(c.out)
			}
			fmt.Fprint(c.out, brain.FormatGet(r))
		}
		return recordErr(c, err)
	case "edit":
		if len(rest) != 1 {
			break
		}
		in := brain.EditInput{ID: rest[0]}
		if set["status"] {
			in.Status = &c.recStatus
		}
		if set["title"] {
			in.Title = &c.recTitle
		}
		if c.recBody {
			in.Body = stdin
		}
		return recordErr(c, brain.Edit(o, in))
	case "list":
		if len(rest) != 0 {
			break
		}
		recs, err := brain.List(o, brain.ListFilter{Kind: c.kind, Open: c.openRecords, Project: c.recProject, All: c.recAll})
		for _, r := range recs {
			fmt.Fprintln(c.out, brain.FormatLine(r, c.recAll))
		}
		return recordErr(c, err)
	}
	fmt.Fprintln(c.out, recordUsage)
	return exitCode(2)
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
	return err
}

// recordHits prints the brain store's hits for a words search, before the
// file hits (ADR-063): nothing when there are none, and a one-line note when
// the records could not be searched (a trail.jsonl that does not parse), so
// the file search still answers.
func recordHits(c *ctx, o search.Options) {
	limit := o.Limit
	if limit == 0 {
		limit = 10
	}
	hits, err := brain.Search(brain.Options{Home: o.Home, Dir: o.Dir, Global: o.Global}, o.Words, o.Kind, limit)
	if err != nil {
		fmt.Fprintln(c.out, search.Printable("records: not searched: "+err.Error()))
		return
	}
	if len(hits) == 0 {
		return
	}
	fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("records: %d hits for %q", len(hits), strings.Join(o.Words, " "))))
	for _, h := range hits {
		fmt.Fprintf(c.out, "%s\n    %s\n", brain.FormatHit(h), h.Snippet)
	}
}
