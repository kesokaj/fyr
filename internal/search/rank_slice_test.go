package search

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersonalizedPageRank(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	// Create > 5 files defining a generic helper "commonUtil" and an unexported
	// helper "logHelper" called everywhere, plus a target pivot "ReconcileIndex"
	// that calls "VerifyTreeState".
	for i := 1; i <= 6; i++ {
		writeFile(t, root, fmt.Sprintf("internal/util/u%d.go", i), `package util
func CommonUtil() { logHelper() }
func logHelper() {}
`)
	}
	writeFile(t, root, "go.mod", "module example\n\ngo 1.24\n")
	writeFile(t, root, "internal/core/reconcile.go", `package core
import "example/internal/util"

func ReconcileIndex() error {
	util.CommonUtil()
	return VerifyTreeState()
}

func VerifyTreeState() error {
	util.CommonUtil()
	return nil
}
`)
	writeFile(t, root, "internal/core/session_active.go", `package core
func ActiveSessionHandler() error {
	return ReconcileIndex()
}
`)
	commitAll(t, root, "seed graph")

	sc, err := prepare(Options{Dir: root, Home: home}, io.Discard)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer sc.close()

	g, err := sc.loadSymGraphOpts(false)
	if err != nil {
		t.Fatalf("loadSymGraphOpts: %v", err)
	}
	ranked := g.rank("ReconcileIndex", map[string]bool{"internal/core/session_active.go": true})
	if len(ranked) == 0 {
		t.Fatalf("expected non-empty ranked symbols, got 0")
	}

	scoreOf := map[string]float64{}
	for _, s := range ranked {
		if scoreOf[s.Name] == 0 || s.Score > scoreOf[s.Name] {
			scoreOf[s.Name] = s.Score
		}
	}
	if scoreOf["ReconcileIndex"] <= scoreOf["CommonUtil"] {
		t.Errorf("target pivot ReconcileIndex (%f) should rank above >5-file CommonUtil (%f)", scoreOf["ReconcileIndex"], scoreOf["CommonUtil"])
	}
	if scoreOf["ReconcileIndex"] <= scoreOf["logHelper"] {
		t.Errorf("target pivot ReconcileIndex (%f) should rank above unexported logHelper (%f)", scoreOf["ReconcileIndex"], scoreOf["logHelper"])
	}
	if scoreOf["ActiveSessionHandler"] <= scoreOf["CommonUtil"] {
		t.Errorf("active-file symbol ActiveSessionHandler (%f) should rank above CommonUtil (%f)", scoreOf["ActiveSessionHandler"], scoreOf["CommonUtil"])
	}
	if scoreOf["VerifyTreeState"] <= scoreOf["logHelper"] {
		t.Errorf("direct callee VerifyTreeState (%f) should rank above unexported logHelper (%f)", scoreOf["VerifyTreeState"], scoreOf["logHelper"])
	}
}

func TestBuildContextSliceAndBudget(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	var longBody strings.Builder
	longBody.WriteString("package core\n\nfunc LongHopOneCallee() error {\n")
	for i := 1; i <= 42; i++ {
		fmt.Fprintf(&longBody, "\t_ = %d\n", i)
	}
	longBody.WriteString("\treturn HopTwoLeaf()\n}\n\nfunc HopTwoLeaf() error {\n\treturn nil\n}\n")
	writeFile(t, root, "internal/core/callee.go", longBody.String())

	writeFile(t, root, "internal/core/reconcile.go", `package core

// Reconcile coordinates index reconciliation (see ADR-511).
func Reconcile() error {
	if err := ShortHopOneHelper(); err != nil {
		return err
	}
	return LongHopOneCallee()
}

func ShortHopOneHelper() error {
	return HopTwoSecondLeaf()
}

func HopTwoSecondLeaf() error {
	return nil
}
`)
	commitAll(t, root, "seed slice repo")

	var buf bytes.Buffer
	err := BuildContextSlice(Options{
		Dir:    root,
		Home:   home,
		Slice:  "Reconcile",
		Hops:   2,
		Budget: 1800,
	}, &buf)
	if err != nil {
		t.Fatalf("BuildContextSlice: %v", err)
	}
	got := buf.String()
	if got == "" {
		t.Fatalf("expected non-empty slice output")
	}
	tokens := (len(got) + 3) / 4
	if tokens > 1800 {
		t.Errorf("slice token estimate = %d (%d bytes), want <= 1800", tokens, len(got))
	}
	for _, want := range []string{"[Hop 0]", "[Hop 1]", "[Hop 2]", "Reconcile", "ShortHopOneHelper", "LongHopOneCallee", "HopTwoLeaf"} {
		if !strings.Contains(got, want) {
			t.Errorf("slice output missing %q in:\n%s", want, got)
		}
	}
	// LongHopOneCallee is > 40 lines, so Hop 1 must emit a snippet rather than all 42 assignment lines.
	if strings.Contains(got, "_ = 25") {
		t.Errorf("expected >40-line Hop-1 symbol LongHopOneCallee to be snippeted, got full body:\n%s", got)
	}
}

func TestSearchFileSkeleton(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	writeFile(t, root, "internal/core/sample.go", `package core

type Engine struct {
	Name string
}

func (e *Engine) Run(limit int) error {
	x := limit + 1
	_ = x
	return nil
}
`)
	commitAll(t, root, "seed skeleton")

	var buf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Skeleton: "internal/core/sample.go"}, &buf); err != nil {
		t.Fatalf("Search skeleton: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "3-5  type Engine struct") {
		t.Errorf("skeleton missing Engine declaration in:\n%s", got)
	}
	if !strings.Contains(got, "7-11  func (e *Engine) Run(limit int) error") {
		t.Errorf("skeleton missing Run signature in:\n%s", got)
	}
	if strings.Contains(got, "limit + 1") {
		t.Errorf("skeleton must not contain function bodies, got:\n%s", got)
	}
}

func TestFindSymbolPath(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	writeFile(t, root, "internal/core/chain.go", `package core

type Runner interface {
	Execute() error
}

type Worker struct{}

func (w *Worker) Execute() error {
	return LeafTarget()
}

func EntryPoint(r Runner) error {
	return r.Execute()
}

func LeafTarget() error {
	return nil
}
`)
	commitAll(t, root, "seed path repo")

	var buf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Path: "EntryPoint", To: "LeafTarget"}, &buf); err != nil {
		t.Fatalf("FindSymbolPath: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"EntryPoint", "Execute", "LeafTarget", "[EXTRACTED]", "[INFERRED]"} {
		if !strings.Contains(got, want) {
			t.Errorf("path output missing %q in:\n%s", want, got)
		}
	}
}

func TestFindAffected(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	writeFile(t, root, "internal/core/target.go", `package core

// CoreStore handles storage (fixes BUG-042).
func CoreStore() error {
	return nil
}

func DirectCaller() error {
	return CoreStore()
}

func TransitiveCaller() error {
	return DirectCaller()
}
`)
	writeFile(t, root, "internal/core/target_test.go", `package core

import "testing"

func TestDirectCaller(t *testing.T) {
	_ = DirectCaller()
}
`)
	commitAll(t, root, "seed affected repo")

	var buf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Affected: "CoreStore"}, &buf); err != nil {
		t.Fatalf("FindAffected: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"direct_callers:", "DirectCaller", "transitive_callers:", "TransitiveCaller", "tests_to_run:", "TestDirectCaller", "linked_records:", "BUG-042"} {
		if !strings.Contains(got, want) {
			t.Errorf("affected output missing %q in:\n%s", want, got)
		}
	}
}

func TestRepoMapSummary(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	writeFile(t, root, "go.mod", "module example.com\n\ngo 1.24\n")
	writeFile(t, root, "internal/alpha/alpha.go", `package alpha
import "example.com/internal/beta"
func AlphaMain() { beta.BetaHelper() }
`)
	writeFile(t, root, "internal/beta/beta.go", `package beta
func BetaHelper() {}
`)
	commitAll(t, root, "seed map repo")

	var buf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Map: true}, &buf); err != nil {
		t.Fatalf("RepoMapSummary: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"god_nodes", "BetaHelper", "packages:", "alpha", "beta", "import_cycles:", "none"} {
		if !strings.Contains(got, want) {
			t.Errorf("map output missing %q in:\n%s", want, got)
		}
	}
}

func TestSearchMapNoFalseCycles(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	home := t.TempDir()

	writeFile(t, root, "go.mod", "module example.com\n\ngo 1.24\n")
	writeFile(t, root, "internal/alpha/alpha.go", `package alpha
import "example.com/internal/beta"
type closer interface{ Close() error }
type AlphaImpl struct{}
func (a *AlphaImpl) Close() error { return nil }
func UseAlpha(c closer, b *beta.BetaImpl) error {
	_ = beta.Helper()
	_ = b.Close()
	return c.Close()
}
`)
	writeFile(t, root, "internal/beta/beta.go", `package beta
type BetaImpl struct{}
func (b *BetaImpl) Close() error { return nil }
func Helper() error {
	var x BetaImpl
	return x.Close()
}
`)
	writeFile(t, root, "scripts/deploy.sh", "#!/bin/bash\nClose() { :; }\nHelper() { Close; }\n")
	commitAll(t, root, "seed map false-cycle repro")

	var buf bytes.Buffer
	if err := Search(Options{Dir: root, Home: home, Map: true}, &buf); err != nil {
		t.Fatalf("RepoMapSummary: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "internal/alpha (") || !strings.Contains(got, "-> internal/beta") {
		t.Errorf("expected internal/alpha -> internal/beta edge in map:\n%s", got)
	}
	if !strings.Contains(got, "import_cycles:\n  none (DAG verified)") {
		t.Errorf("expected DAG with zero false cycles, got:\n%s", got)
	}
}

func TestFindPackageCyclesMultiHop(t *testing.T) {
	t.Parallel()
	deps := map[string][]string{
		"a": {"b"},
		"b": {"a"},
		"x": {"y"},
		"y": {"z"},
		"z": {"w", "x"},
		"w": nil,
	}
	got := findPackageCycles(deps)
	want := "a -> b -> a\nx -> y -> z -> x"
	if strings.Join(got, "\n") != want {
		t.Errorf("findPackageCycles =\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
}

func benchGit(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		tb.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func benchWriteFile(tb testing.TB, root, rel, content string) {
	tb.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		tb.Fatal(err)
	}
}

func benchInitRepo(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	benchGit(tb, root, "init", "-q", "-b", "main")
	benchGit(tb, root, "config", "user.email", "bench@example.invalid")
	benchGit(tb, root, "config", "user.name", "bench")
	benchGit(tb, root, "config", "commit.gpgsign", "false")
	benchWriteFile(tb, root, ".gitignore", ".agents/scratch/*\n")
	benchWriteFile(tb, root, "README.md", "# demo\n\nA small demo project.\n")
	benchGit(tb, root, "add", "-A")
	benchGit(tb, root, "commit", "-q", "-m", "init")
	return root
}

func BenchmarkPersonalizedPageRank(b *testing.B) {
	root := benchInitRepo(b)
	home := b.TempDir()
	for i := 1; i <= 6; i++ {
		benchWriteFile(b, root, fmt.Sprintf("internal/util/u%d.go", i), "package util\nfunc CommonUtil() { logHelper() }\nfunc logHelper() {}\n")
	}
	benchWriteFile(b, root, "go.mod", "module example\n\ngo 1.24\n")
	benchWriteFile(b, root, "internal/core/reconcile.go", "package core\nimport \"example/internal/util\"\nfunc ReconcileIndex() error { util.CommonUtil(); return VerifyTreeState() }\nfunc VerifyTreeState() error { util.CommonUtil(); return nil }\n")
	benchWriteFile(b, root, "internal/core/session_active.go", "package core\nfunc ActiveSessionHandler() error { return ReconcileIndex() }\n")
	benchGit(b, root, "add", "-A")
	benchGit(b, root, "commit", "-q", "-m", "seed graph")

	sc, err := prepare(Options{Dir: root, Home: home}, io.Discard)
	if err != nil {
		b.Fatalf("prepare: %v", err)
	}
	defer sc.close()
	g, err := sc.loadSymGraphOpts(false)
	if err != nil {
		b.Fatalf("loadSymGraphOpts: %v", err)
	}
	active := map[string]bool{"internal/core/session_active.go": true}
	b.ResetTimer()
	for range b.N {
		if ranked := g.rank("ReconcileIndex", active); len(ranked) == 0 {
			b.Fatal("expected non-empty ranked symbols")
		}
	}
}
