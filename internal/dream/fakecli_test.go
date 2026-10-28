package dream

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/search"
)

// fakeCLI puts an executable named name first on PATH. It records its
// arguments, working directory, stdin and environment under rec, prints out
// and exits with code.
func fakeCLI(t *testing.T, name, out string, code int) (rec string) {
	t.Helper()
	bin := t.TempDir()
	rec = t.TempDir()
	if err := os.WriteFile(filepath.Join(rec, "out"), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"" + rec + "/args\"\n" +
		"pwd > \"" + rec + "/cwd\"\n" +
		"cat > \"" + rec + "/stdin\"\n" +
		"printf '%s' \"$HOME\" > \"" + rec + "/home\"\n" +
		"env > \"" + rec + "/env\"\n" +
		"cp \"$HOME/.gemini/antigravity-cli/settings.json\" \"" + rec + "/settings\" 2>/dev/null\n" +
		"ls \"$HOME/.gemini/antigravity-cli\" > \"" + rec + "/agyfiles\" 2>/dev/null\n" +
		"cat \"" + rec + "/out\"\n" +
		"exit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return rec
}

const canned = `Here is the answer.
{"proposals":[
 {"title":"Vet before tag","kind":"rule","target":"project","file":"AGENTS.md","text":"Run go vet before every tag.","evidence":"memory/deploy-day.md"},
 {"title":"Release skill","kind":"skill","target":"harness","file":"skills/release/SKILL.md","text":"A release skill.","evidence":"trail changes/x.md"}],
 "skills":[{"name":"release-steps","description":"cut a release","body":"# Release\n1. vet"},{"name":"../escape","description":"bad","body":"x"}]}
trailing words`

func runOpts(f fixture, out *bytes.Buffer) Options {
	return Options{Home: f.home, All: true, Out: out, Now: func() time.Time { return now },
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil },
		Scopes:   func(string) ([]string, error) { return nil, nil },
		Active:   idle}
}

// idle is Options.Active for a test about something else: every project
// counts as idle (the idle rule's own tests use the real check).
func idle(string, search.Project, time.Time) string { return "" }

// dreamProposals is every proposal record the dream stored in home, newest
// first, bodies included.
func dreamProposals(t *testing.T, home string) []brain.Record {
	t.Helper()
	recs, err := brain.List(brain.Options{Home: home, Dir: t.TempDir(), Global: true}, brain.ListFilter{Kind: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

func titled(recs []brain.Record, prefix string) (brain.Record, bool) {
	for _, r := range recs {
		if strings.HasPrefix(r.Title, prefix) {
			return r, true
		}
	}
	return brain.Record{}, false
}

func scaffold(t *testing.T, f fixture) {
	t.Helper()
	os.MkdirAll(filepath.Join(f.root, ".agents", "docs"), 0o755)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
