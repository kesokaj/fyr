package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// evalRecord is what the testing record is written from: the run's
// options, install source and keys, and its trials with their tally.
type evalRecord struct {
	root            string
	o               eval.Options
	src             harness.Source
	cli, key, model string
	runs            int
	results         []eval.Result
	tally           tally
}

// body is the testing record's body (SPEC-fyr-eval § The testing record):
// what ran against what, how it is counted, one table row per trial and
// one per suite row. No output of the CLI or a check goes in it.
func (e evalRecord) body() string {
	var b strings.Builder
	fixture := e.o.Fixture
	if fixture == "" {
		fixture = e.root
	}
	source := "embedded " + e.src.Version
	if e.src.Dir != "" {
		source = e.src.Dir
		// A tree Dirty cannot read is labelled so, never shown as clean
		// (security review of the 2026-10-27 fix).
		if dirty, err := trail.Dirty(e.src.Dir); err != nil {
			source += " (dirty: unknown, " + search.Printable(err.Error()) + ")"
		} else if dirty {
			source += " (dirty)"
		}
	}
	fmt.Fprintf(&b, "# fyr eval — %s\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- fyr: %s\n", buildVersion())
	fmt.Fprintf(&b, "- project: %s at %s\n", e.root, gitShort(e.root))
	fmt.Fprintf(&b, "- fixture: %s at %s\n", fixture, gitShort(fixture))
	fmt.Fprintf(&b, "- install source: %s\n", source)
	fmt.Fprintf(&b, "- runs per row: %d; estimator: c/n over graded trials; pass^k = C(c,k)/C(n,k)\n", e.runs)
	fmt.Fprintf(&b, "- model: %s; agy: %s; harness: %s\n", search.Printable(e.model), e.cli, e.key)
	fmt.Fprintf(&b, "- interval: Wilson 95%%, per row below and over rows for the suite: %s\n", e.tally.suiteInterval())
	if !eval.ProcsChecked {
		b.WriteString("- the leftover-process check did not run: no /proc here (plan fyr-eval O1)\n")
	}
	b.WriteString("\n| row | trial | status | cause | reasons | agy exit | seconds | session s | sandbox |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range e.results {
		cause, reasons, session := orDash(r.Cause), orDash(strings.Join(r.Reasons, "; ")), "-"
		if r.SessionSeconds >= 0 {
			session = fmt.Sprint(r.SessionSeconds)
		}
		fmt.Fprintf(&b, "| `%s` | %d | %s | %s | %s | %d | %d | %s | `%s` |\n", r.Row, r.Trial, r.Status, cause, strings.ReplaceAll(reasons, "|", `\|`), r.AgyExit, r.Seconds, session, r.Dir)
	}
	b.WriteString("\n| row | n | c | errors | wilson95 |\n|---|---|---|---|---|\n")
	for _, rt := range e.tally.rows {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %s |\n", rt.row, rt.n, rt.c, rt.errors, rt.interval())
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// gitShort is dir's HEAD as a short hash, or "unknown".
func gitShort(dir string) string {
	out, err := trail.Command(dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// agyVersion is the first line of `agy --version`, bounded so a CLI that
// hangs cannot hold the record back.
func agyVersion() string {
	cx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cx, "agy", "--version").Output()
	if err != nil && len(out) == 0 {
		if errors.Is(cx.Err(), context.DeadlineExceeded) {
			return "unknown (agy --version timed out)"
		}
		return "unknown"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}
