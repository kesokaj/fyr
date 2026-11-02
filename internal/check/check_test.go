package check

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repo is a scratch checkout holding files, every one committed.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "fixture")
	return dir
}

// trailLine is one valid trail.jsonl line.
func trailLine(t *testing.T, id, kind, status, body string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"date": "2026-10-25", "id": id, "kind": kind, "status": status, "title": "t", "updated": "2026-10-25T10:00:00Z", "body": body})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// vocab is a small binary: record with subverbs, a plain verb with a flag,
// and help.
var vocab = Vocabulary{
	"help":   {Flags: map[string]bool{"home": true, "repo": true}},
	"search": {Flags: map[string]bool{"home": true, "repo": true, "kind": true}},
	"check":  {Flags: map[string]bool{"home": true, "repo": true}},
	"record": {Flags: map[string]bool{"home": true, "repo": true, "title": true, "global": true}, Subverbs: map[string]map[string]bool{
		"get": {"home": true, "repo": true, "global": true},
		"add": {"home": true, "repo": true, "title": true},
	}},
}

func run(t *testing.T, dir string) Report {
	t.Helper()
	rep, err := Run(dir, vocab)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

// lines renders findings as "where:line rule" for comparison.
func lines(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, fmt.Sprintf("%s:%d %s", f.Where, f.Line, f.Rule))
	}
	return out
}

func same(t *testing.T, got []Finding, want ...string) {
	t.Helper()
	g := lines(got)
	sort.Strings(want)
	gs := append([]string(nil), g...)
	sort.Strings(gs)
	if strings.Join(gs, "\n") != strings.Join(want, "\n") {
		t.Errorf("findings:\n%s\nwant:\n%s\nfull: %+v", strings.Join(gs, "\n"), strings.Join(want, "\n"), got)
	}
}

// The scanner reads single- and double-backtick spans and backtick and
// tilde fences of any info string, joins a `\` continuation, and reads
// neither prose nor an indented block (D15).
func TestScanReadsSpansAndFences(t *testing.T) {
	text := strings.Join([]string{
		"prose fyr nope here, then `one` and ``two `x` two``",
		"an unclosed `span stays prose",
		"",
		"```bash",
		"fenced a \\",
		"  joined",
		"```",
		"~~~ any info",
		"tilde ```inside```",
		"~~~",
		"",
		"    indented block",
		"    still indented",
		"",
		"- a bullet with `bullet span`",
		"    - a nested bullet with `nested span`",
		"````",
		"open to the end",
	}, "\n")
	var got []string
	for _, c := range scan(text) {
		got = append(got, fmt.Sprintf("%d %v %s", c.line, c.fence, c.text))
	}
	want := []string{
		"1 false one",
		"1 false two `x` two",
		"5 true fenced a    joined",
		"9 true tilde ```inside```",
		"15 false bullet span",
		"16 false nested span",
		"18 true open to the end",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("scan:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// One finding each for an unknown verb, subverb and flag, in a span and in a
// fence; none for a placeholder, a program word that is not fyr, prose, a
// `;` inside a span (D28), or ROADMAP.md, TODO.md and PLAN.md (D6; PLAN.md
// item 38).
func TestDocsRuleVerbs(t *testing.T) {
	dir := repo(t, map[string]string{
		"README.md": strings.Join([]string{
			"`fyr frobnicate` and `fyr record frob` and `fyr search --nope`",
			"```",
			"$ FYR_X=1 fyr --home /h frobnicate",
			"go run ./cmd/fyr record frob",
			"cd x && ./fyr search --kind k --nope2=v",
			"```",
			"fine: `fyr <verb>`, `cmd/fyr the binary`, `~/.config/fyr exist`, `fyr-bin x`, `fyr record get --global`, `fyr record add --title T`, `fyr check`",
			"prose names fyr frobnicate outside code",
			"`keep ~/.claude: 4 files; fyr never removes them`",
			"`fyr record get --title`",
			"```",
			"fyr index; fyr gone",
			"```",
		}, "\n"),
		"ROADMAP.md": "`fyr eval` is next\n",
		"TODO.md":    "`fyr eval` is next\n",
		"PLAN.md":    "`fyr eval` and `fyr metrics evals` land in P7\n",
	})
	rep := run(t, dir)
	same(t, rep.Findings,
		"README.md:1 verb", "README.md:1 subverb", "README.md:1 flag",
		"README.md:3 verb", "README.md:4 subverb", "README.md:5 flag",
		"README.md:10 flag",
		"README.md:12 verb", "README.md:12 verb",
	)
}

// The path rule (D11, D19, D26): a missing nested path, a :N past the end,
// a :N on a directory; no candidate for an untracked top-level entry, a Go
// identifier, an ignored path or a placeholder; a file under skills/ is
// scanned, and a root file naming skills/… is checked.
func TestDocsRulePaths(t *testing.T) {
	dir := repo(t, map[string]string{
		".gitignore":         ".agents/scratch/\n",
		"internal/here.go":   "package x\n\nfunc A() {}\n",
		"skills/x/SKILL.md":  "deployed: `internal/gone2.go` and `internal/here.go:2`\n",
		"cmd/fyr/main.go":    "package main\n",
		".agents/kit/run.sh": "#!/bin/sh\n",
		"README.md": strings.Join([]string{
			"`internal/gone.go` `internal/here.go:4` `internal/here.go:2-9` `internal/here.go:3` `internal:1`",
			"`web/` `crypto/tls` `internal/trail.Command` `.agents/scratch/x` `<clone>/internal/x.go` `./internal/here.go` `.agents/trail.jsonl`",
			"`skills/x/SKILL.md` `skills/y/SKILL.md` `internal/` `README.md` `go run ./cmd/fyr` `https://x/internal/a`",
		}, "\n"),
	})
	rep := run(t, dir)
	same(t, rep.Findings,
		"README.md:1 path", "README.md:1 line", "README.md:1 line", "README.md:1 line",
		"README.md:3 path",
		"skills/x/SKILL.md:1 path",
	)
}

// A path through a symlinked directory that leaves the checkout is refused,
// never followed: a finding, and the file outside is not read.
func TestDocsRuleSymlinkOutOfRoot(t *testing.T) {
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte("one\n"), 0o644)
	dir := repo(t, map[string]string{"internal/a.go": "package a\n", "README.md": "`internal/out/secret.md` `internal/out/secret.md:1`\n"})
	if err := os.Symlink(outside, filepath.Join(dir, "internal", "out")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "internal", "out")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture's symlink is not there: %v", err)
	}
	rep := run(t, dir)
	same(t, rep.Findings, "README.md:1 path", "README.md:1 path")
	for _, f := range rep.Findings {
		if !strings.Contains(f.Text, "does not resolve inside this checkout") {
			t.Errorf("finding %q does not say the path leaves the checkout", f.Text)
		}
	}
}

// A vendored skill (skills/VENDORED.md) and a directory holding either
// noindex marker are not in the docs set (D5, D20).
func TestDocsRuleSkipsVendoredAndNoIndex(t *testing.T) {
	dir := repo(t, map[string]string{
		"skills/VENDORED.md":         "| Skill | Source |\n|-------|--------|\n| grilling | x |\n",
		"skills/grilling/SKILL.md":   "`fyr frobnicate` `internal/gone.go`\n",
		"skills/own/SKILL.md":        "`fyr frobnicate`\n",
		"data/new/.fyr-noindex":      "",
		"data/new/sub/a.md":          "`fyr frobnicate`\n",
		"data/old/.sh-agent-noindex": "",
		"data/old/a.md":              "`fyr frobnicate`\n",
		"internal/a.go":              "package a\n",
	})
	same(t, run(t, dir).Findings, "skills/own/SKILL.md:1 verb")
	// An untracked marker counts too.
	os.WriteFile(filepath.Join(dir, "skills", "own", ".fyr-noindex"), nil, 0o644)
	same(t, run(t, dir).Findings)
}

// A current spec, guide or deploy record is checked; a change record and a
// retired spec are not. A bad trail line is a refusal naming the line.
func TestDocsRuleRecords(t *testing.T) {
	trail := trailLine(t, "CHANGE-2026-10-25-x", "change", "closed", "`fyr frobnicate`") +
		trailLine(t, "GUIDE-a", "guide", "current", "line one\n`fyr frobnicate` `internal/gone.go`") +
		trailLine(t, "SPEC-old", "spec", "retired", "`fyr frobnicate`")
	dir := repo(t, map[string]string{".agents/trail.jsonl": trail, "internal/a.go": "package a\n"})
	same(t, run(t, dir).Findings, "GUIDE-a:2 verb", "GUIDE-a:2 path")

	bad := repo(t, map[string]string{".agents/trail.jsonl": trail + "{\"id\": \n"})
	_, err := Run(bad, vocab)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("a bad trail: %v, want a refusal naming line 4", err)
	}
}

// Outside a checkout Run refuses and checks nothing; a checkout with no
// trail gives the note.
func TestRunRefusesANonCheckout(t *testing.T) {
	_, err := Run(t.TempDir(), vocab)
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "is not a git checkout") {
		t.Fatalf("a plain directory: %v, want a refusal", err)
	}
	rep := run(t, repo(t, map[string]string{"a.txt": "x\n"}))
	if len(rep.Findings) != 0 || !strings.Contains(strings.Join(rep.Notes, "\n"), "no .agents/trail.jsonl") {
		t.Errorf("an empty checkout: %+v", rep)
	}
}

// Findings come in rule order, then by place and line.
func TestFindingsSorted(t *testing.T) {
	fs := []Finding{
		{"PRD.md", 1, "trace", ""}, {"b.md", 2, "requirement", ""}, {"SPEC-a", 0, "spec-stale", ""}, {"b.md", 1, "path", ""}, {"a.md", 9, "verb", ""},
	}
	sortFindings(fs)
	if got := strings.Join(lines(fs), ","); got != "a.md:9 verb,b.md:1 path,SPEC-a:0 spec-stale,b.md:2 requirement,PRD.md:1 trace" {
		t.Errorf("order %s", got)
	}
	if d, s, r, tr := Counts(fs); d != 2 || s != 1 || r != 1 || tr != 1 {
		t.Errorf("counts %d %d %d %d", d, s, r, tr)
	}
}

// git check-ignore dies on a path beyond a symbolic link; such a path must
// not take the ignore answer of every other candidate with it (the real
// checkout's `.agents/skills` link reported every scratch path).
func TestDocsRuleIgnoreSurvivesASymlinkedParent(t *testing.T) {
	dir := repo(t, map[string]string{
		".gitignore":        ".agents/scratch/*\n",
		".agents/kit/a.sh":  "#!/bin/sh\n",
		"skills/x/SKILL.md": "x\n",
		"README.md":         "`.agents/skills/x/SKILL.md` `.agents/scratch/gone.md` `.agents/skills/gone.md`\n",
	})
	if err := os.Symlink("../skills", filepath.Join(dir, ".agents", "skills")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".agents/skills")
	git(t, dir, "commit", "-q", "-m", "link")
	cmd := exec.Command("git", "-C", dir, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(".agents/skills/x/SKILL.md\x00.agents/scratch/gone.md\x00")
	if _, err := cmd.Output(); err == nil {
		t.Fatal("the fixture does not make a batched git check-ignore fail")
	}
	same(t, run(t, dir).Findings, "README.md:1 path")
}

// A tracked doc the check cannot read (a link, here) is named in a note,
// not skipped in silence (IN53 of the 2026-10-27 audit).
func TestSkippedDocIsNoted(t *testing.T) {
	dir := repo(t, map[string]string{"README.md": "x\n", ".agents/trail.jsonl": trailLine(t, "ADR-063", "adr", "accepted", "x")})
	if err := os.Symlink("README.md", filepath.Join(dir, "LINK.md")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "LINK.md")
	git(t, dir, "commit", "-q", "-m", "link")
	rep := run(t, dir)
	found := false
	for _, n := range rep.Notes {
		if strings.Contains(n, "skipped") && strings.Contains(n, "LINK.md") {
			found = true
		}
	}
	if !found {
		t.Errorf("notes %q do not name the skipped LINK.md", rep.Notes)
	}
}
