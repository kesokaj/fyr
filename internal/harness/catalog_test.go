package harness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The Google skills catalog (plan 2026-10-01-google-skills-catalog): the skills
// published by Google, vendored verbatim under skills/google-catalog/catalog/
// and reached through one router skill, because installed as global skills
// they push the harness's own skills out of the CLI's listing.
//
// CATALOG_DIR points the catalog tests at another directory and
// CATALOG_WRITE=1 makes the two derived files be written instead of compared:
// that is how scripts/refresh-catalog.sh builds INDEX.md and CHECKSUMS, so the
// index is derived by the installer's own frontmatter parser and nothing is
// parsed twice.

// catalogHarnessFiles are written by the harness, not vendored: CHECKSUMS does
// not cover them.
var catalogHarnessFiles = map[string]bool{"INDEX.md": true, "MANIFEST.md": true, "CHECKSUMS": true, ".fyr-noindex": true}

const catalogIndexHeader = "# Google skills catalog — index\n\nOne line per entry: the directory name under `catalog/`, then the start of its description. Grep this file for the product or task words, then read `catalog/<name>/SKILL.md`.\n\n"

// catalogWriteMode reports whether the derived files are to be written. That
// happens only on the refresh script's build directory: CATALOG_WRITE without
// CATALOG_DIR would regenerate the checked-in CHECKSUMS from whatever is on
// disk and turn the integrity test into its own answer (security review).
func catalogWriteMode(t *testing.T) bool {
	t.Helper()
	if os.Getenv("CATALOG_WRITE") != "1" {
		return false
	}
	if os.Getenv("CATALOG_DIR") == "" {
		t.Fatal("CATALOG_WRITE=1 needs CATALOG_DIR: the checked-in INDEX.md and CHECKSUMS are written only by the refresh script, into its build directory")
	}
	return true
}

func catalogDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("CATALOG_DIR"); d != "" {
		if os.Getenv("CATALOG_WRITE") != "1" {
			// Compare mode on another tree would pass for a catalog that is
			// not the checked-in one (security review T1).
			t.Fatal("CATALOG_DIR is only for the refresh script's write mode (CATALOG_WRITE=1)")
		}
		return d
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(repo, "skills", "google-catalog", "catalog")
}

// catalogEntries are the directories holding a SKILL.md, sorted. An empty
// catalog fails the caller: a test over zero files proves nothing.
func catalogEntries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	des, _ := os.ReadDir(dir)
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, de.Name(), "SKILL.md")); err == nil {
			out = append(out, de.Name())
		} else {
			t.Errorf("catalog entry %s: no SKILL.md", de.Name())
		}
	}
	if len(out) == 0 {
		t.Fatalf("the catalog is empty: no entry with a SKILL.md under %s", dir)
	}
	sort.Strings(out)
	return out
}

// catalogSums maps every vendored file (slash path relative to the catalog)
// to its sha256.
func catalogSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if catalogHarnessFiles[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		sums[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return sums
}

// TestCatalogMatchesChecksums pins the vendored tree to what the refresh
// fetched, in both directions: a changed or added file has no matching line,
// and a line with no file is a deletion.
func TestCatalogMatchesChecksums(t *testing.T) {
	dir := catalogDir(t)
	catalogEntries(t, dir)
	got := catalogSums(t, dir)
	file := filepath.Join(dir, "CHECKSUMS")
	if catalogWriteMode(t) {
		paths := make([]string, 0, len(got))
		for p := range got {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		var b strings.Builder
		for _, p := range paths {
			fmt.Fprintf(&b, "%s  %s\n", got[p], p)
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("no CHECKSUMS: %v", err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		sum, path, ok := strings.Cut(line, "  ")
		if !ok || len(sum) != 64 {
			t.Fatalf("CHECKSUMS line is not `<sha256>  <path>`: %q", line)
		}
		want[path] = sum
	}
	for p, sum := range got {
		switch w, ok := want[p]; {
		case !ok:
			t.Errorf("%s is in the catalog and not in CHECKSUMS", p)
		case w != sum:
			t.Errorf("%s differs from its CHECKSUMS line: the vendored file was edited", p)
		}
	}
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("CHECKSUMS names %s, which is not in the catalog", p)
		}
	}
}

// catalogIndexLine is one INDEX.md line: the entry's directory name and its
// description as Parse reads it, cut at 220 runes.
func catalogIndexLine(name, description string) string {
	r := []rune(description)
	if len(r) > 220 {
		r = r[:220]
	}
	return "- `" + name + "` — " + strings.TrimSpace(string(r))
}

// TestCatalogIndexMatchesEntries re-derives the index from every entry's
// frontmatter through the installer's own parser: a missing line, an extra
// line and a stale description all differ from it.
func TestCatalogIndexMatchesEntries(t *testing.T) {
	dir := catalogDir(t)
	entries := catalogEntries(t, dir)
	var lines []string
	for _, name := range entries {
		data, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		front, _, err := Split(string(data))
		if err != nil {
			t.Fatalf("catalog entry %s: SKILL.md has no frontmatter: %v", name, err)
		}
		fm := Parse(front)
		if fm["name"] == "" || fm["description"] == "" {
			t.Fatalf("catalog entry %s: SKILL.md frontmatter lacks name or description", name)
		}
		lines = append(lines, catalogIndexLine(name, fm["description"]))
	}
	want := catalogIndexHeader + strings.Join(lines, "\n") + "\n"
	file := filepath.Join(dir, "INDEX.md")
	if catalogWriteMode(t) {
		if err := os.WriteFile(file, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("no INDEX.md: %v", err)
	}
	if string(data) == want {
		return
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		have[l] = true
	}
	wanted := map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		wanted[l] = true
		if !have[l] {
			t.Errorf("INDEX.md lacks the line %q", l)
		}
	}
	for l := range have {
		if !wanted[l] {
			t.Errorf("INDEX.md has a line no entry produces: %q", l)
		}
	}
	if !t.Failed() {
		t.Error("INDEX.md holds the right lines in the wrong order or count")
	}
}

// TestCatalogFilesAreCarryable: what the harness cannot carry byte-identical or
// would mis-handle is refused at the source — an executable bit (the embed has
// none, BUG-137), a symlink (the installer refuses it), and a Go file or module
// file (the Go tool would compile the one and the embed would drop the
// directory for the other).
func TestCatalogFilesAreCarryable(t *testing.T) {
	dir := catalogDir(t)
	catalogEntries(t, dir)
	// The search marker travels with the catalog: without it `fyr search`
	// indexes the entries and the checkout's own files drop in the hits.
	if _, err := os.Stat(filepath.Join(dir, ".fyr-noindex")); err != nil {
		t.Errorf("the catalog has no .fyr-noindex marker: %v", err)
	}
	root := dir
	if os.Getenv("CATALOG_DIR") == "" {
		root = filepath.Dir(dir) // the whole router skill, its scripts included
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			t.Errorf("%s is a symlink", rel)
		case d.IsDir():
		case info.Mode()&0o111 != 0:
			t.Errorf("%s has an executable bit", rel)
		case d.Name() == "go.mod" || d.Name() == "go.sum" || strings.HasSuffix(d.Name(), ".go"):
			t.Errorf("%s is a Go file inside the module", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// catalogExcluded reads the names under the manifest's "## Excluded".
func catalogExcluded(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "MANIFEST.md"))
	if err != nil {
		t.Fatalf("no MANIFEST.md: %v", err)
	}
	var out []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "## "):
			in = line == "## Excluded"
		case in && strings.HasPrefix(line, "- `"):
			if name, _, ok := strings.Cut(line[3:], "`"); ok {
				out = append(out, name)
			}
		}
	}
	return out
}

// TestCatalogExcludedEntriesAreAbsent: an entry the security review excluded
// stays out — the manifest's list is what the refresh applies, and a refresh
// that brought one back fails here.
func TestCatalogExcludedEntriesAreAbsent(t *testing.T) {
	dir := catalogDir(t)
	catalogEntries(t, dir)
	for _, name := range catalogExcluded(t, dir) {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("%s is listed under ## Excluded in MANIFEST.md and is in the catalog", name)
		}
	}
}

// TestCatalogWriteModeNeedsCatalogDir: CATALOG_WRITE=1 alone must not rewrite
// the checked-in CHECKSUMS.
func TestCatalogWriteModeNeedsCatalogDir(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a nested go test")
	}
	if os.Getenv("CATALOG_WRITE") == "1" {
		t.Skip("inside the write mode itself")
	}
	file := filepath.Join(catalogDir(t), "CHECKSUMS")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.WriteFile(file, before, 0o644) }) // a broken guard must not leave the file rewritten
	cmd := exec.Command("go", "test", ".", "-run", "^TestCatalogMatchesChecksums$", "-count=1")
	cmd.Env = fixtureEnv("CATALOG_WRITE=1", "CATALOG_DIR=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "needs CATALOG_DIR") {
		t.Errorf("CATALOG_WRITE=1 without CATALOG_DIR was accepted (err %v):\n%s", err, out)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(before, after) {
		t.Error("the checked-in CHECKSUMS was rewritten")
	}
	// The other half of the pair: CATALOG_DIR without the write mode would
	// compare some other tree and pass for a catalog that is not this one.
	cmd = exec.Command("go", "test", ".", "-run", "^TestCatalogMatchesChecksums$", "-count=1")
	cmd.Env = fixtureEnv("CATALOG_WRITE=", "CATALOG_DIR="+t.TempDir())
	out, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "only for the refresh script's write mode") {
		t.Errorf("CATALOG_DIR without CATALOG_WRITE=1 was accepted (err %v):\n%s", err, out)
	}
}

// earlyExitConsumer matches the start of a pipeline stage that stops reading
// before its producer is done: `head`, `grep -q/-m/-l` (short or long form),
// a `sed` script with a `q` or `Q` command (after an address, a `!`, a flag,
// a `;` or a brace, with or without an exit code), an `awk` program with
// `exit`, `read`; by name or by path.
var earlyExitConsumer = regexp.MustCompile(`^&?\s*(\S*/)?(head\b|grep\b.*(\s-[a-zA-Z]*[qml]|\s--(quiet|silent|max-count|files-with-matches)\b)|sed\b.*[^a-z_][qQ][0-9]*([}'";\s)]|$)|awk\b.*\bexit\b|read\b)`)

// pipeStages cuts one logical shell line at its single pipes and returns what
// follows each. Not a pipe: `||`, an escaped `\|`, a `|` inside single
// quotes, a `|` inside double quotes (an alternation in a pattern), anything
// after an unquoted ` #`. A command substitution is code again, also inside
// double quotes: `v="$(find . | head -1)"` has a pipe, and the stage it
// starts ends at the substitution's closing parenthesis.
func pipeStages(line string) []string {
	var stages []string
	var quote byte   // 0, '\'' or '"'
	var saved []byte // the quote state each open `$(` returns to
	var depth []int  // plain parentheses open inside each `$(`
	start := -1
	end := func(i int) {
		if start >= 0 {
			stages = append(stages, line[start:i])
			start = -1
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' {
			i++
			continue
		}
		if c == '$' && i+1 < len(line) && line[i+1] == '(' {
			saved, depth, quote = append(saved, quote), append(depth, 0), 0
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				end(len(line))
				return stages
			}
		case '(':
			if len(depth) > 0 {
				depth[len(depth)-1]++
			}
		case ')':
			if n := len(depth); n > 0 {
				if depth[n-1] > 0 {
					depth[n-1]--
				} else {
					end(i)
					quote, saved, depth = saved[n-1], saved[:n-1], depth[:n-1]
				}
			}
		case '|':
			if i+1 < len(line) && line[i+1] == '|' {
				end(i)
				i++
				continue
			}
			end(i)
			start = i + 1
		}
	}
	end(len(line))
	return stages
}

// earlyExitPipeProblem is the first logical line of a shell script that pipes
// into such a consumer without a guard. Under `set -o pipefail` the producer
// is killed by SIGPIPE once its output outgrows a pipe and the pipeline's
// status is 141: `set -e` ends the script with no message, and an `||` or an
// `if` on the pipeline takes the wrong branch (BUG-155, BUG-157; BUG-138 and
// BUG-148 are the same family). A producer stops itself instead (`find
// -quit`, `awk '{…; exit}'` reading a file), a test reads a here-string or
// asks git directly; a line that only prints an excerpt ends `|| true`.
//
// It is a static check with limits, in the refusing direction and the
// passing one: a `case` label spelled like a consumer (`a|head)`) is refused;
// a `sed` stage is refused when a `q` or `Q` follows anything but a lowercase
// letter, which takes in some harmless ones (`sed 's|a|b|' FAQ`, a label
// `:q`, `s/x/"Q"/`): restructure such a line, or end it `|| true`; and a
// consumer reached through a function or a variable is not seen.
func earlyExitPipeProblem(script string) string {
	// One logical line: a backslash continuation, and a line ending in a
	// pipe, continue on the next.
	script = strings.ReplaceAll(script, "\\\n", " ")
	script = regexp.MustCompile(`([^|])\|[ \t]*\n`).ReplaceAllString(script, "$1| ")
	for _, l := range strings.Split(script, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "#") || strings.HasSuffix(t, "|| true") {
			continue
		}
		for _, stage := range pipeStages(t) {
			if earlyExitConsumer.MatchString(stage) {
				return t
			}
		}
	}
	return ""
}

// TestScriptsHaveNoEarlyExitPipe pins that rule on the shell scripts the
// repository tracks that run under pipefail (the skills' scripts and the run
// kits' preamble), and proves the check on both states first.
func TestScriptsHaveNoEarlyExitPipe(t *testing.T) {
	for _, bad := range []string{
		"bad=$(find . -print | head -1)\n",
		"git log --format=%h | grep -q -- \"^$sha\" || exit 2\n",
		"printf '%s\\n' \"$line\" | grep -q $ci -F -- \"$w\" || keep=0\n",
		"if producer | grep -m1 x; then :; fi\n",
		"if producer | grep --quiet x; then :; fi\n",
		"if producer | grep -e x -q; then :; fi\n",
		"n=$(producer | grep --max-count=1 x)\n",
		"f=$(producer | grep -l x)\n",
		"first=$(producer | sed 1q)\n",
		"first=$(producer | sed q)\n",
		"first=$(producer | sed -n '1p;q')\n",
		"first=$(producer | sed '/re/q')\n",
		"first=$(producer | awk '{print; exit}')\n",
		"producer | read first\n",
		"producer |& head -1\n",
		"bad=$(find . -print | /usr/bin/head -1)\n",
		"bad=$(find . -print |\n  head -1)\n",
		"bad=$(find . -print \\\n  | head -1)\n",
		"v=\"$(find . -print | head -1)\"\n",
		"[ -n \"$(producer | grep -q x)\" ] || exit 1\n",
		"first=$(producer | sed -n '1{p;q}')\n",
		"producer | sed '/^#/!q'\n",
		"producer | sed '/x/Iq'\n",
		"producer | sed -n '/x/Mq'\n",
		"producer | sed '\\%x%q'\n",
		"producer | sed '/a/,/b/!q'\n",
		"producer | sed 1Q\n",
		"producer | sed '/x/q5'\n",
		"echo \"n: $(producer | (cat; true) | head -1) done\"\n",
	} {
		if earlyExitPipeProblem(bad) == "" {
			t.Errorf("the check passes %q", bad)
		}
	}
	for _, good := range []string{
		"# never `| head -1`\n",
		"grep x f | head -20 >&2 || true\n",
		"bad=$(find . -print -quit)\n",
		"grep -q x <<< \"$line\"\n",
		"sort f | grep -v x\n",
		"printf x | sed 's/q/z/'\n",
		"sed -n 's/^date: //p' f | sort\n",
		"[ -f x ] || head -1 f\n",
		"grep -E 'write|read|head' f\n",
		"grep -E \"quiet|head -1\" f | sort\n",
		"producer | while read -r x; do :; done\n",
		"d=$(awk '/^date: /{print; exit}' f)\n",
		"producer | awk '{print $1}' | sort\n",
		"echo a \\| head -1\n",
		"x=1 # then | head -1\n",
		"echo \"$(sort f | uniq)\" \"a|head\"\n",
		"sed 's/a/b/' faq | sort\n",
		"producer | sed 's/q/z/;s/x/q/' | sort\n",
	} {
		if got := earlyExitPipeProblem(good); got != "" {
			t.Errorf("the check refuses %q", good)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var scripts []string
	for _, pat := range []string{"skills/*/scripts/*.sh", ".agents/docs/testing/*.sh"} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pat)))
		if err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, m...)
	}
	if len(scripts) < 3 {
		t.Fatalf("want the two skill scripts and the kit preamble, found %v", scripts)
	}
	for _, f := range scripts {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "pipefail") {
			continue
		}
		if l := earlyExitPipeProblem(string(b)); l != "" {
			t.Errorf("%s pipes into a consumer that exits early, without a guard (BUG-155): %s", filepath.Base(f), l)
		}
	}
}

// TestCatalogNotesNeverAllowQuiet: the router does not follow an entry's
// `--quiet` (step 4), and a note never relaxes the router. A note that names
// the flag therefore says to leave it out, in one of three fixed phrases;
// "only on read-only commands" or "after approval" is an exception the router
// does not have (security reviews of the v2.1.0 gate: found three times as
// instances, pinned here as a kind).
func TestCatalogNotesNeverAllowQuiet(t *testing.T) {
	// The helper first, on lines that must and must not pass: a carve-out
	// beside an allowed phrase once passed (code review, R3-1).
	for line, bad := range map[string]bool{
		"- `x` — No `--quiet` on any command.":                                      false,
		"- `x` — bind on the bucket, drop `--quiet`, and confirm every IAM change.": false,
		"- `x` — run it without `--quiet`; ask before `services enable`.":           false,
		"- `x` — the operator only said so; drop `--quiet`; ask after the dry run.": false,
		"- `x` — No `--quiet` except after approval.":                               true,
		"- `x` — drop `--quiet` unless the command is read-only.":                   true,
		"- `x` — `--quiet` only on read-only commands.":                             true,
		"- `x` — No `--quiet` once the operator approved the plan.":                 true,
		"- `x` — it runs `gcloud compute ssh --quiet`.":                             true,
		"- `x` — drop `--quiet` if the command changes state.":                      true,
		"- `x` — No `--quiet` when the operator has not approved.":                  true,
		"- `x` — drop `--quiet`, but keep it on read commands.":                     true,
		"- `x` — pass `--project` on each command.":                                 false,
	} {
		if got := quietNoteProblem(line); (got != "") != bad {
			t.Errorf("quietNoteProblem(%q) = %q, want a problem: %v", line, got, bad)
		}
	}
	if os.Getenv("CATALOG_WRITE") == "1" {
		t.Skip("the refresh script's build directory has no notes yet")
	}
	data, err := os.ReadFile(filepath.Join(catalogDir(t), "MANIFEST.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, notes, ok := strings.Cut(string(data), "\n## Notes\n")
	if !ok {
		t.Fatal("MANIFEST.md has no ## Notes section")
	}
	seen := 0
	for _, line := range strings.Split(notes, "\n") {
		if !strings.HasPrefix(line, "- `") || !strings.Contains(line, "--quiet") {
			continue
		}
		seen++
		if problem := quietNoteProblem(line); problem != "" {
			t.Errorf("%s:\n%s", problem, line)
		}
	}
	if seen == 0 {
		t.Error("no note names --quiet: the test checks nothing (was the section renamed?)")
	}
}

// fixtureEnv is the environment for every subprocess the catalog tests
// start, the fixture's git calls and each run of the script: the caller's,
// without any GIT_* variable, plus the test's own additions. An exported
// GIT_DIR or GIT_INDEX_FILE (git sets them inside a hook) would aim the
// fixture's add, commit and reset --hard at another repository (the v2.1.0
// gate's L10).
func fixtureEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// quietNoteProblem judges one note: every clause that names `--quiet` must
// say to leave it out, in one of three fixed phrases, and must not carve an
// exception beside the phrase. A clause ends at a semicolon or at a full
// stop followed by a space. It returns "" or the reason.
func quietNoteProblem(line string) string {
	for _, clause := range quietClauseEnd.Split(line, -1) {
		if !strings.Contains(clause, "--quiet") {
			continue
		}
		lower := strings.ToLower(clause)
		if !strings.Contains(lower, "no `--quiet`") && !strings.Contains(lower, "drop `--quiet`") && !strings.Contains(lower, "without `--quiet`") {
			return "a note names --quiet without saying to leave it out (no `--quiet`, drop `--quiet` or without `--quiet`)"
		}
		if word := quietCarveOut.FindString(lower); word != "" {
			return "a note carves an exception (\"" + word + "\") in the clause that names --quiet; the router has none"
		}
	}
	return ""
}

var (
	quietClauseEnd = regexp.MustCompile(`;|\. `)
	quietCarveOut  = regexp.MustCompile(`\b(except|unless|only|once|after|if|when|but)\b`)
)

// TestCatalogInstallsClean: the install plan over the real checkout has no
// refusal — a secret-shaped name or a symlink anywhere under skills/ fails it,
// through the installer's own list rather than a second copy in the script.
func TestCatalogInstallsClean(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(DirSource(repo), nil); err != nil {
		t.Fatalf("the checkout does not install: %v", err)
	}
}

// TestRouterDescription: the router is the one catalog line the CLI lists, so
// its description is short (Claude Code's listing budget, TestSkillListingBudget)
// and says what it must not be used for.
func TestRouterDescription(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "skills", "google-catalog", "SKILL.md"))
	if err != nil {
		t.Fatalf("no skills/google-catalog/SKILL.md: %v", err)
	}
	front, _, err := Split(string(data))
	if err != nil {
		t.Fatal(err)
	}
	desc := Parse(front)["description"]
	if n := len(desc); n < 240 || n > 300 {
		t.Errorf("the router's description is %d bytes, want 240 to 300", n)
	}
	if !strings.Contains(desc, "Do NOT use") {
		t.Errorf("the router's description names no skill that wins over it: %q", desc)
	}
}

// TestRefreshCatalogScript runs the refresh against two local fixture
// repositories: the happy path, idempotence, and one fixture per refusal
// class, each of which must exit 1, name the entry, and leave the destination
// byte-identical. It never reaches the network.
func TestRefreshCatalogScript(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repo, "skills", "google-catalog", "scripts", "refresh-catalog.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("no refresh-catalog.sh: %v", err)
	}
	if testing.Short() {
		t.Skip("runs git and a nested go test")
	}
	// The manifest carries the day's date, and the test compares the catalog
	// across runs of the script: a test that crosses midnight would compare
	// two dates and fail for that alone (seen once, 2026-10-02 00:00). Wait
	// the last two minutes of a day out.
	if now := time.Now(); now.Hour() == 23 && now.Minute() >= 58 {
		midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 2, 0, now.Location())
		t.Logf("waiting %s for midnight to pass", time.Until(midnight).Round(time.Second))
		time.Sleep(time.Until(midnight))
	}
	for _, tool := range []string{"bash", "git", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	tmp := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = fixtureEnv("GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	put := func(dir string, files map[string]string) {
		t.Helper()
		for rel, body := range files {
			p := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	commit := func(dir string) string {
		t.Helper()
		git(dir, "add", "-A")
		git(dir, "commit", "-qm", "c", "--allow-empty")
		return git(dir, "rev-parse", "HEAD")
	}
	skill := func(name, description string) string {
		return "---\nname: " + name + "\n" + description + "---\n\n# " + name + "\n"
	}
	agents, gskills := filepath.Join(tmp, "agents-cli"), filepath.Join(tmp, "google-skills")
	for _, d := range []string{agents, gskills} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		git(d, "init", "-q")
	}
	put(agents, map[string]string{
		"LICENSE":               "Apache-2.0 (fixture)\n",
		"skills/alpha/SKILL.md": skill("alpha", "description: plain one-line description of alpha\n"),
		"extensions/langchain/template/.agents/skills/beta/SKILL.md": skill("beta", "description: \"a quoted description: with a colon\"\n"),
	})
	pinA := commit(agents)
	put(gskills, map[string]string{
		"LICENSE":                              "Apache-2.0 (fixture)\n",
		"skills/cloud/gamma/SKILL.md":          skill("gamma", "description: >-\n  a block scalar description\n  folded over two lines\n"),
		"skills/cloud/gamma/scripts/run.sh":    "#!/bin/sh\necho hi\n",
		"skills/cloud/delta/SKILL.md":          skill("delta", "metadata:\n  version: \"1.0\"\ndescription: delta with a metadata map\n"),
		"skills/developers/epsilon/SKILL.md":   skill("epsilon", "description: epsilon\n"),
		"skills/ads/not-asked-for/SKILL.md":    skill("not-asked-for", "description: outside the four source sets\n"),
		"skills/cloud/gamma/references/ref.md": "reference text\n",
	})
	if err := os.Chmod(filepath.Join(gskills, "skills/cloud/gamma/scripts/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	pinG := commit(gskills)

	dest, work := filepath.Join(tmp, "dest"), filepath.Join(tmp, "work")
	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{script, "--dest", dest, "--work", work}, args...)...)
		cmd.Dir = repo
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return out.String(), ee.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return out.String(), 0
	}
	snapshot := func() string {
		t.Helper()
		var b strings.Builder
		filepath.WalkDir(dest, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, _ := os.ReadFile(path)
			sum := sha256.Sum256(data)
			info, _ := d.Info()
			rel, _ := filepath.Rel(dest, path)
			fmt.Fprintf(&b, "%s %o %x\n", rel, info.Mode().Perm(), sum[:6])
			return nil
		})
		return b.String()
	}

	out, code := run(pinA, pinG)
	if code != 0 {
		t.Fatalf("happy path: exit %d\n%s", code, out)
	}
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if _, err := os.Stat(filepath.Join(dest, name, "SKILL.md")); err != nil {
			t.Errorf("entry %s not vendored: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "not-asked-for")); err == nil {
		t.Error("an entry outside the four source sets was vendored")
	}
	index, _ := os.ReadFile(filepath.Join(dest, "INDEX.md"))
	for _, want := range []string{
		"- `gamma` — a block scalar description folded over two lines\n",
		"- `beta` — a quoted description: with a colon\n",
		"- `delta` — delta with a metadata map\n",
	} {
		if !strings.Contains(string(index), want) {
			t.Errorf("INDEX.md lacks %q:\n%s", want, index)
		}
	}
	if n := strings.Count(string(index), "\n- `"); n != 5 {
		t.Errorf("INDEX.md has %d entry lines, want 5", n)
	}
	sums, _ := os.ReadFile(filepath.Join(dest, "CHECKSUMS"))
	for _, want := range []string{"  gamma/scripts/run.sh\n", "  LICENSE-agents-cli\n", "  LICENSE-google-skills\n"} {
		if !strings.Contains(string(sums), want) {
			t.Errorf("CHECKSUMS lacks %q", want)
		}
	}
	if strings.Contains(string(sums), "INDEX.md") || strings.Contains(string(sums), "MANIFEST.md") {
		t.Error("CHECKSUMS covers a harness-written file")
	}
	manifest, _ := os.ReadFile(filepath.Join(dest, "MANIFEST.md"))
	for _, want := range []string{pinA[:7], pinG[:7], "## Excluded", "## Notes", "https://github.com/google/agents-cli", "https://github.com/google/skills", "Fetched through an override"} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("MANIFEST.md lacks %q", want)
		}
	}
	// The manifest names the upstream repositories, never the value of an
	// override: that value may hold a token in its path, and the manifest is
	// committed (the v2.2.0 gate's L4).
	if strings.Contains(string(manifest), tmp) {
		t.Errorf("MANIFEST.md records an override's value:\n%s", manifest)
	}
	if info, err := os.Stat(filepath.Join(dest, "gamma", "scripts", "run.sh")); err != nil || info.Mode()&0o111 != 0 {
		t.Errorf("the executable bit was not cleared: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".fyr-noindex")); err != nil {
		t.Errorf("the search marker was not written: %v", err)
	}
	if !strings.Contains(out, "next: git add") {
		t.Errorf("the script does not say what to run next:\n%s", out)
	}
	// With an override the script says where the value stays on disk: git
	// keeps it in the work clones' config and logs (security review of
	// v2.2.0..1fc0d4c).
	if !strings.Contains(out, "delete that directory if the value is sensitive") {
		t.Errorf("the script does not say an override's value stays in the work directory:\n%s", out)
	}
	good := snapshot()
	if out, code := run(pinA, pinG); code != 0 || snapshot() != good {
		t.Fatalf("a second run at the same pins changed the catalog (exit %d)\n%s", code, out)
	}

	// Relative --dest and --work resolve against the caller's directory, not
	// against the directory the nested go test runs in (BUG-146).
	relCmd := exec.Command("bash", script, "--dest", "rel-dest", "--work", "rel-work", pinA, pinG)
	relCmd.Dir = tmp
	relCmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
	if out, err := relCmd.CombinedOutput(); err != nil {
		t.Errorf("relative --dest and --work: %v\n%s", err, out)
	} else if _, err := os.Stat(filepath.Join(tmp, "rel-dest", "gamma", "SKILL.md")); err != nil {
		t.Errorf("relative --dest: the catalog did not land under the caller's directory: %v", err)
	}

	// An entry the old CHECKSUMS names but whose directory is already gone
	// (a half-done refresh, a hand deletion) must not stop the swap half way:
	// the loop's last test once ended the pipeline with status 1, silently,
	// after the other entries were deleted (code review 2026-10-01).
	if err := os.RemoveAll(filepath.Join(dest, "gamma")); err != nil {
		t.Fatal(err)
	}
	if out, code := run(pinA, pinG); code != 0 || snapshot() != good {
		t.Fatalf("a refresh over a catalog with a missing entry directory did not restore it (exit %d)\n%s", code, out)
	}

	// The swap removes what the old CHECKSUMS names and nothing else: a
	// directory someone put beside the entries survives a refresh.
	put(dest, map[string]string{"bystander/keep.txt": "kept\n"})
	if out, code := run(pinA, pinG); code != 0 {
		t.Fatalf("a refresh over a catalog with a bystander directory failed (exit %d)\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dest, "bystander", "keep.txt")); err != nil {
		t.Errorf("the swap removed a directory the old CHECKSUMS does not name: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dest, "bystander")); err != nil {
		t.Fatal(err)
	}
	if snapshot() != good {
		t.Fatal("the catalog is not the good one after the bystander fixture")
	}

	// The same loop's second way out: a CHECKSUMS that names no entry at all
	// (an empty one) once made `grep /` end the pipeline the same way.
	bare := filepath.Join(tmp, "bare-dest")
	put(bare, map[string]string{"CHECKSUMS": ""})
	bareCmd := exec.Command("bash", script, "--dest", bare, "--work", work, pinA, pinG)
	bareCmd.Dir = repo
	bareCmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
	if out, err := bareCmd.CombinedOutput(); err != nil {
		t.Errorf("a refresh over a destination whose CHECKSUMS names no entry failed: %v\n%s", err, out)
	} else if _, err := os.Stat(filepath.Join(bare, "gamma", "SKILL.md")); err != nil {
		t.Errorf("a refresh over a destination whose CHECKSUMS names no entry vendored nothing: %v", err)
	}

	// The destination guard: an empty --dest, and a directory that is not a
	// catalog, are refused before anything is deleted (security review S1).
	guardDir := filepath.Join(tmp, "not-a-catalog")
	if err := os.MkdirAll(filepath.Join(guardDir, "precious"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"", guardDir} {
		cmd := exec.Command("bash", script, "--dest", d, "--work", work, pinA, pinG)
		cmd.Dir = guardDir
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("--dest %q was accepted:\n%s", d, out)
		}
		if _, err := os.Stat(filepath.Join(guardDir, "precious")); err != nil {
			t.Fatalf("--dest %q: a directory that is not a catalog entry was deleted", d)
		}
	}
	// The places the guard names, each refused by its own message before the
	// not-a-catalog check is reached: /, the home directory, the checkout,
	// and an ancestor of either. HOME is a fixture directory here.
	fakeHome := filepath.Join(tmp, "home", "user")
	put(fakeHome, map[string]string{"keep.txt": "kept\n"})
	repoLink := filepath.Join(tmp, "link-to-the-checkout")
	if err := os.Symlink(repo, repoLink); err != nil {
		repoLink = repo // no symlinks here: the case repeats the plain one
	}
	for _, c := range []struct{ dest, want string }{
		{"/", "is not a catalog directory"},
		{fakeHome, "is not a catalog directory"},
		{fakeHome + "/", "is not a catalog directory"},
		{repo, "is not a catalog directory"},
		{filepath.Dir(repo), "contains the checkout"},
		{filepath.Dir(fakeHome), "contains the home directory"},
		// Another spelling of the same place is the same place: a path
		// through `..` and a symlink (security review of the v2.1.0 gate).
		{filepath.Join(repo, "skills") + "/..", "is not a catalog directory"},
		{repoLink, "is not a catalog directory"},
		// `..` below a directory that does not exist cannot be resolved, and
		// `mkdir -p` would later make it real: the home directory, the
		// checkout or / reached that way (code review of 7dd0645..f62513b).
		{fakeHome + "/no-such-dir/..", "below a directory that does not exist"},
		{repo + "/no-such-dir/..", "below a directory that does not exist"},
		{"/no-such-dir-for-this-test/..", "below a directory that does not exist"},
		{fakeHome + "/no-such-dir/./catalog", "below a directory that does not exist"},
		// A leading run of slashes is the same place (the v2.1.0 gate's L2).
		{"/" + fakeHome, "is not a catalog directory"},
		{"//" + fakeHome, "is not a catalog directory"},
	} {
		cmd := exec.Command("bash", script, "--dest", c.dest, "--work", work, pinA, pinG)
		cmd.Dir = tmp
		// The sources name nothing: if the guard and the not-a-catalog check
		// both regressed, the run stops at the clone and writes nowhere
		// (code review round 3).
		nowhere := filepath.Join(tmp, "no-such-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+nowhere, "CATALOG_GOOGLE_SKILLS_REPO="+nowhere, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("--dest %s: want a refusal saying %q, got err %v\n%s", c.dest, c.want, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(fakeHome, "keep.txt")); err != nil {
		t.Fatalf("the guard fixture's home directory was touched: %v", err)
	}
	// Two ways the guard itself could lose its footing (code review of
	// 7dd0645..b4e3d79, round 2): a parent directory it cannot enter, which
	// once resolved to a path under /, and no HOME to compare against.
	guardRun := func(dest string, env []string) (string, error) {
		cmd := exec.Command("bash", script, "--dest", dest, "--work", work, pinA, pinG)
		cmd.Dir = tmp
		nowhere := filepath.Join(tmp, "no-such-repository")
		cmd.Env = append(env, "CATALOG_AGENTS_CLI_REPO="+nowhere, "CATALOG_GOOGLE_SKILLS_REPO="+nowhere, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(tmp, "locked")
		if err := os.Mkdir(locked, 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		if out, err := guardRun(filepath.Join(locked, "x"), fixtureEnv("HOME="+fakeHome)); err == nil || !strings.Contains(out, "cannot resolve") {
			t.Errorf("--dest below a directory that cannot be entered: want a refusal saying \"cannot resolve\", got err %v\n%s", err, out)
		}
	}
	// --work is cloned into and its build directory removed, so it is a
	// directory of this script or a new one (the v2.1.0 gate's L7): never /,
	// the home directory or the checkout, never nested with --dest, and when
	// it holds files, only with the marker the script writes before its
	// first clone, or the origins.txt of an earlier completed run.
	workRun := func(destArg, workArg string, pins ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{script, "--dest", destArg, "--work", workArg}, pins...)...)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// The three forbidden places are tried with a COPY of the script inside a
	// stand-in checkout, and with sources that cannot be cloned: when this
	// guard did not exist yet, the same cases run against the real checkout
	// with working sources cloned the fixtures into it (BUG-150).
	fakeRepo := filepath.Join(tmp, "fake-checkout")
	scriptCopy := filepath.Join(fakeRepo, "skills", "google-catalog", "scripts", "refresh-catalog.sh")
	scriptBytes, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	put(fakeRepo, map[string]string{"skills/google-catalog/scripts/refresh-catalog.sh": string(scriptBytes), "README.md": "a stand-in checkout\n"})
	places := []string{fakeHome, fakeRepo}
	if os.Geteuid() != 0 {
		places = append(places, "/") // as root a regressed guard could write a marker there
	}
	for _, w := range places {
		cmd := exec.Command("bash", scriptCopy, "--dest", filepath.Join(tmp, "dest-w"), "--work", w, pinA, pinG)
		cmd.Dir = tmp
		nowhere := filepath.Join(tmp, "no-such-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+nowhere, "CATALOG_GOOGLE_SKILLS_REPO="+nowhere, "CATALOG_DIR=", "CATALOG_WRITE=")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "must not be /, the home directory or the checkout") {
			t.Errorf("--work %s: want a refusal, got err %v\n%s", w, err, out)
		}
	}
	for _, stray := range []string{filepath.Join(fakeRepo, "agents-cli"), filepath.Join(fakeRepo, ".refresh-catalog-work"), filepath.Join(fakeHome, "agents-cli"), filepath.Join(fakeHome, ".refresh-catalog-work")} {
		if _, err := os.Stat(stray); err == nil {
			t.Errorf("a refused --work was written into: %s", stray)
		}
	}
	nest := filepath.Join(tmp, "nest")
	for _, c := range [][2]string{{nest, filepath.Join(nest, "work")}, {filepath.Join(nest, "dest"), nest}} {
		if out, err := workRun(c[0], c[1], pinA, pinG); err == nil || !strings.Contains(out, "must not contain each other") {
			t.Errorf("--dest %s --work %s: want a refusal, got err %v\n%s", c[0], c[1], err, out)
		}
	}
	foreign := filepath.Join(tmp, "someone-elses-directory")
	put(foreign, map[string]string{"agents-cli/notes.txt": "the operator's own clone\n", "build/keep.txt": "kept\n"})
	if out, err := workRun(filepath.Join(tmp, "dest-f"), foreign, pinA, pinG); err == nil || !strings.Contains(out, "is not a work directory of this script") || !strings.Contains(out, "name an empty or new directory, or delete this one") {
		t.Errorf("a non-empty --work without the marker: want a refusal, got err %v\n%s", err, out)
	}
	for _, f := range []string{"agents-cli/notes.txt", "build/keep.txt"} {
		if _, err := os.Stat(filepath.Join(foreign, f)); err != nil {
			t.Errorf("the refused --work lost %s: %v", f, err)
		}
	}
	// A first run that fails after the checks (a pin that does not resolve)
	// must leave a work directory the corrected run accepts.
	freshWork, freshDest := filepath.Join(tmp, "fresh-work"), filepath.Join(tmp, "fresh-dest")
	if out, err := workRun(freshDest, freshWork, pinA, "0000000000000000000000000000000000000000"); err == nil || !strings.Contains(out, "does not resolve") {
		t.Fatalf("a first run with a bad pin: want the pin refusal, got err %v\n%s", err, out)
	} else if !strings.Contains(out, "delete that directory if the value is sensitive") {
		// The clones are on disk by then, and with them the override's
		// value: a run that fails says so too, not only one that succeeds
		// (security review of 1fc0d4c..4165f81).
		t.Errorf("a run that fails after the clone does not say an override's value stays in the work directory:\n%s", out)
	}
	// The same when no refusal of the script's own ends the run: a plain
	// command that fails under set -e (here the destination cannot be made,
	// its parent is read-only) still leaves the value on disk, and the run
	// still says so (review of 4165f81..7b3e969).
	roParent := filepath.Join(tmp, "read-only")
	if err := os.Mkdir(roParent, 0o555); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		t.Log("running as root: a 0555 parent stops nothing, the failing-command case is not exercised")
	} else if out, err := workRun(filepath.Join(roParent, "dest"), filepath.Join(tmp, "ro-work"), pinA, pinG); err == nil {
		t.Errorf("a destination that cannot be created: want a failure\n%s", out)
	} else if !strings.Contains(out, "delete that directory if the value is sensitive") {
		t.Errorf("a run stopped by a failing command after the clone does not say an override's value stays in the work directory:\n%s", out)
	}
	// And when a signal ends it: TERM while the script waits for the writer
	// (a stub `go` that says it started and then sleeps). The whole process
	// group is signalled, as a terminal does, so the stub dies and the
	// script's TERM trap exits 143 through the note.
	{
		stub := filepath.Join(tmp, "stub-bin")
		started := filepath.Join(tmp, "stub-go-started")
		if err := os.MkdirAll(stub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stub, "go"), []byte("#!/bin/sh\n: > '"+started+"'\nexec sleep 120\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "sig-dest"), "--work", filepath.Join(tmp, "sig-work"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "PATH="+stub+string(os.PathListSeparator)+os.Getenv("PATH"), "CATALOG_AGENTS_CLI_REPO="+agents, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				cmd.Wait()
				t.Fatalf("the script never reached the writer:\n%s", buf.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		err := cmd.Wait()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 143 || !strings.Contains(buf.String(), "delete that directory if the value is sensitive") {
			t.Errorf("a run ended by TERM after the clone: want exit 143 and the note that an override's value stays in the work directory, got %v\n%s", err, buf.String())
		}
	}
	if out, err := workRun(freshDest, freshWork, pinA, pinG); err != nil {
		t.Errorf("the corrected run on the same --work was refused: %v\n%s", err, out)
	} else if _, err := os.Stat(filepath.Join(freshDest, "gamma", "SKILL.md")); err != nil {
		t.Errorf("the corrected run vendored nothing: %v", err)
	}
	// The source overrides take an https URL or an absolute path, nothing
	// else: git has transports that run a command (`ext::`).
	for _, bad := range []string{"ext::sh -c id", "relative/path", "http://example.invalid/x", "file:///x", "https://user:token@example.invalid/x", "http://user:token@example.invalid/x", "https://example.invalid/x?access_token=abc"} {
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-o"), "--work", filepath.Join(tmp, "work-o"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+bad, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "CATALOG_AGENTS_CLI_REPO must be an https:// URL or an absolute path") {
			t.Errorf("override %q: want a refusal naming the variable, got err %v\n%s", bad, err, out)
		}
		// No refused value is echoed: any of them may hold a credential
		// (an http:// URL with user information is refused for its scheme).
		if strings.Contains(string(out), bad) || strings.Contains(string(out), "token") || strings.Contains(string(out), "example.invalid") {
			t.Errorf("override %q: the refusal echoes the value:\n%s", bad, out)
		}
		if strings.HasPrefix(bad, "https://") && !strings.Contains(string(out), "without credentials") {
			t.Errorf("override with credentials: the refusal does not say why:\n%s", out)
		}
		// Refused before anything is cloned: nothing is on disk, and the
		// note about the work directory would be false.
		if strings.Contains(string(out), "delete that directory") {
			t.Errorf("override %q: a refusal before any clone speaks of a value on disk:\n%s", bad, out)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "work-o")); err == nil {
		t.Error("a refused override still created the work directory")
	}
	// A source that passes the check and then cannot be cloned is named by
	// its role, not by its value: a token can sit in a URL's path.
	{
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-c"), "--work", filepath.Join(tmp, "work-c"), pinA, pinG)
		cmd.Dir = tmp
		secretPath := filepath.Join(tmp, "no-such-SECRETPATH-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") || strings.Contains(string(out), "SECRETPATH") || strings.Contains(string(out), "fatal:") {
			t.Errorf("a source that cannot be cloned: want it named by role, and neither its value nor git's own message in the output, got err %v\n%s", err, out)
		}
	}
	// git has channels that no redirect of its output covers: a trace file
	// named by the caller's environment records the clone's arguments. The
	// script turns tracing off for its own git calls (security review of
	// 7b3e969..a224329).
	{
		trace := filepath.Join(tmp, "git-trace.log")
		trace2 := filepath.Join(tmp, "git-trace2.log")
		cmd := exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-t"), "--work", filepath.Join(tmp, "work-t"), pinA, pinG)
		cmd.Dir = tmp
		secretPath := filepath.Join(tmp, "no-such-SECRETTRACE-repository")
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=", "GIT_TRACE="+trace, "GIT_TRACE2_EVENT="+trace2)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") {
			t.Errorf("a traced run with a source that cannot be cloned: want the role-named refusal, got err %v\n%s", err, out)
		}
		for _, f := range []string{trace, trace2} {
			if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "SECRETTRACE") {
				t.Errorf("%s holds the override's value: the script's git calls ran with the caller's tracing on", filepath.Base(f))
			}
		}
		// A trace target can also come from git's configuration, where no
		// environment variable of the caller's names it: the script's own
		// GIT_TRACE2* settings must win over it.
		cfgHome := filepath.Join(tmp, "trace-home")
		cfgTraces := map[string]string{ // trace2 key: its file
			"eventTarget":  filepath.Join(tmp, "git-trace2-config-event.log"),
			"normalTarget": filepath.Join(tmp, "git-trace2-config-normal.log"),
			"perfTarget":   filepath.Join(tmp, "git-trace2-config-perf.log"),
		}
		if err := os.MkdirAll(cfgHome, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg := "[trace2]\n"
		for key, f := range cfgTraces {
			cfg += "\t" + key + " = " + f + "\n"
		}
		if err := os.WriteFile(filepath.Join(cfgHome, ".gitconfig"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-t2"), "--work", filepath.Join(tmp, "work-t2"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+cfgHome, "CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err = cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") {
			t.Errorf("a run under a trace2 target in git's config: want the role-named refusal, got err %v\n%s", err, out)
		}
		for key, f := range cfgTraces {
			if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), "SECRETTRACE") {
				t.Errorf("%s holds the override's value: trace2.%s from git's config was left on", filepath.Base(f), key)
			}
		}
		// git asks for a credential through an askpass program before it
		// tries the terminal, and hands it a prompt that names the URL: an
		// https source that answers 401 must not reach one (security review
		// of a224329..01255bc).
		var asked atomic.Int32
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			http.Error(w, "credentials required", http.StatusUnauthorized)
		}))
		defer srv.Close()
		askLog := filepath.Join(tmp, "askpass.log")
		askStub := filepath.Join(tmp, "askpass.sh")
		if err := os.WriteFile(askStub, []byte("#!/bin/sh\necho \"$*\" >> '"+askLog+"'\necho x\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command("bash", script, "--dest", filepath.Join(tmp, "dest-a"), "--work", filepath.Join(tmp, "work-a"), pinA, pinG)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv("HOME="+fakeHome, "CATALOG_AGENTS_CLI_REPO="+srv.URL+"/SECRETASK", "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=", "GIT_SSL_NO_VERIFY=1", "GIT_ASKPASS="+askStub, "SSH_ASKPASS="+askStub,
			"https_proxy=", "HTTPS_PROXY=", "ALL_PROXY=", "all_proxy=", "NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1")
		out, err = cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot clone the agents-cli source") || strings.Contains(string(out), "SECRETASK") {
			t.Errorf("an https source that wants credentials: want the role-named refusal and no value, got err %v\n%s", err, out)
		}
		if b, err := os.ReadFile(askLog); err == nil {
			t.Errorf("git ran the caller's askpass program for the script's clone:\n%s", b)
		}
		// The condition this case sets up: git did ask the server, and was
		// told 401. Without that the stub staying silent proves nothing.
		if asked.Load() == 0 {
			t.Errorf("git never reached the https fixture (no https transport, or a proxy in the way): the askpass case tested nothing\n%s", out)
		}
		// GIT_CURL_VERBOSE is on whenever it is set, whatever its value, and
		// what it prints goes where the calls' output goes (discarded), so
		// no run shows the difference: the script's text is pinned instead.
		// It removes the variable and never assigns it.
		text, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), "GIT_CURL_VERBOSE=") || !strings.Contains(string(text), "unset GIT_CURL_VERBOSE") {
			t.Error("the script assigns GIT_CURL_VERBOSE or does not unset it: git turns curl's trace on when the variable is set to anything, 0 included")
		}
	}
	// The same on the path every later refresh takes: a work directory that
	// already holds the clones fetches, and a fetch that fails says which
	// source, not its value (the v2.2.0 gate's L3).
	{
		secretPath := filepath.Join(tmp, "no-such-SECRETFETCH-repository")
		cmd := exec.Command("bash", script, "--dest", dest, "--work", work, pinA, pinG)
		cmd.Dir = repo
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+secretPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "cannot fetch the agents-cli source") || strings.Contains(string(out), "SECRETFETCH") || strings.Contains(string(out), "fatal:") {
			t.Errorf("a source that cannot be fetched: want it named by role, and neither its value nor git's own message in the output, got err %v\n%s", err, out)
		}
		if !strings.Contains(string(out), "delete that directory if the value is sensitive") {
			t.Errorf("a failed fetch does not say an override's value stays in the work directory (remote set-url wrote it):\n%s", out)
		}
		if snapshot() != good {
			t.Error("a failed fetch changed the destination")
		}
		// Before the fetch the clone is pointed at the source, and git
		// repeats the value when it cannot write the clone's config (a
		// stale lock file): that message is not shown either (security
		// review of 4165f81..7b3e969).
		lock := filepath.Join(work, "agents-cli", ".git", "config.lock")
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		lockPath := filepath.Join(tmp, "no-such-SECRETLOCK-repository")
		cmd = exec.Command("bash", script, "--dest", dest, "--work", work, pinA, pinG)
		cmd.Dir = repo
		cmd.Env = fixtureEnv("CATALOG_AGENTS_CLI_REPO="+lockPath, "CATALOG_GOOGLE_SKILLS_REPO="+gskills, "CATALOG_DIR=", "CATALOG_WRITE=")
		out, err = cmd.CombinedOutput()
		os.Remove(lock)
		if err == nil || !strings.Contains(string(out), "cannot point the agents-cli clone at its source") || strings.Contains(string(out), "SECRETLOCK") || strings.Contains(string(out), "error:") {
			t.Errorf("a clone whose config cannot be written: want it named by role, and neither the source's value nor git's own message in the output, got err %v\n%s", err, out)
		}
		if snapshot() != good {
			t.Error("a failed set-url changed the destination")
		}
		if out, code := run(pinA, pinG); code != 0 || snapshot() != good {
			t.Fatalf("could not refresh again after the failed fetch (exit %d)\n%s", code, out)
		}
	}
	var noHome []string
	for _, kv := range fixtureEnv() {
		if !strings.HasPrefix(kv, "HOME=") {
			noHome = append(noHome, kv)
		}
	}
	if out, err := guardRun(filepath.Join(tmp, "dest-without-home"), noHome); err == nil || !strings.Contains(out, "HOME is not set") {
		t.Errorf("no HOME: want a refusal saying \"HOME is not set\", got err %v\n%s", err, out)
	}
	if out, code := run(pinA, "main"); code == 0 || !strings.Contains(out, "commit id") {
		t.Errorf("a pin that is not a commit id was accepted (exit %d)\n%s", code, out)
	}
	// An entry named under the manifest's ## Excluded stays out without the flag (S5).
	mf := filepath.Join(dest, "MANIFEST.md")
	orig, _ := os.ReadFile(mf)
	if err := os.WriteFile(mf, bytes.Replace(orig, []byte("## Excluded\n"), []byte("## Excluded\n\n- `epsilon` — excluded by the fixture\n"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := run(pinA, pinG); code != 0 {
		t.Errorf("a refresh with a manifest exclusion failed (exit %d)\n%s", code, out)
	} else if _, err := os.Stat(filepath.Join(dest, "epsilon")); err == nil {
		t.Error("an entry listed under ## Excluded was vendored")
	}
	os.WriteFile(mf, orig, 0o644)
	if out, code := run(pinA, pinG); code != 0 || snapshot() != good {
		t.Fatalf("could not restore the good catalog after the exclusion fixture (exit %d)\n%s", code, out)
	}

	// Refusals: each leaves the destination as it was and names the entry.
	refuse := func(label, names string, files map[string]string, mutate func(), exclude string) {
		t.Helper()
		put(gskills, files)
		if mutate != nil {
			mutate()
		}
		pin := commit(gskills)
		out, code := run(pinA, pin)
		if code != 1 || !strings.Contains(out, names) {
			t.Errorf("%s: want exit 1 naming %q, got exit %d\n%s", label, names, code, out)
		}
		if snapshot() != good {
			t.Errorf("%s: the refused run changed the destination", label)
		}
		if exclude != "" {
			if out, code := run("--exclude", exclude, pinA, pin); code != 0 {
				t.Errorf("%s: --exclude %s did not clear the refusal (exit %d)\n%s", label, exclude, code, out)
			}
			if _, err := os.Stat(filepath.Join(dest, exclude)); err == nil && exclude != "alpha" {
				t.Errorf("%s: the excluded entry %s was vendored", label, exclude)
			}
			// back to the good state for the next fixture
			if out, code := run(pinA, pinG); code != 0 || snapshot() != good {
				t.Fatalf("%s: could not restore the good catalog (exit %d)\n%s", label, code, out)
			}
		}
		git(gskills, "reset", "-q", "--hard", pinG)
		git(gskills, "clean", "-qfd")
	}
	if out, code := run(pinA, "0000000000000000000000000000000000000000"); code != 1 || !strings.Contains(out, "does not resolve in the google-skills source") || snapshot() != good {
		t.Errorf("unresolvable pin: want exit 1 and an untouched destination, got exit %d\n%s", code, out)
	}
	// A pin is a full commit id. git checks a tag or branch named like a
	// shorter one out before the abbreviated id, and a prefix check after the
	// checkout cannot tell a commit made to share the prefix: whoever names
	// refs at the source would choose the content (security reviews of
	// 01255bc..4487a7a and 4487a7a..5bbd789). Both shapes are refused before
	// any clone: a tag named in hex digits, and the real commit's short id.
	git(gskills, "tag", "20261001", pinG)
	for _, short := range []string{"20261001", pinG[:7], pinG[:39]} {
		if out, code := run(pinA, short); code != 1 || !strings.Contains(out, "a pin must be a full commit id (40 hex digits)") || snapshot() != good {
			t.Errorf("a short pin %q: want exit 1, the shape refusal and an untouched destination, got exit %d\n%s", short, code, out)
		}
	}
	git(gskills, "tag", "-d", "20261001")
	// A full id is no protection by itself: `git checkout --detach <id>`
	// takes a local branch of that exact name first, and a clone makes the
	// source's default branch local. A source whose default branch is named
	// like the pin, and points elsewhere, must not choose the content
	// (security review of 5bbd789..42670ff).
	{
		was := git(gskills, "symbolic-ref", "--short", "HEAD")
		put(gskills, map[string]string{"skills/cloud/gamma/PLANTED.md": "content of another commit\n"})
		other := commit(gskills)
		git(gskills, "branch", pinG, other)
		git(gskills, "symbolic-ref", "HEAD", "refs/heads/"+pinG)
		git(gskills, "reset", "-q", "--hard", other)
		bDest, bWork := filepath.Join(tmp, "branch-dest"), filepath.Join(tmp, "branch-work")
		out, err := workRun(bDest, bWork, pinA, pinG)
		if _, statErr := os.Stat(filepath.Join(bDest, "gamma", "PLANTED.md")); statErr == nil {
			t.Errorf("a default branch named like the pin chose the content: the other commit's file was vendored (err %v)\n%s", err, out)
		} else if err != nil {
			t.Errorf("the pinned commit is in the source and must be vendored whatever a branch is called: %v\n%s", err, out)
		} else if _, statErr := os.Stat(filepath.Join(bDest, "gamma", "SKILL.md")); statErr != nil {
			t.Errorf("the run vendored nothing: %v\n%s", statErr, out)
		}
		git(gskills, "symbolic-ref", "HEAD", "refs/heads/"+was)
		git(gskills, "branch", "-D", pinG)
		git(gskills, "reset", "-q", "--hard", pinG)
		git(gskills, "clean", "-qfd")
	}
	// The id of an annotated tag is forty hex digits and peels to a commit:
	// it is not that commit's id, and the manifest would record a pin that
	// names no commit. The comparison after the checkout refuses it.
	git(gskills, "tag", "-a", "annotated", "-m", "a tag object", pinG)
	tagObject := git(gskills, "rev-parse", "annotated")
	if tagObject == pinG {
		t.Fatal("fixture: the annotated tag's id is the commit's id")
	}
	if out, code := run(pinA, tagObject); code != 1 || !strings.Contains(out, "did not check out as itself in the google-skills source") || snapshot() != good {
		t.Errorf("a pin that is a tag object's id: want exit 1, the comparison's refusal and an untouched destination, got exit %d\n%s", code, out)
	}
	git(gskills, "tag", "-d", "annotated")
	refuse("no frontmatter", "bad", map[string]string{"skills/cloud/bad/SKILL.md": "no frontmatter here\n"}, nil, "bad")
	refuse("collision", "alpha", map[string]string{"skills/cloud/alpha/SKILL.md": skill("alpha", "description: a second alpha\n")}, nil, "")
	refuse("symlink", "gamma", nil, func() {
		if err := os.Symlink("SKILL.md", filepath.Join(gskills, "skills/cloud/gamma/link.md")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "gamma")
	refuse("go.mod", "delta", map[string]string{"skills/cloud/delta/go.mod": "module x\n"}, nil, "")
	// An entry with very many offending files is refused in the same words:
	// `find | head -1` under pipefail ended the script with status 141 and no
	// message once find wrote more than a pipe holds (security review of
	// 4165f81..7b3e969). 1,500 dotfiles with long names are about 200 KB.
	refuse("many dotfiles", "holds a dotfile", nil, func() {
		many := map[string]string{}
		for i := 0; i < 1500; i++ {
			many[fmt.Sprintf("skills/cloud/delta/.a-dotfile-with-a-long-name-to-fill-the-pipe-%04d", i)] = "x\n"
		}
		put(gskills, many)
	}, "")
	refuse("go file", "epsilon", map[string]string{"skills/developers/epsilon/main.go": "package main\n"}, nil, "")
	// The hint is printed shell-quoted: the name comes from upstream and the
	// operator pastes the hint into a shell (code review round 3).
	refuse("name with a space", `(--exclude bad\ name)`, map[string]string{"skills/cloud/bad name/SKILL.md": skill("bad-name", "description: a directory name with a space\n")}, nil, "bad name")
	refuse("uppercase name", `(--exclude Loud)`, map[string]string{"skills/cloud/Loud/SKILL.md": skill("loud", "description: a directory name with a capital\n")}, nil, "Loud")
	refuse("shell characters in a name", `(--exclude a\$\(id\))`, map[string]string{"skills/cloud/a$(id)/SKILL.md": skill("a-id", "description: a directory name a shell would expand\n")}, nil, "a$(id)")
	// The refusal prints an upstream name only shell-quoted: a raw escape
	// sequence in a directory name must not reach the operator's terminal
	// (security review of the v2.1.0 gate).
	put(gskills, map[string]string{"skills/cloud/a\x1b[31mb/SKILL.md": skill("a-b", "description: a directory name with a terminal escape\n")})
	escPin := commit(gskills)
	if out, code := run(pinA, escPin); code != 1 || strings.Contains(out, "\x1b") || !strings.Contains(out, "not a plain skill name") {
		t.Errorf("a name with an escape sequence: want exit 1 and no raw escape in the message, got exit %d\n%q", code, out)
	}
	git(gskills, "reset", "-q", "--hard", pinG)
	git(gskills, "clean", "-qfd")
	// The same for a path inside an entry: the dotfile, symlink and Go-file
	// refusals print the path shell-quoted. One fixture per message: each is
	// its own line in the script (the gate's verifier removed the quoting
	// from one and nothing failed).
	for _, c := range []struct {
		label, want string
		make        func()
	}{
		{"dotfile", "holds a dotfile", func() {
			put(gskills, map[string]string{"skills/cloud/delta/.x\x1b[31m": "a dotfile whose name holds an escape\n"})
		}},
		{"Go file", "holds a Go file", func() {
			put(gskills, map[string]string{"skills/cloud/delta/a\x1b[31m.go": "package main\n"})
		}},
		{"symlink", "holds a symlink", func() {
			if err := os.Symlink("SKILL.md", filepath.Join(gskills, "skills/cloud/delta/l\x1b[31mnk")); err != nil {
				t.Skip("no symlinks here")
			}
		}},
	} {
		c.make()
		pin := commit(gskills)
		if out, code := run(pinA, pin); code != 1 || strings.Contains(out, "\x1b") || !strings.Contains(out, c.want) {
			t.Errorf("a nested %s path with an escape sequence: want exit 1, %q and no raw escape in the message, got exit %d\n%q", c.label, c.want, code, out)
		}
		git(gskills, "reset", "-q", "--hard", pinG)
		git(gskills, "clean", "-qfd")
	}
	// An excluded name is one name: excluding "delta epsilon" must not also
	// exclude the entries delta and epsilon.
	put(gskills, map[string]string{"skills/cloud/delta epsilon/SKILL.md": skill("delta-epsilon", "description: two entry names with a space between\n")})
	wordsPin := commit(gskills)
	if out, code := run("--exclude", "delta epsilon", pinA, wordsPin); code != 0 {
		t.Errorf("--exclude \"delta epsilon\" failed (exit %d)\n%s", code, out)
	}
	for _, name := range []string{"delta", "epsilon"} {
		if _, err := os.Stat(filepath.Join(dest, name, "SKILL.md")); err != nil {
			t.Errorf("excluding \"delta epsilon\" also excluded the entry %s", name)
		}
	}
	git(gskills, "reset", "-q", "--hard", pinG)
	git(gskills, "clean", "-qfd")
	if out, code := run(pinA, pinG); code != 0 || snapshot() != good {
		t.Fatalf("could not restore the good catalog after the two-word exclusion (exit %d)\n%s", code, out)
	}
	refuse("dotfile", "delta", map[string]string{"skills/cloud/delta/.gitattributes": "* export-ignore\n"}, nil, "")
	refuse("symlinked entry", "zeta", nil, func() {
		if err := os.Symlink("gamma", filepath.Join(gskills, "skills/cloud/zeta")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "")
	refuse("symlinked source set", "leaves the clone through a symlink", nil, func() {
		// skills/developers becomes a link to a directory elsewhere in the clone.
		if err := os.Rename(filepath.Join(gskills, "skills/developers"), filepath.Join(gskills, "elsewhere")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../elsewhere", filepath.Join(gskills, "skills/developers")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "")
	refuse("symlinked LICENSE", "LICENSE", nil, func() {
		os.Remove(filepath.Join(gskills, "LICENSE"))
		if err := os.Symlink("skills/cloud/gamma/SKILL.md", filepath.Join(gskills, "LICENSE")); err != nil {
			t.Skip("no symlinks here")
		}
	}, "")
}

// TestKitPreambleRefusesLeftoverCredentials: a kit that ends with a
// credential still in its sandbox home fails, by file name and by a token or
// private key written inside any file; one that cleaned up says so (BUG-160).
func TestKitPreambleRefusesLeftoverCredentials(t *testing.T) {
	if testing.Short() {
		t.Skip("runs bash and git")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on this machine", tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	preamble := filepath.Join(root, ".agents", "docs", "testing", "kit-preamble.sh")
	tmp := t.TempDir()
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): the preamble refuses that by design", strings.TrimSpace(string(out)))
	}
	clone, home, kitHome := filepath.Join(tmp, "clone"), filepath.Join(tmp, "home"), filepath.Join(tmp, "kit-home")
	for _, d := range []string{clone, home, kitHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", clone, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	run := func(then string) (int, string) {
		cmd := exec.Command("bash", "-c", ". \"$1\"; "+then, "kit", preamble)
		cmd.Env = fixtureEnv("HOME="+home, "KIT_CLONE="+clone, "KIT_HOME="+kitHome, "KIT_BIN=/bin/true")
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), string(out)
		}
		if err != nil {
			t.Fatalf("running the preamble: %v\n%s", err, out)
		}
		return 0, string(out)
	}
	if code, out := run(":"); code != 0 || !strings.Contains(out, "kit: no credentials left in KIT_HOME") {
		t.Errorf("a clean sandbox home: want exit 0 and the no-credentials line, got exit %d\n%s", code, out)
	}
	// The token below is a shape, assembled so this file holds no literal one.
	token := "gh" + "o_" + strings.Repeat("A", 36)
	// AWS's published example access key, and a real-shaped one that is not it.
	awsExample, awsKey := "AKIA"+"IOSFODNN7EXAMPLE", "AKIA"+"Q3EGRK7TNZ2BXW4M"
	realGrep, err := exec.LookPath("grep")
	if err != nil {
		t.Skip("no grep on this machine")
	}
	for name, then := range map[string]string{
		"an SSH key copied in":          `mkdir -p "$KIT_HOME/.ssh" && : > "$KIT_HOME/.ssh/id_ed25519"`,
		"gcloud credentials":            `mkdir -p "$KIT_HOME/.config/gcloud" && : > "$KIT_HOME/.config/gcloud/credentials.db"`,
		"an Antigravity token":          `mkdir -p "$KIT_HOME/.gemini/antigravity-cli" && : > "$KIT_HOME/.gemini/antigravity-cli/antigravity-oauth-token"`,
		"a token in a terminal log":     `mkdir -p "$KIT_HOME/.gemini/brain" && printf 'declare -x GH_TOKEN=%s\n' "` + token + `" > "$KIT_HOME/.gemini/brain/term1.env"`,
		"a private key in a stray file": `printf -- '-----BEGIN OPENSSH PRIVATE KEY-----\nx\n' > "$KIT_HOME/notes.txt"`,
		// The gaps the 2026-10-05 reviews reproduced: each passed the first check.
		"a symlinked SSH key":        `mkdir -p "$KIT_HOME/.ssh" && : > "$KIT_HOME/real-key" && ln -s ../real-key "$KIT_HOME/.ssh/id_rsa" && mv "$KIT_HOME/real-key" "$KIT_HOME/.ssh/k"`,
		"a key under a nested bin":   `mkdir -p "$KIT_HOME/.local/bin" && printf -- '-----BEGIN RSA PRIVATE KEY-----\n' > "$KIT_HOME/.local/bin/notes"`,
		"a token in a binary file":   `printf 'SQLite\000\001token=%s\000' "` + token + `" > "$KIT_HOME/conv.db"`,
		"a Google access token":      `printf 'Authorization: Bearer ya29.%s\n' "` + strings.Repeat("b", 40) + `" > "$KIT_HOME/trace.log"`,
		"a .netrc":                   `: > "$KIT_HOME/.netrc"`,
		"an unreadable file":         `: > "$KIT_HOME/locked" && chmod 000 "$KIT_HOME/locked"`,
		"an unreadable directory":    `mkdir -p "$KIT_HOME/sealed" && : > "$KIT_HOME/sealed/id_ed25519" && chmod 000 "$KIT_HOME/sealed"`,
		"a symlinked .ssh directory": `mkdir -p "$KIT_HOME/.elsewhere" && : > "$KIT_HOME/.elsewhere/id_ed25519" && ln -s .elsewhere "$KIT_HOME/.ssh" && mv "$KIT_HOME/.elsewhere" "$KIT_HOME/../kit-outside" && ln -sfn ../kit-outside "$KIT_HOME/.ssh"`,
		"a key in the kit's own bin": `printf -- '-----BEGIN EC PRIVATE KEY-----\n' > "$KIT_HOME/bin/stray"`,
		// The example-key exception is exact: a real-shaped AWS key fails alone
		// and beside the example.
		"an AWS access key":                  `printf 'aws_access_key_id = %s\n' "` + awsKey + `" > "$KIT_HOME/trace.log"`,
		"an AWS key beside the example key":  `printf 'SQLite\000%s\000%s\000' "` + awsExample + `" "` + awsKey + `" > "$KIT_HOME/conv.db"`,
		"a GitHub token holding the example": `printf '%s\n' "gh` + `p_` + awsExample + strings.Repeat("Z", 16) + `" > "$KIT_HOME/trace.log"`,
		// A shell function named grep must not blind the scan (release review of v2.6.1).
		"a grep function that finds nothing": `grep() { return 1; }; printf 'aws_access_key_id = %s\n' "` + awsKey + `" > "$KIT_HOME/trace.log"`,
		// grep that cannot read a file for the AWS check (exit 2) fails the run;
		// it never reads as "no key" (security review of v2.6.1, round 2).
		"a grep that cannot read for -o": `mkdir -p "$KIT_HOME/../fakebin" && printf '#!/bin/sh\ncase " $* " in *" -aoE "*) exit 2 ;; esac\nexec ` + realGrep + ` "$@"\n' > "$KIT_HOME/../fakebin/grep" && chmod +x "$KIT_HOME/../fakebin/grep" && printf 'aws_access_key_id = %s\n' "` + awsKey + `" > "$KIT_HOME/trace.log" && PATH="$KIT_HOME/../fakebin:$PATH"`,
		// Shell functions named after the scan's tools never stand in for them.
		"a find function that lists nothing": `find() { :; }; printf 'aws_access_key_id = %s\n' "` + awsKey + `" > "$KIT_HOME/trace.log"`,
		"an od function that says ELF":       `od() { echo 7f454c46; }; printf 'aws_access_key_id = %s\n' "` + awsKey + `" > "$KIT_HOME/trace.log"`,
		// Under xtrace no matched key reaches the log (security review of v2.6.1).
		"a key found under bash -x": `printf 'aws_access_key_id = %s\n%s\n' "` + awsKey + `" "` + token + `" > "$KIT_HOME/trace.log"; set -x`,
	} {
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(name, "an unreadable") && os.Geteuid() == 0 {
				t.Skip("root reads through permissions")
			}
			code, out := run(then)
			if code != 1 || !strings.Contains(out, "credentials left in KIT_HOME") {
				t.Errorf("want exit 1 and the FAIL line, got exit %d\n%s", code, out)
			}
			if strings.Contains(out, token) || strings.Contains(out, awsKey) {
				t.Errorf("the FAIL output prints the matching text; it must print paths only:\n%s", out)
			}
			_ = exec.Command("chmod", "-R", "u+rwx", kitHome).Run()
			if err := os.RemoveAll(kitHome); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(kitHome, 0o755); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A public key is not a credential, and an executable is not scanned: the
	// installed fyr carries the redaction patterns as text.
	if code, out := run(`mkdir -p "$KIT_HOME/.ssh" && : > "$KIT_HOME/.ssh/id_ed25519.pub"`); code != 0 {
		t.Errorf("a public key alone: want exit 0, got %d\n%s", code, out)
	}
	if code, out := run(`mkdir -p "$KIT_HOME/.local/bin" && printf '\177ELF-----BEGIN RSA PRIVATE KEY-----' > "$KIT_HOME/.local/bin/fyr"`); code != 0 {
		t.Errorf("an ELF binary naming the PEM header: want exit 0, got %d\n%s", code, out)
	}
	// AWS's published example key is documentation, not a credential: an agent
	// that reads a test holding it leaves it in its transcripts and database.
	if code, out := run(`mkdir -p "$KIT_HOME/.gemini/brain" && printf '{"user_quote":"%s"}\n' "` + awsExample + `" > "$KIT_HOME/.gemini/brain/transcript.jsonl" && printf 'SQLite\000%s\000' "` + awsExample + `" > "$KIT_HOME/conv.db"`); code != 0 || !strings.Contains(out, "kit: no credentials left in KIT_HOME") {
		t.Errorf("only AWS's example key: want exit 0 and the no-credentials line, got exit %d\n%s", code, out)
	}
	if err := os.RemoveAll(kitHome); err != nil {
		t.Fatal(err)
	}
	// One file, so it is also the last one scanned: the scan's own status never
	// fails the exit check (security review of v2.6.1).
	// The preamble's own wrapper is removed so the example's file is the only
	// one, and the scan's status is asked directly.
	if code, out := run(`printf 'SQLite\000%s\000' "` + awsExample + `" > "$KIT_HOME/conv.db"; rm "$KIT_HOME/bin/fyr"; if kit_credential_leftovers >/dev/null; then echo "scan status 0"; fi`); code != 0 || !strings.Contains(out, "scan status 0") || !strings.Contains(out, "kit: no credentials left in KIT_HOME") {
		t.Errorf("one file holding only AWS's example key: want exit 0 and the no-credentials line, got exit %d\n%s", code, out)
	}
	if err := os.RemoveAll(kitHome); err != nil {
		t.Fatal(err)
	}
	// KIT_HOME itself a symlink: find must follow it, as kit_fingerprint does.
	realHome := filepath.Join(tmp, "real-kit-home")
	if err := os.MkdirAll(filepath.Join(realHome, ".ssh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realHome, ".ssh", "id_ed25519"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, kitHome); err != nil {
		t.Skip("no symlinks here")
	}
	if code, out := run(":"); code != 1 || !strings.Contains(out, "credentials left in KIT_HOME") {
		t.Errorf("a symlinked KIT_HOME holding a key: want exit 1 and the FAIL line, got exit %d\n%s", code, out)
	}
}

// TestKitPreambleAssertsCommit runs the run kits' preamble, which until now
// had only the static pipe check: KIT_ASSERT_COMMIT is a commit SHA in the
// clone's history and nothing else. A ref name is in every clone's history,
// so it would assert nothing (BUG-157 and its addendum).
func TestKitPreambleAssertsCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("runs bash and git")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on this machine", tool)
		}
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	preamble := filepath.Join(root, ".agents", "docs", "testing", "kit-preamble.sh")
	tmp := t.TempDir()
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): the preamble refuses that by design", strings.TrimSpace(string(out)))
	}
	clone, home, kitHome := filepath.Join(tmp, "clone"), filepath.Join(tmp, "home"), filepath.Join(tmp, "kit-home")
	for _, d := range []string{clone, home, kitHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", clone, "-c", "user.email=kit@example.invalid", "-c", "user.name=kit"}, args...)...)
		cmd.Env = fixtureEnv("HOME=" + home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "first")
	first := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "second")
	git("branch", "side", first)
	// git resolves a ref of that name before an abbreviated commit id.
	git("tag", "20261001", first)
	// A commit that exists in the repository and is not in HEAD's history.
	git("checkout", "-q", "-b", "other", first)
	git("commit", "-q", "--allow-empty", "-m", "elsewhere")
	elsewhere := git("rev-parse", "HEAD")
	git("checkout", "-q", "-")
	head := git("rev-parse", "HEAD")
	runThen := func(value, then string) (int, string) {
		cmd := exec.Command("bash", "-c", ". \"$1\"; "+then, "kit", preamble)
		cmd.Env = fixtureEnv("HOME="+home, "KIT_CLONE="+clone, "KIT_HOME="+kitHome, "KIT_BIN=/bin/true", "KIT_ASSERT_COMMIT="+value)
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), string(out)
		}
		if err != nil {
			t.Fatalf("running the preamble: %v\n%s", err, out)
		}
		return 0, string(out)
	}
	run := func(value string) (int, string) { return runThen(value, ":") }
	for _, c := range []struct {
		name, value string
		want        int
		say         string
	}{
		{"the head's full SHA", head, 0, ""},
		{"the head's short SHA", head[:7], 0, ""},
		{"an ancestor's SHA", first[:12], 0, ""},
		{"no assertion", "", 0, ""},
		{"a commit on another branch", elsewhere, 2, "not in the clone's log"},
		{"a SHA no commit has", "deadbeef1", 2, "not in the clone's log"},
		{"HEAD", "HEAD", 2, "must be a commit SHA"},
		{"a branch name", "side", 2, "must be a commit SHA"},
		{"a message search", ":/first", 2, "must be a commit SHA"},
		{"an option", "--all", 2, "must be a commit SHA"},
		{"six hex digits", head[:6], 2, "must be a commit SHA"},
		{"a tag whose name is hex digits", "20261001", 2, "names a ref"},
	} {
		code, out := run(c.value)
		if code != c.want || !strings.Contains(out, c.say) {
			t.Errorf("%s (%q): want exit %d saying %q, got exit %d\n%s", c.name, c.value, c.want, c.say, code, out)
		}
	}
	// The cases above ran in a home with no ~/.config/fyr at all, which
	// once ended the preamble with status 2 and no message (BUG-158). With
	// the directory there: a run that leaves it alone passes, and a run that
	// appends to a file in it, or adds one, fails the kit.
	conf := filepath.Join(home, StateDir)
	if err := os.MkdirAll(conf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conf, "feedback.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runThen("", ":"); code != 0 {
		t.Errorf("a run that leaves the real config alone: want exit 0, got %d\n%s", code, out)
	}
	for name, then := range map[string]string{
		"an appended line": `printf '{}\n' >> "$HOME/.config/fyr/feedback.jsonl"`,
		"a new file":       `: > "$HOME/.config/fyr/state.json"`,
	} {
		if code, out := runThen("", then); code != 1 || !strings.Contains(out, "changed during the run") {
			t.Errorf("a run that writes the real config (%s): want exit 1 and the kit's FAIL line, got exit %d\n%s", name, code, out)
		}
	}
	// The old state directory too, until every place has crossed (plan fyr
	// D29): a run that creates it, writes in it or moves it to the new name
	// fails the kit.
	old := filepath.Join(home, OldStateDir)
	if code, out := runThen("", `mkdir -p "$HOME/.config/sh-agent"`); code != 1 || !strings.Contains(out, "changed during the run") {
		t.Errorf("a run that creates the real ~/.config/sh-agent: want exit 1 and the kit's FAIL line, got exit %d\n%s", code, out)
	}
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "feedback.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := runThen("", ":"); code != 0 || !strings.Contains(out, "kit: real config unchanged") {
		t.Errorf("a run that leaves both real state dirs alone: want exit 0, got %d\n%s", code, out)
	}
	for name, then := range map[string]string{
		"an appended line": `printf '{}\n' >> "$HOME/.config/sh-agent/feedback.jsonl"`,
		"a new file":       `: > "$HOME/.config/sh-agent/state.json"`,
	} {
		if code, out := runThen("", then); code != 1 || !strings.Contains(out, "changed during the run") {
			t.Errorf("a run that writes the real ~/.config/sh-agent (%s): want exit 1 and the kit's FAIL line, got exit %d\n%s", name, code, out)
		}
	}
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	// The same when ~/.config/fyr is a symlink to a directory (a
	// dotfiles setup): `find` without -H stops at the link, and the
	// fingerprint was one constant line.
	real := filepath.Join(tmp, "dotfiles-fyr")
	if err := os.Rename(conf, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, conf); err != nil {
		t.Skip("no symlinks here")
	}
	if code, out := runThen("", ":"); code != 0 {
		t.Errorf("a symlinked config left alone: want exit 0, got %d\n%s", code, out)
	}
	if code, out := runThen("", `: > "$HOME/.config/fyr/through-the-link"`); code != 1 || !strings.Contains(out, "changed during the run") {
		t.Errorf("a write through a symlinked config directory: want exit 1 and the kit's FAIL line, got exit %d\n%s", code, out)
	}
	// A new empty directory through the link: only the names pass sees it
	// (fyr itself makes `proposals/` and `feedback/` there).
	if code, out := runThen("", `mkdir "$HOME/.config/fyr/made-through-the-link"`); code != 1 || !strings.Contains(out, "changed during the run") {
		t.Errorf("a new directory through a symlinked config directory: want exit 1 and the kit's FAIL line, got exit %d\n%s", code, out)
	}
	// A link to something that never ends (a device, a fifo) is in some
	// dotfiles too: its name is listed, and nothing reads through it.
	if err := os.Symlink("/dev/zero", filepath.Join(real, "endless")); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { code, _ := runThen("", ":"); done <- code }()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("a config holding a link to /dev/zero, left alone: want exit 0, got %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the preamble hangs on a link to /dev/zero in the real config: the fingerprint reads through it")
	}
	// The same for a fifo behind a link: opening it would block for ever.
	fifo := filepath.Join(tmp, "dotfiles-fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, filepath.Join(real, "pipe")); err != nil {
		t.Fatal(err)
	}
	go func() { code, _ := runThen("", ":"); done <- code }()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("a config holding a link to a fifo, left alone: want exit 0, got %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the preamble hangs on a link to a fifo in the real config")
	}
	// A link that is re-pointed during the run keeps its name: where it
	// points is part of the fingerprint, for a link to a directory too.
	d1, d2 := filepath.Join(tmp, "dotfiles-d1"), filepath.Join(tmp, "dotfiles-d2")
	for _, d := range []string{d1, d2} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(d1, filepath.Join(real, "sub")); err != nil {
		t.Fatal(err)
	}
	if code, out := runThen("", `ln -sfn "`+d2+`" "$HOME/.config/fyr/sub"`); code != 1 || !strings.Contains(out, "changed during the run") {
		t.Errorf("a link re-pointed at another directory: want exit 1 and the kit's FAIL line, got exit %d\n%s", code, out)
	}
	// A run that leaves everything alone says so in a line of its own: a kit
	// whose output holds neither that line nor the FAIL line did not check.
	if code, out := runThen("", ":"); code != 0 || !strings.Contains(out, "kit: real config unchanged") {
		t.Errorf("a clean run: want exit 0 and the line that says the check ran, got exit %d\n%s", code, out)
	}
	// A new symlink that loops back to the directory is a new name too, and
	// following it must neither hide it nor run away.
	if code, out := runThen("", `ln -s . "$HOME/.config/fyr/loop"`); code != 1 || !strings.Contains(out, "changed during the run") {
		t.Errorf("a new looping symlink in the real config: want exit 1 and the kit's FAIL line, got exit %d\n%s", code, out)
	}
	// And a file in it that is itself a symlink: its target's content counts.
	target := filepath.Join(tmp, "dotfiles-feedback.jsonl")
	if err := os.WriteFile(target, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(real, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	if code, out := runThen("", `printf '{}\n' >> "$HOME/.config/fyr/linked.jsonl"`); code != 1 || !strings.Contains(out, "changed during the run") {
		t.Errorf("an append to a symlinked file in the real config: want exit 1 and the kit's FAIL line, got exit %d\n%s", code, out)
	}
}

// TestKitPreambleKillsLeftoverProcesses pins BUG-163's mechanism: the preamble's exit trap finds a process the
// run left behind (an orphan, as a server the agent started becomes once the CLI exits), sends it SIGTERM and
// fails the run, by cwd in the clone, by an executable under the clone, or by HOME=$KIT_HOME; a process that
// carries none of those is left alone and the run passes.
func TestKitPreambleKillsLeftoverProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("runs bash, git and sleep")
	}
	for _, tool := range []string{"bash", "git", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s on this machine", tool)
		}
	}
	if _, err := os.ReadFile("/proc/self/environ"); err != nil {
		t.Skipf("no readable /proc on this machine (%v): the check reports itself skipped there", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	preamble := filepath.Join(root, ".agents", "docs", "testing", "kit-preamble.sh")
	tmp := t.TempDir()
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): the preamble refuses that by design", strings.TrimSpace(string(out)))
	}
	clone, home, kitHome, other := filepath.Join(tmp, "clone"), filepath.Join(tmp, "home"), filepath.Join(tmp, "kit-home"), filepath.Join(tmp, "other")
	for _, d := range []string{clone, home, kitHome, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", clone, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	pids := filepath.Join(tmp, "pids")
	t.Cleanup(func() { // whatever the check did, nothing planted outlives the test
		b, _ := os.ReadFile(pids)
		for _, f := range strings.Fields(string(b)) {
			_ = exec.Command("kill", f).Run()
		}
	})
	// A zombie still has a /proc entry; only a process that is not exiting counts as alive.
	alive := func(pid string) bool {
		b, err := os.ReadFile("/proc/" + pid + "/status")
		return err == nil && !strings.Contains(string(b), "State:\tZ")
	}
	run := func(then string) (int, string) {
		cmd := exec.Command("bash", "-c", ". \"$1\"; "+then, "kit", preamble)
		cmd.Env = fixtureEnv("HOME="+home, "KIT_CLONE="+clone, "KIT_HOME="+kitHome, "KIT_BIN=/bin/true", "PIDS="+pids, "OTHER="+other)
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), string(out)
		}
		if err != nil {
			t.Fatalf("running the preamble: %v\n%s", err, out)
		}
		return 0, string(out)
	}
	if code, out := run(":"); code != 0 || !strings.Contains(out, "kit: no processes left by the run") {
		t.Fatalf("a clean run: want exit 0 and the no-processes line, got exit %d\n%s", code, out)
	}
	// Each planted process is an orphan (a double fork), its output detached so it cannot hold the test's pipe.
	plant := func(cmd string) string {
		return `( ( ` + cmd + ` </dev/null >/dev/null 2>&1 & echo $! > "$PIDS.last"; cat "$PIDS.last" >> "$PIDS" ) )`
	}
	lastPID := func() string { b, _ := os.ReadFile(pids + ".last"); return strings.TrimSpace(string(b)) }
	for name, cmd := range map[string]string{
		"a server left in the clone":        `cd "$KIT_CLONE" && exec sleep 301`,
		"an executable under the clone":     `cp "$(command -v sleep)" "$KIT_CLONE/srv" && cd "$OTHER" && exec "$KIT_CLONE/srv" 302`,
		"a process carrying HOME=$KIT_HOME": `cd "$OTHER" && HOME="$KIT_HOME" exec sleep 303`,
	} {
		code, out := run(plant(cmd))
		pid := lastPID()
		if pid == "" {
			t.Fatalf("%s: the fixture planted nothing\n%s", name, out)
		}
		if code != 1 || !strings.Contains(out, "kit: FAIL processes left by the run (BUG-163)") || !strings.Contains(out, "  "+pid+" (") {
			t.Errorf("%s: want exit 1 and a FAIL line naming pid %s, got exit %d\n%s", name, pid, code, out)
		}
		if alive(pid) {
			t.Errorf("%s: pid %s is still running after the check", name, pid)
		}
	}
	// Near misses are not the run's: a HOME that only starts with KIT_HOME, and the kit shell's own child
	// (a descendant is the check itself or the still-running kit, skipped by design).
	for name, then := range map[string]string{
		"a HOME that only starts with KIT_HOME": plant(`cd "$OTHER" && HOME="$KIT_HOME-x" exec sleep 305`),
		"the kit shell's own child":             `( cd "$KIT_CLONE" && exec sleep 306 </dev/null >/dev/null 2>&1 ) & echo $! > "$PIDS.last"; cat "$PIDS.last" >> "$PIDS"`,
	} {
		code, out := run(then)
		pid := lastPID()
		if code != 0 || !strings.Contains(out, "kit: no processes left by the run") {
			t.Errorf("%s: want exit 0 and the no-processes line, got exit %d\n%s", name, code, out)
		}
		if pid == "" || !alive(pid) {
			t.Errorf("%s: pid %q was killed or never started", name, pid)
		}
		// Gone before the next case: once its kit shell exits, the child is an orphan in the clone, which
		// the next run would rightly flag.
		_ = exec.Command("kill", pid).Run()
		for i := 0; i < 50 && alive(pid); i++ {
			time.Sleep(20 * time.Millisecond)
		}
	}
	// Roots the trap would kill under are refused at load: the real home reached through "/.", and /.
	homeLink := filepath.Join(tmp, "home-link")
	if err := os.Symlink(home, homeLink); err != nil {
		t.Fatal(err)
	}
	const realHome, checkout, absolute = "is the real home or a parent of it; refusing", "is the harness checkout or a parent of it; refusing", "must be an absolute path"
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"KIT_HOME is the real home via /.":      {[]string{"KIT_HOME=" + home + "/."}, realHome},
		"KIT_HOME is a link to the real home":   {[]string{"KIT_HOME=" + homeLink}, realHome},
		"KIT_HOME is a parent of the real home": {[]string{"KIT_HOME=" + tmp}, realHome},
		"KIT_HOME is /":                         {[]string{"KIT_HOME=/"}, "a kit root resolves to /; refusing"},
		"KIT_HOME is the harness checkout":      {[]string{"KIT_HOME=" + root}, checkout},
		"KIT_HOME is a parent of the checkout":  {[]string{"KIT_HOME=" + filepath.Dir(root)}, checkout},
		"KIT_CLONE is relative":                 {[]string{"KIT_CLONE=clone"}, absolute},
	} {
		env := append([]string{"HOME=" + home, "KIT_CLONE=" + clone, "KIT_HOME=" + kitHome, "KIT_BIN=/bin/true"}, c.env...)
		cmd := exec.Command("bash", "-c", ". \"$1\"; :", "kit", preamble)
		cmd.Dir = tmp
		cmd.Env = fixtureEnv(env...)
		out, err := cmd.CombinedOutput()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != 2 || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: want exit 2 and %q, got %v\n%s", name, c.want, err, out)
		}
	}
	// The negative control: a process outside the clone, with the run's real HOME, is not the run's.
	code, out := run(plant(`cd "$OTHER" && exec sleep 304`))
	pid := lastPID()
	if code != 0 || !strings.Contains(out, "kit: no processes left by the run") {
		t.Errorf("an unrelated process: want exit 0 and the no-processes line, got exit %d\n%s", code, out)
	}
	if pid == "" || !alive(pid) {
		t.Errorf("an unrelated process (pid %q) was killed or never started", pid)
	}
}
