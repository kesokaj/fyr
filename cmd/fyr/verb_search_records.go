package main

import (
	"fmt"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

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
