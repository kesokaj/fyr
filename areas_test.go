package fyr

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const areasPath = "skills/fyr-harness-audit/references/areas.md"

var (
	// runPattern reads a `-run` argument, quoted or bare, as areas.md writes it.
	runPattern = regexp.MustCompile(`-run (?:'([^']+)'|"([^"]+)"|([A-Za-z0-9_|]+))`)
	testFunc   = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
)

type goTestRun struct {
	pkg string
	pat string
}

// TestAreasRunPatternsExist pins WS53 of the 2026-10-27 audit and AG37 of the
// 2026-11-19 audit: every alternative of every `-run` pattern the harness-audit
// areas hand an agent names the start of a test function in the named package's
// tracked _test.go. Go prints `ok` when a pattern matches nothing or targets the
// wrong package.
func TestAreasRunPatternsExist(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("not a git checkout: tracked test files cannot be listed")
	}
	text, err := os.ReadFile(areasPath)
	if err != nil {
		t.Fatal(err)
	}
	funcs := trackedTestFuncs(t)
	checkAreasPatterns(t, string(text), funcs)
	checkAreasMutations(t, string(text), funcs)
}

func checkAreasPatterns(t *testing.T, text string, funcs map[string][]string) {
	t.Helper()
	runs := parseGoTestRuns(text)
	if got, want := len(runs), len(runPattern.FindAllString(text, -1)); got != want {
		t.Errorf("%s: parsed %d `go test ... -run` patterns, want %d", areasPath, got, want)
	}
	missing, n := missingRunPatterns(text, funcs)
	if n == 0 {
		t.Fatalf("%s holds no -run pattern; the reader no longer matches its shape", areasPath)
	}
	for _, m := range missing {
		t.Errorf("%s: -run alternative %q names no test function in its package", areasPath, m)
	}
}

func checkAreasMutations(t *testing.T, text string, funcs map[string][]string) {
	t.Helper()
	renamed := strings.Replace(text, "-run TestHarnessMap", "-run TestHarnessMapRenamedAway", 1)
	if renamed == text {
		t.Fatal("areas.md no longer holds `-run TestHarnessMap`; pick another pattern for the must-fail copy")
	}
	if missing, _ := missingRunPatterns(renamed, funcs); !slices.Contains(missing, "cmd/fyr:TestHarnessMapRenamedAway") {
		t.Errorf("a renamed pattern was not caught: missing=%q", missing)
	}
	drifted := strings.Replace(text, "go test ./cmd/fyr -run TestHarnessMap", "go test ./internal/harness -run TestHarnessMap", 1)
	if drifted == text {
		t.Fatal("areas.md no longer holds `go test ./cmd/fyr -run TestHarnessMap`; pick another command for the must-fail copy")
	}
	if missing, _ := missingRunPatterns(drifted, funcs); !slices.Contains(missing, "internal/harness:TestHarnessMap") {
		t.Errorf("package-path drift was not caught: missing=%q", missing)
	}
}

func TestMissingRunPatternsScopesPackage(t *testing.T) {
	funcs := map[string][]string{
		".":                    {"TestRootPin"},
		"cmd/fyr":              {"TestHarnessMap"},
		"internal/harness":     {"TestLint"},
		"internal/harness/sub": {"TestSub"},
	}
	good := "`go test ./cmd/fyr -run TestHarnessMap`\n`go test -run TestRootPin`\n`go test ./internal/harness/... -run 'TestLint|TestSub'`\n`go test -race ./... -run '^TestSub$'`"
	if missing, n := missingRunPatterns(good, funcs); n != 5 || len(missing) != 0 {
		t.Fatalf("good suite: missing=%q n=%d, want none and 5", missing, n)
	}
	bad := "`go test ./internal/harness -run TestHarnessMap`"
	if missing, n := missingRunPatterns(bad, funcs); n != 1 || !slices.Contains(missing, "internal/harness:TestHarnessMap") {
		t.Fatalf("drifted suite: missing=%q n=%d, want internal/harness:TestHarnessMap", missing, n)
	}
}

// missingRunPatterns returns `<pkg>:<alt>` entries that match no test function
// in the named package, and how many alternatives it read.
func missingRunPatterns(text string, funcsByPkg map[string][]string) ([]string, int) {
	var missing []string
	n := 0
	for _, r := range parseGoTestRuns(text) {
		for _, alt := range strings.Split(r.pat, "|") {
			n++
			if !pkgHasFunc(funcsByPkg, r.pkg, alt) {
				missing = append(missing, normPkg(r.pkg)+":"+alt)
			}
		}
	}
	return missing, n
}

func pkgHasFunc(funcsByPkg map[string][]string, pkg, alt string) bool {
	exact := strings.HasSuffix(alt, "$")
	prefix := strings.TrimPrefix(strings.TrimSuffix(alt, "$"), "^")
	prefix, _, _ = strings.Cut(prefix, "/")
	for _, fn := range funcsForPkg(funcsByPkg, pkg) {
		if (exact && fn == prefix) || (!exact && strings.HasPrefix(fn, prefix)) {
			return true
		}
	}
	return false
}

func funcsForPkg(funcsByPkg map[string][]string, pkg string) []string {
	if pkg == "./..." || pkg == "..." {
		var all []string
		for _, list := range funcsByPkg {
			all = append(all, list...)
		}
		return all
	}
	if strings.HasSuffix(pkg, "/...") {
		base := normPkg(strings.TrimSuffix(pkg, "/..."))
		var matched []string
		for dir, list := range funcsByPkg {
			if dir == base || strings.HasPrefix(dir, base+"/") {
				matched = append(matched, list...)
			}
		}
		return matched
	}
	return funcsByPkg[normPkg(pkg)]
}

func normPkg(pkg string) string {
	p := strings.TrimSuffix(strings.TrimPrefix(pkg, "./"), "/")
	if p == "" {
		return "."
	}
	return p
}

func parseGoTestRuns(text string) []goTestRun {
	var runs []goTestRun
	for i := 0; i < len(text); {
		rel := strings.Index(text[i:], "go test ")
		if rel < 0 {
			break
		}
		start := i + rel
		if start > 0 && !strings.ContainsRune(" \t\n`;|&(", rune(text[start-1])) {
			i = start + len("go test ")
			continue
		}
		seg, end := readCmdSegment(text[start:])
		pkgs, pats := parseGoTestArgs(shellFields(seg)[2:])
		for _, pat := range pats {
			for _, pkg := range pkgs {
				runs = append(runs, goTestRun{pkg: pkg, pat: pat})
			}
		}
		i = start + end
	}
	return runs
}

func readCmdSegment(s string) (string, int) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if strings.ContainsRune("`;\n|&)", rune(c)) {
			return s[:i], i
		}
	}
	return s, len(s)
}

func shellFields(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	inTok := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote, inTok = c, true
			continue
		}
		if c == ' ' || c == '\t' {
			if inTok {
				out = append(out, cur.String())
				cur.Reset()
				inTok = false
			}
			continue
		}
		cur.WriteByte(c)
		inTok = true
	}
	if inTok {
		out = append(out, cur.String())
	}
	return out
}

func parseGoTestArgs(args []string) (pkgs, pats []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-run" && i+1 < len(args):
			pats = append(pats, args[i+1])
			i++
		case strings.HasPrefix(a, "-run="):
			pats = append(pats, strings.TrimPrefix(a, "-run="))
		case takesGoTestArg(a) && i+1 < len(args):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			pkgs = append(pkgs, a)
		}
	}
	if len(pkgs) == 0 && len(pats) > 0 {
		pkgs = []string{"."}
	}
	return pkgs, pats
}

func takesGoTestArg(flag string) bool {
	switch flag {
	case "-bench", "-benchtime", "-count", "-covermode", "-coverpkg",
		"-coverprofile", "-cpu", "-exec", "-fuzz", "-fuzztime",
		"-gcflags", "-ldflags", "-mod", "-outputdir", "-parallel",
		"-tags", "-timeout":
		return true
	}
	return false
}

// trackedTestFuncs lists every top-level Test function keyed by package dir.
func trackedTestFuncs(t *testing.T) map[string][]string {
	t.Helper()
	out, err := exec.Command("git", "ls-files", "-z", "--", "*_test.go").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	funcs := map[string][]string{}
	for _, f := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		if len(f) == 0 {
			continue
		}
		src, err := os.ReadFile(string(f))
		if err != nil {
			t.Fatal(err)
		}
		dir := path.Dir(string(f))
		for _, m := range testFunc.FindAllSubmatch(src, -1) {
			funcs[dir] = append(funcs[dir], string(m[1]))
		}
	}
	return funcs
}

var activeRootDocs = []string{"AGENTS.md", "PRD.md", "README.md", "SDD.md", "SECURITY.md", "TDD.md", "TODO.md", "Workflow.md"}

// TestAreasPackageParity pins that skills/fyr-harness-audit/references/areas.md
// names every tracked internal/* package dir, every root *_test.go (including
// arch_fitness_test.go), every features/*.md, and every active root doc.
func TestAreasPackageParity(t *testing.T) {
	requireGitCheckout(t)
	textBytes, err := os.ReadFile(areasPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(textBytes)
	want := collectAreasParityTargets(t)
	for _, m := range missingAreaRefs(text, want) {
		t.Errorf("%s does not reference %q", areasPath, m)
	}
	for _, probe := range []string{"internal/gate", "arch_fitness_test.go", "features/brain-and-memory.md", "SDD.md"} {
		mutated := strings.ReplaceAll(text, probe, "REDACTED_TARGET")
		if mutated != text && !slices.Contains(missingAreaRefs(mutated, want), probe) {
			t.Errorf("dropping %q from %s was not caught", probe, areasPath)
		}
	}
}

func collectAreasParityTargets(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "ls-files", "-z", "-co", "--exclude-standard").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	seen := map[string]bool{"arch_fitness_test.go": true}
	for _, d := range activeRootDocs {
		seen[d] = true
	}
	for _, raw := range bytes.Split(bytes.TrimRight(out, "\x00"), []byte{0}) {
		p := string(raw)
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(p, "internal/") && strings.HasSuffix(p, ".go"):
			if parts := strings.Split(p, "/"); len(parts) >= 3 {
				seen[parts[0]+"/"+parts[1]] = true
			}
		case !strings.Contains(p, "/") && strings.HasSuffix(p, "_test.go"):
			seen[p] = true
		case strings.HasPrefix(p, "features/") && strings.HasSuffix(p, ".md") && strings.Count(p, "/") == 1:
			seen[p] = true
		}
	}
	targets := make([]string, 0, len(seen))
	for k := range seen {
		targets = append(targets, k)
	}
	slices.Sort(targets)
	return targets
}

func missingAreaRefs(text string, targets []string) []string {
	var missing []string
	for _, target := range targets {
		if !areasMentionsTarget(text, target) {
			missing = append(missing, target)
		}
	}
	return missing
}

func areasMentionsTarget(text, target string) bool {
	hasSlash := strings.Contains(target, "/")
	isWord := func(b byte) bool {
		return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == '.'
	}
	for off := 0; off < len(text); {
		idx := strings.Index(text[off:], target)
		if idx < 0 {
			return false
		}
		start, end := off+idx, off+idx+len(target)
		beforeOK := start == 0 || (!isWord(text[start-1]) && (hasSlash || text[start-1] != '/'))
		afterOK := end == len(text) || !isWord(text[end])
		if beforeOK && afterOK {
			return true
		}
		off = start + 1
	}
	return false
}
