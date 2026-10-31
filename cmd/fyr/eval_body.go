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

// evalBody is the testing record's body: what ran against what, and one
// table row per result. No output of the CLI or a check goes in it.
func evalBody(root string, o eval.Options, src harness.Source, cli string, results []eval.Result) string {
	var b strings.Builder
	fixture := o.Fixture
	if fixture == "" {
		fixture = root
	}
	source := "embedded " + src.Version
	if src.Dir != "" {
		source = src.Dir
		// A tree Dirty cannot read is labelled so, never shown as clean
		// (security review of the 2026-10-27 fix).
		if dirty, err := trail.Dirty(src.Dir); err != nil {
			source += " (dirty: unknown, " + search.Printable(err.Error()) + ")"
		} else if dirty {
			source += " (dirty)"
		}
	}
	fmt.Fprintf(&b, "# fyr eval — %s\n\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "- fyr: %s\n", buildVersion())
	fmt.Fprintf(&b, "- project: %s at %s\n", root, gitShort(root))
	fmt.Fprintf(&b, "- fixture: %s at %s\n", fixture, gitShort(fixture))
	fmt.Fprintf(&b, "- install source: %s\n", source)
	fmt.Fprintf(&b, "- agy: %s\n", cli)
	if !eval.ProcsChecked {
		b.WriteString("- the leftover-process check did not run: no /proc here (plan fyr-eval O1)\n")
	}
	b.WriteString("\n| row | verdict | reasons | agy exit | seconds | sandbox |\n|---|---|---|---|---|---|\n")
	for _, r := range results {
		reasons := strings.Join(r.Reasons, "; ")
		if reasons == "" {
			reasons = "-"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %d | %d | `%s` |\n", r.Row, r.Status, strings.ReplaceAll(reasons, "|", `\|`), r.AgyExit, r.Seconds, r.Dir)
	}
	return b.String()
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
