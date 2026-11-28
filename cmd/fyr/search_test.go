package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/harness"
)

func TestReorderSearchFlags(t *testing.T) {
	t.Parallel()
	// --limit, --kind and --refs take values: were reorder not told so they
	// would swallow the next word (BUG-002's shape).
	cases := map[string][]string{
		"search zip --kind bug verify --limit 3":                {"--kind", "bug", "--limit", "3", "search", "zip", "verify"},
		"search --refs BUG-049 --global":                        {"--refs", "BUG-049", "--global", "search"},
		"search --open --kind plan":                             {"--open", "--kind", "plan", "search"},
		"search --contracts internal/search --budget 80 --json": {"--contracts", "internal/search", "--budget", "80", "--json", "search"},
		"index --rebuild --home /h":                             {"--rebuild", "--home", "/h", "index"},
		"search -- --from-zip is a word":                        {"--", "search", "--from-zip", "is", "a", "word"},
	}
	for in, want := range cases {
		if got := reorder(allFlags(), strings.Fields(in)); !reflect.DeepEqual(got, want) {
			t.Errorf("reorder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchVerb(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644)
	os.WriteFile(filepath.Join(root, "notes.md"), []byte("# Notes\n\nthe marmot decision\n"), 0o644)
	t.Chdir(root)
	home := t.TempDir()

	var out bytes.Buffer
	if err := run([]string{"search", "marmot", "--home", home}, &out); err != nil {
		t.Fatalf("search: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "notes.md:3  doc") {
		t.Errorf("search output:\n%s", out.String())
	}
	out.Reset()
	if err := run([]string{"index", "--home", home}, &out); err != nil {
		t.Fatalf("index: %v", err)
	}
	if !strings.Contains(out.String(), "files    2") {
		t.Errorf("index output:\n%s", out.String())
	}
	out.Reset()
	// A usage error: exit 2, the reason on the writer (BUG-168).
	err := run([]string{"search", "--limit", "99", "marmot", "--home", home}, &out)
	var code exitCode
	if !errors.As(err, &code) || code != 2 || !strings.Contains(out.String(), "--limit") {
		t.Errorf("--limit 99: err = %v, out = %q, want exit 2 naming --limit", err, out.String())
	}
	// --global runs from an unregistered checkout and creates the global
	// search index (registration is TestIndexRegisterWritesMarkerThenIndexes').
	out.Reset()
	if err := run([]string{"search", "--global", "marmot", "--home", home}, &out); err != nil {
		t.Fatalf("search --global: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "search.db")); err != nil {
		t.Errorf("global search index not created: %v", err)
	}
	if st, _ := exec.Command("git", "-C", root, "status", "--porcelain").Output(); strings.Contains(string(st), ".agents") {
		t.Errorf("the index dirtied the checkout: %s", st)
	}
}

// TestUsageMatchesDocComment: every verb the short page lists is in the
// package doc comment, and back.
func TestUsageMatchesDocComment(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^//\tfyr ([a-z][a-z-]*)`).FindAllStringSubmatch(string(src), -1) {
		doc[m[1]] = true
	}
	use := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  ([a-z][a-z-]*) `).FindAllStringSubmatch(shortPage, -1) {
		use[m[1]] = true
	}
	keys := func(m map[string]bool) []string {
		var k []string
		for v := range m {
			k = append(k, v)
		}
		sort.Strings(k)
		return k
	}
	if !reflect.DeepEqual(keys(doc), keys(use)) {
		t.Errorf("doc comment verbs %v, usage verbs %v", keys(doc), keys(use))
	}
	for _, v := range []string{"search", "index"} {
		if !use[v] {
			t.Errorf("usage lacks %s", v)
		}
	}
}

func TestSearchContractsCLI(t *testing.T) {
	root := recordRepo(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".agents/scratch/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pkg", "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "ledger", "entry.go"), []byte("package ledger\n\nfunc AppendEntry(id string) error { return nil }\nfunc helper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "ledger", "client.ts"), []byte("export interface LedgerItem { id: string; }\nexport function loadLedger(): LedgerItem[] { return []; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bo := brain.Options{Home: home, Dir: root}
	adrID, err := brain.Add(bo, brain.AddInput{
		Kind: "adr", Status: "accepted", Title: "Append-only ledger invariant",
		Body: strings.NewReader("Applies to `pkg/ledger/`.\n"),
	})
	if err != nil {
		t.Fatalf("Add adr: %v", err)
	}
	specBody := strings.Join([]string{
		"# SPEC-ledger",
		"## Purpose",
		"Defines `pkg/ledger/` contract (" + adrID + ").",
		"## Interfaces",
		"```go",
		"func AppendEntry(id string) error",
		"```",
		"## Data",
		"- Entry IDs are immutable.",
		"## Failure modes",
		"| Condition | Trigger | Behavior | Recovery |",
		"| --- | --- | --- | --- |",
		"| Empty ID | id == \"\" | Return ErrEmpty | Provide non-empty ID |",
		"## Verification",
		"- Run `go test ./pkg/ledger/...`.",
	}, "\n")
	if _, err := brain.Add(bo, brain.AddInput{
		Kind: "spec", ID: "SPEC-ledger", Status: "current", Title: "Ledger surface spec",
		Body: strings.NewReader(specBody),
	}); err != nil {
		t.Fatalf("Add spec: %v", err)
	}

	t.Chdir(root)
	var out1, out2 bytes.Buffer
	if err := run([]string{"search", "--contracts", "pkg/ledger", "--home", home}, &out1); err != nil {
		t.Fatalf("search --contracts run 1: %v\n%s", err, out1.String())
	}
	if err := run([]string{"search", "--contracts", "pkg/ledger", "--home", home}, &out2); err != nil {
		t.Fatalf("search --contracts run 2: %v\n%s", err, out2.String())
	}
	if out1.String() != out2.String() {
		t.Fatalf("CLI --contracts not byte-identical:\n%s\nvs\n%s", out1.String(), out2.String())
	}
	got := out1.String()
	for _, want := range []string{
		"# contracts: pkg/ledger (budget: 1500 tokens)",
		"[CODE_SIGNATURES]",
		"AppendEntry(id string) error",
		"LedgerItem",
		"[SPEC_CONTRACTS] (SPEC-ledger)",
		"[INVARIANTS] (SPEC-ledger)",
		"- Entry IDs are immutable.",
		"[FAULTS] (SPEC-ledger)",
		"[GUARDRAILS]",
		"- " + adrID + ": Append-only ledger invariant",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CLI --contracts missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "helper()") {
		t.Errorf("unexported helper leaked:\n%s", got)
	}

	// --budget flag enforcement + UTF-8 safety.
	var budOut bytes.Buffer
	if err := run([]string{"search", "--contracts", "pkg/ledger", "--budget", "60", "--home", home}, &budOut); err != nil {
		t.Fatalf("search --contracts --budget 60: %v", err)
	}
	if budOut.Len() > 240 || !utf8.ValidString(budOut.String()) {
		t.Errorf("--budget 60 invalid: len=%d validUTF8=%v\n%s", budOut.Len(), utf8.ValidString(budOut.String()), budOut.String())
	}

	// --json output support.
	var jsonBuf bytes.Buffer
	if err := run([]string{"search", "--contracts", "pkg/ledger", "--json", "--home", home}, &jsonBuf); err != nil {
		t.Fatalf("search --contracts --json: %v\n%s", err, jsonBuf.String())
	}
	var payload struct {
		Output string   `json:"output"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &payload); err != nil {
		t.Fatalf("invalid --json payload: %v\n%s", err, jsonBuf.String())
	}
	if !strings.Contains(payload.Output, "# contracts: pkg/ledger") || !strings.Contains(payload.Output, "SPEC-ledger") {
		t.Errorf("unexpected --json output: %+v", payload)
	}

	// Must-fail: missing target exits 1 with exact error.
	var missBuf bytes.Buffer
	err = run([]string{"search", "--contracts", "no/such/target.go", "--home", home}, &missBuf)
	var code exitCode
	if err == nil || errors.As(err, &code) {
		t.Fatalf("missing target: err=%v, want exit 1 error", err)
	}
	if !strings.Contains(err.Error(), "search: no indexed symbols or linked specs for no/such/target.go") {
		t.Errorf("missing target err = %q", err.Error())
	}

	// Must-fail: combining --contracts with another mode exits 2 naming --contracts.
	for _, args := range [][]string{
		{"search", "--contracts", "pkg/ledger", "extraword", "--home", home},
		{"search", "--contracts", "pkg/ledger", "--slice", "AppendEntry", "--home", home},
		{"search", "--contracts", "pkg/ledger", "--refs", adrID, "--home", home},
		{"search", "--contracts", "pkg/ledger", "--map", "--home", home},
		{"search", "--contracts", "pkg/ledger", "--hook", "pre-invocation", "--home", home},
	} {
		var errBuf bytes.Buffer
		err := run(args, &errBuf)
		var ec exitCode
		if !errors.As(err, &ec) || ec != 2 || !strings.Contains(errBuf.String(), "--contracts") {
			t.Errorf("args %v: err=%v out=%q, want exit 2 naming --contracts", args, err, errBuf.String())
		}
	}
}
