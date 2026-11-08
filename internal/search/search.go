package search

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// LinkedRecord is a BUG-NNN or ADR-NNN record linked to a file or symbol.
type LinkedRecord struct {
	ID     string
	Status string
	Title  string
}

// Options are the flags of `fyr search` and `fyr index`.
type Options struct {
	Dir          string                             // working directory the command runs in
	Home         string                             // home directory (--home), for the global search index
	Words        []string                           // search words
	Refs         string                             // --refs target
	Open         bool                               // --open
	Kind         string                             // --kind filter
	Limit        int                                // --limit
	Global       bool                               // --global
	Rebuild      bool                               // index --rebuild
	Slice        string                             // --slice query or symbol
	Hops         int                                // --hops (default 2)
	Budget       int                                // --budget token limit (default 2000, chars/4)
	Session      string                             // --session conversation ID
	Skeleton     string                             // --skeleton file path
	Path         string                             // --path source symbol
	To           string                             // --to target symbol for --path
	Affected     string                             // --affected file path or symbol
	Map          bool                               // --map repo summary
	ActiveFiles  map[string]bool                    // optional in-memory active file set
	BrainRefs    func(target string) []LinkedRecord // optional callback to internal/brain.Refs
	BrainReprime func(dir, home string) string      // optional callback for compaction re-prime
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
		return usageError{fmt.Sprintf("search: --limit must be between 1 and %d; see fyr help search", maxLimit)}
	}
	if o.Kind != "" {
		ok := false
		for _, k := range kinds {
			ok = ok || k == o.Kind
		}
		if !ok {
			return usageError{fmt.Sprintf("search: --kind must be one of: %s; see fyr help search", strings.Join(kinds, " "))}
		}
	}
	// index takes no words and none of search's modes, and search no
	// --rebuild: the verb table refuses them before they reach here.
	if verb == "index" {
		return nil
	}
	return validateSearchModes(o)
}

func validateSearchModes(o *Options) error {
	if o.Path != "" && o.To == "" && len(o.Words) == 1 {
		o.To = o.Words[0]
		o.Words = nil
	}
	if o.Slice != "" && len(o.Words) > 0 {
		o.Slice = strings.TrimSpace(o.Slice + " " + strings.Join(o.Words, " "))
		o.Words = nil
	}
	modes := 0
	for _, set := range []bool{len(o.Words) > 0, o.Refs != "", o.Open, o.Slice != "", o.Skeleton != "", o.Path != "", o.Affected != "", o.Map} {
		if set {
			modes++
		}
	}
	switch {
	case modes > 1:
		return usageError{"search: words, --refs, --open, --slice, --skeleton, --path, --affected and --map exclude each other; see fyr help search"}
	case modes == 0:
		return usageError{"search: give words, --refs <BUG-NNN|ADR-NNN|path>, --open, --slice, --skeleton, --path, --affected or --map; see fyr help search"}
	}
	if o.Path != "" && o.To == "" {
		return usageError{"search: --path requires --to <symbol> or a target symbol argument; see fyr help search"}
	}
	if o.Hops == 0 {
		o.Hops = defaultSliceHops
	}
	if o.Hops < 0 || o.Hops > 6 {
		return usageError{"search: --hops must be between 0 and 6; see fyr help search"}
	}
	if o.Budget == 0 {
		o.Budget = defaultSliceBudget
	}
	if o.Budget < 50 {
		return usageError{"search: --budget must be at least 50 tokens; see fyr help search"}
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
	for _, t := range []string{"chunks", "refs"} {
		var n int
		q := `SELECT count(*) FROM ` + t + ` WHERE oid IN (SELECT oid FROM files WHERE project IN ` + sc.in() + `)`
		if err := sc.st.db.QueryRow(q, sc.keys()...).Scan(&n); err != nil {
			return err
		}
		counts[t] = n
	}
	fmt.Fprintf(out, "files    %d\nchunks   %d\nrefs     %d\nchanged  %d\nbytes    %d\nms       %d\n",
		sc.files, counts["chunks"], counts["refs"], sc.changed, sc.st.size(), sc.elapsed.Milliseconds())
	return nil
}

// Search answers one query: ranked words or --refs. --open is the brain
// store's alone (recordHits): a file holds no record, so it prints nothing
// here, after the same refusals as the other modes.
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
	case o.Slice != "":
		return sc.buildContextSlice(o, out)
	case o.Skeleton != "":
		return sc.buildFileSkeleton(o.Skeleton, out)
	case o.Path != "":
		return sc.findSymbolPath(o.Path, o.To, out)
	case o.Affected != "":
		return sc.findAffected(o, out)
	case o.Map:
		return sc.repoMapSummary(o, out)
	case o.Refs != "":
		return sc.refs(o, out)
	case o.Open:
		return nil
	}
	return sc.words(o, out)
}
