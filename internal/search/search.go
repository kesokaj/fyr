package search

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Options are the flags of `fyr search` and `fyr index`.
type Options struct {
	Dir     string   // working directory the command runs in
	Home    string   // home directory (--home), for the global search index
	Words   []string // search words
	Refs    string   // --refs target
	Open    bool     // --open
	Kind    string   // --kind filter
	Limit   int      // --limit
	Global  bool     // --global
	Rebuild bool     // index --rebuild
}

const (
	defaultLimit = 10
	maxLimit     = 50
	perFileCap   = 2
	snippetMax   = 200
)

var (
	errNotCheckout = errors.New("search: not inside a git checkout; use grep")
	errNotIgnored  = errors.New(`search: .agents/scratch/search.db is not gitignored; add ".agents/scratch/*" to .gitignore (/init-project does)`)
	idPattern      = regexp.MustCompile(`^(BUG|ADR)-\d{3}[a-z]?$`)
)

// usageError is a refusal of the verb's arguments: the CLI prints it on the
// caller's writer and exits 2, like every other usage error (BUG-168).
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// IsUsage reports whether err is a usage error from Search or Index.
func IsUsage(err error) bool {
	var u usageError
	return errors.As(err, &u)
}

func validate(o *Options, verb string) error {
	if o.Limit == 0 {
		o.Limit = defaultLimit
	}
	if o.Limit < 1 || o.Limit > maxLimit {
		return usageError{fmt.Sprintf("search: --limit must be between 1 and %d; see fyr help", maxLimit)}
	}
	if o.Kind != "" {
		ok := false
		for _, k := range kinds {
			ok = ok || k == o.Kind
		}
		if !ok {
			return usageError{fmt.Sprintf("search: --kind must be one of: %s; see fyr help", strings.Join(kinds, " "))}
		}
	}
	// index takes no words and none of search's modes, and search no
	// --rebuild: the verb table refuses them before they reach here.
	if verb == "index" {
		return nil
	}
	modes := 0
	for _, set := range []bool{len(o.Words) > 0, o.Refs != "", o.Open} {
		if set {
			modes++
		}
	}
	switch {
	case modes > 1:
		return usageError{"search: words, --refs and --open exclude each other; see fyr help"}
	case modes == 0:
		return usageError{"search: give words, --refs <BUG-NNN|ADR-NNN|path> or --open; see fyr help"}
	}
	return nil
}

// Index reconciles the index (or rebuilds it) and prints its counts.
func Index(o Options, out io.Writer) error {
	if err := validate(&o, "index"); err != nil {
		return err
	}
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	counts := map[string]int{}
	for _, t := range []string{"chunks", "records", "refs"} {
		var n int
		q := `SELECT count(*) FROM ` + t + ` WHERE oid IN (SELECT oid FROM files WHERE project IN ` + sc.in() + `)`
		if err := sc.st.db.QueryRow(q, sc.keys()...).Scan(&n); err != nil {
			return err
		}
		counts[t] = n
	}
	fmt.Fprintf(out, "files    %d\nchunks   %d\nrecords  %d\nrefs     %d\nchanged  %d\nbytes    %d\nms       %d\n",
		sc.files, counts["chunks"], counts["records"], counts["refs"], sc.changed, sc.st.size(), sc.elapsed.Milliseconds())
	return nil
}

// Search answers one query: ranked words, --refs or --open.
func Search(o Options, out io.Writer) error {
	if err := validate(&o, "search"); err != nil {
		return err
	}
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	switch {
	case o.Refs != "":
		return sc.refs(o, out)
	case o.Open:
		return sc.open(o, out)
	}
	return sc.words(o, out)
}
