package eval

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The kinds of row (ADR-085): a regression row is expected to pass every
// time, an adversarial row tests a planted instruction or a lane, and an
// indicator row is reported but left out of the suite's rate.
const (
	KindCapability  = "capability"
	KindRegression  = "regression"
	KindAdversarial = "adversarial"
	KindIndicator   = "indicator"
)

var kinds = []string{KindCapability, KindRegression, KindAdversarial, KindIndicator}

// optionalHeader is the four columns a long suite header adds after the
// seven (ADR-085).
var optionalHeader = []string{"version", "kind", "cluster", "expect"}

// expectMax is the longest joined expect list: the trial's text bound in
// internal/metrics (trialText), so a parsed row is never refused at the store.
const expectMax = 120

var (
	// A version is plain digits, no sign and no leading zero, and fits an int
	// on every platform.
	validVersion = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	// identWord is SPEC-fyr-metrics' identifier rule (nameShape in
	// internal/metrics/writer.go), copied because this package links no
	// SQLite driver and so cannot import metrics (D10).
	identWord = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,63}$`)
)

// optionalCells reads the four optional cells of row r, line n, into r, with
// the defaults for an empty cell: version 1, kind capability, cluster the
// row's id, no expect.
func optionalCells(r *Row, cells []string, n int) error {
	version, kind, cluster, expect := textCell(cells[0]), textCell(cells[1]), textCell(cells[2]), textCell(cells[3])
	r.Version, r.Kind, r.Cluster = 1, KindCapability, r.ID
	if version != "" {
		if !validVersion.MatchString(version) {
			return fmt.Errorf("GUIDE-eval line %d: row %s: version %q: want an integer from 1, in plain digits", n, r.ID, version)
		}
		r.Version, _ = strconv.Atoi(version)
	}
	if kind != "" {
		if !slices.Contains(kinds, kind) {
			return fmt.Errorf("GUIDE-eval line %d: row %s: kind %q: want %s or %s", n, r.ID, kind, strings.Join(kinds[:len(kinds)-1], ", "), kinds[len(kinds)-1])
		}
		r.Kind = kind
	}
	if cluster != "" {
		if !identWord.MatchString(cluster) {
			return fmt.Errorf("GUIDE-eval line %d: row %s: cluster %q: want one word of A-Z, a-z, 0-9, '_', '.', ':', '@', '/' or '-'", n, r.ID, cluster)
		}
		r.Cluster = cluster
	}
	if expect != "" {
		joined, err := expectList(expect)
		if err != nil {
			return fmt.Errorf("GUIDE-eval line %d: row %s: %w", n, r.ID, err)
		}
		r.Expect = joined
	}
	return nil
}

// expectList is a comma-separated expect cell as stored: its entries
// trimmed and joined by ",". Each entry is a tool name or a normalised verb,
// words joined by single spaces, each word an identifier; none empty or
// repeated.
func expectList(cell string) (string, error) {
	var entries []string
	seen := map[string]bool{}
	for i, e := range strings.Split(cell, ",") {
		e = strings.TrimSpace(e)
		switch {
		case e == "":
			return "", fmt.Errorf("expect entry %d is empty", i+1)
		case seen[e]:
			return "", fmt.Errorf("expect entry %d repeats %s", i+1, e)
		case !verbShaped(e):
			return "", fmt.Errorf("expect entry %d %q: each word must be an identifier, one space between words", i+1, e)
		}
		seen[e] = true
		entries = append(entries, e)
	}
	joined := strings.Join(entries, ",")
	if len(joined) > expectMax {
		return "", fmt.Errorf("expect is %d bytes joined, over %d bytes", len(joined), expectMax)
	}
	return joined, nil
}

// verbShaped reports whether e is words of the identifier rule joined by
// single spaces.
func verbShaped(e string) bool {
	for _, w := range strings.Split(e, " ") {
		if !identWord.MatchString(w) {
			return false
		}
	}
	return true
}
