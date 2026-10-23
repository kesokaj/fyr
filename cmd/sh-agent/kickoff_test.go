package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const kickoffFixtureBlock = "Project: /srv/demo. Read AGENTS.md, then .agents/docs/handoffs/2026-10-02-0900-new.md, then .agents/docs/plans/p.md; do Phase 2 — The metric only.\n" +
	"Standing approvals (closed set): the phase's boxes marked who: AI and the tests they name; the checkpoint's `changes/` entry, commit and push; the closing handoff. Never: a who: user box, an Open: row, a release, a deploy, a real-home install, anything outside this repository. Ask only outside the set or on a critical failure.\n" +
	"Done when: metric visible. Then write the handoff with the next Kickoff and end the session.\n"

func kickoffHandoff(block string) string {
	return "---\nowner: main\ndate: 2026-10-02\n---\n# Handoff\n\n## Next Steps\n\n1. x\n\n## Kickoff\n\n```text\n" + block + "```\n"
}

// kickoffFixture is a checkout with a docs root (`.agents/docs` unless docs
// names another) and the handoffs given as name → file text.
func kickoffFixture(t *testing.T, docs string, handoffs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if docs == "" {
		docs = ".agents/docs"
	}
	dir := filepath.Join(root, docs, "handoffs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range handoffs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The newest handoff by its file-name stamp wins (date, then HHMM; an older
// `YYYY-MM-DD-<slug>.md` name counts as 0000 that day), a file without a stamp
// is not a handoff, and the verb finds the checkout from a subdirectory: the
// block is printed without its fence and nothing else.
func TestKickoffPrintsTheNewestHandoffBlock(t *testing.T) {
	for name, docs := range map[string]string{"agents docs root": ".agents/docs", "bare docs root": "docs"} {
		t.Run(name, func(t *testing.T) {
			root := kickoffFixture(t, docs, map[string]string{
				"2026-10-01-1200-old.md":   kickoffHandoff("OLD\n"),
				"2026-10-02-older-form.md": kickoffHandoff("OLDER FORM\n"),
				"2026-10-02-0900-new.md":   kickoffHandoff(kickoffFixtureBlock),
				"README.md":                "# not a handoff\n\n## Kickoff\n\n```text\nREADME\n```\n",
			})
			sub := filepath.Join(root, "internal", "x")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(sub)
			var out bytes.Buffer
			if err := run([]string{"kickoff"}, &out); err != nil {
				t.Fatalf("kickoff: %v\n%s", err, out.String())
			}
			if out.String() != kickoffFixtureBlock {
				t.Errorf("kickoff printed\n%s\nwant\n%s", out.String(), kickoffFixtureBlock)
			}
		})
	}
}

// A Kickoff written as prose instead of the template's fenced block is still
// what was queued (D33): the section's text is printed, blank lines around it
// dropped, and the next heading ends it.
func TestKickoffPrintsAProseSection(t *testing.T) {
	root := kickoffFixture(t, "", map[string]string{
		"2026-10-23-1448-h.md": "# Handoff\n\n## Next Steps\n\n1. x\n\n## Kickoff\n\nAdd the line to CONTRIBUTING.md.\nThen commit.\n\n## Cross-references\n\n- y\n",
	})
	t.Chdir(root)
	var out bytes.Buffer
	if err := run([]string{"kickoff"}, &out); err != nil {
		t.Fatalf("kickoff: %v\n%s", err, out.String())
	}
	if want := "Add the line to CONTRIBUTING.md.\nThen commit.\n"; out.String() != want {
		t.Errorf("kickoff printed %q, want %q", out.String(), want)
	}
}

// Nothing queued — the nothing-queued line, no Kickoff section, an empty
// block, no handoff, no docs root — is one plain error, `kickoff: nothing
// queued…`, with nothing on stdout: `$(sh-agent kickoff)` must not hand a CLI
// the message as its prompt.
func TestKickoffNothingQueued(t *testing.T) {
	for name, handoffs := range map[string]map[string]string{
		"nothing-queued line": {"2026-10-02-0900-h.md": "# Handoff\n\n## Kickoff\n\nNothing queued; start the next task in a fresh conversation.\n"},
		"nothing-queued line, lower case, before another section": {"2026-10-02-0900-h.md": "# Handoff\n\n## Kickoff\n\nnothing queued.\n\n## Cross-references\n\n- y\n"},
		"a blank section":    {"2026-10-02-0900-h.md": "# Handoff\n\n## Kickoff\n\n\n## Cross-references\n\n- y\n"},
		"no Kickoff section": {"2026-10-02-0900-h.md": "# Handoff\n\n## Next Steps\n\n1. x\n"},
		"empty block":        {"2026-10-02-0900-h.md": kickoffHandoff("")},
		"a Kickoff in an older handoff only": {
			"2026-10-01-0900-h.md": kickoffHandoff("OLD\n"),
			"2026-10-02-0900-h.md": "# Handoff\n\n## Kickoff\n\nNothing queued; start the next task in a fresh conversation.\n",
		},
		"no handoff": {},
	} {
		t.Run(name, func(t *testing.T) {
			root := kickoffFixture(t, "", handoffs)
			t.Chdir(root)
			var out bytes.Buffer
			err := run([]string{"kickoff"}, &out)
			var code exitCode
			if err == nil || errors.As(err, &code) {
				t.Fatalf("kickoff: err %v, want a plain error (exit 1)", err)
			}
			if !strings.HasPrefix(err.Error(), "kickoff: nothing queued") {
				t.Errorf("kickoff: err %q, want the prefix \"kickoff: nothing queued\"", err)
			}
			if out.Len() != 0 {
				t.Errorf("kickoff wrote %q to stdout; nothing may reach the prompt", out.String())
			}
		})
	}
	t.Run("no docs root", func(t *testing.T) {
		root := t.TempDir()
		if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		t.Chdir(root)
		var out bytes.Buffer
		err := run([]string{"kickoff"}, &out)
		if err == nil || !strings.HasPrefix(err.Error(), "kickoff: nothing queued") || out.Len() != 0 {
			t.Fatalf("kickoff outside a scaffold: err %v, stdout %q", err, out.String())
		}
	})
}

// The binary itself: nothing queued is exit 1, the reason on stderr with the
// `sh-agent:` prefix main gives every plain error, and an empty stdout. The
// helper runs in a directory that is not a checkout, so the result does not
// depend on this repository's own handoffs.
func TestKickoffNothingQueuedExitsOne(t *testing.T) {
	c := exec.Command(os.Args[0], "-test.run=^TestHelperMain$")
	c.Dir = t.TempDir()
	c.Env = append(os.Environ(), "SH_AGENT_HELPER_ARGS=kickoff")
	var stdout, stderr bytes.Buffer
	c.Stdout, c.Stderr = &stdout, &stderr
	err := c.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Fatalf("sh-agent kickoff: %v, want exit 1\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.HasPrefix(stderr.String(), "sh-agent: kickoff: nothing queued") {
		t.Errorf("stderr %q, want the prefix \"sh-agent: kickoff: nothing queued\"", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout %q, want nothing", stdout.String())
	}
}
