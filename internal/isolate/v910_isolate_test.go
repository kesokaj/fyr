package isolate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/models"
)

func TestV910IsolateStageModelsCatalog(t *testing.T) {
	base := t.TempDir()
	realHome := filepath.Join(base, "real")
	scratchHome := filepath.Join(base, "scratch")
	for _, d := range []string{realHome, scratchHome} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	// Absent models.json is a clean no-op.
	if err := StageModelsCatalog(realHome, scratchHome); err != nil {
		t.Fatalf("StageModelsCatalog missing source: %v", err)
	}
	dst := filepath.Join(scratchHome, harness.StateDir, "models.json")
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("expected no staged file when source missing, err=%v", err)
	}

	// Regular models.json stages with mode 0400 and identical contents.
	cat := models.Catalog{
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
		QuotaUpdatedAt: time.Now().UTC().Format(time.RFC3339),
		Entries: []models.CatalogEntry{
			{ID: "gemini-4-carbon-high", DisplayName: "Gemini 4 Carbon (High)", Family: "gemini", Tier: "pro", Status: "active"},
		},
		Pools: []models.Pool{{Name: "Gemini Models", Five: 80, Week: 80}},
	}
	raw, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(realHome, harness.StateDir, "models.json")
	put(t, src, string(raw), 0o600)

	if err := StageModelsCatalog(realHome, scratchHome); err != nil {
		t.Fatalf("StageModelsCatalog regular: %v", err)
	}
	st, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("stat staged models.json: %v", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0o400 {
		t.Fatalf("staged models.json mode = %v, want regular 0400", st.Mode())
	}
	gotBytes, err := os.ReadFile(dst)
	if err != nil || string(gotBytes) != string(raw) {
		t.Fatalf("staged content mismatch: err=%v got=%q", err, string(gotBytes))
	}

	// Symlink models.json in realHome must be refused.
	symReal := filepath.Join(base, "sym-real")
	symScratch := filepath.Join(base, "sym-scratch")
	symState := filepath.Join(symReal, harness.StateDir)
	for _, d := range []string{symState, symScratch} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(src, filepath.Join(symState, "models.json")); err != nil {
		t.Fatal(err)
	}
	if err := StageModelsCatalog(symReal, symScratch); err == nil {
		t.Fatal("StageModelsCatalog accepted symlinked models.json")
	}
	if _, err := os.Lstat(filepath.Join(symScratch, harness.StateDir, "models.json")); !os.IsNotExist(err) {
		t.Fatalf("symlink refusal unexpectedly created destination, err=%v", err)
	}
}

func TestV910IsolateSeatbeltAgyBundleAndConfinement(t *testing.T) {
	// Case 1: macOS .app bundle under user Applications.
	realHome := "/Users/op"
	appBin := "/Users/op/Applications/Antigravity.app/Contents/MacOS/agy"
	rw, ro := policyPaths(realHome, appBin, []string{"/tmp/sb/app", "/tmp/sb/home", "/tmp/sb/tmp"}, nil, nil, nil)
	prof, err := renderSeatbeltProfile(Policy{
		Argv:    []string{appBin, "-p", "hi"},
		Dir:     "/tmp/sb/app",
		RWPaths: rw,
		ROPaths: ro,
		Network: true,
	})
	if err != nil {
		t.Fatalf("renderSeatbeltProfile app bundle: %v", err)
	}
	if !strings.Contains(prof, `(subpath "/Users/op/Applications/Antigravity.app")`) {
		t.Fatalf("seatbelt profile missing scoped .app bundle RO rule:\n%s", prof)
	}
	for _, forbid := range []string{
		`(subpath "/Users/op")`,
		`(literal "/Users/op")`,
		`(subpath "/Users/op/Applications")`,
		`(subpath "/Users/op/` + harness.StateDir + `")`,
		`(subpath "/Users/op/.config")`,
	} {
		if strings.Contains(prof, forbid) {
			t.Errorf("seatbelt profile leaked broad path %s:\n%s", forbid, prof)
		}
	}

	// Case 2: standalone binary under realHome/.local/bin/agy must expose only the binary, never .local/bin or realHome.
	base := t.TempDir()
	rHome := filepath.Join(base, "real")
	standalone := filepath.Join(rHome, ".local", "bin", "agy")
	put(t, standalone, "#!/bin/sh\nexit 0\n", 0o755)
	rw2, ro2 := policyPaths(rHome, standalone, []string{filepath.Join(base, "sb")}, nil, nil, nil)
	prof2, err := renderSeatbeltProfile(Policy{Argv: []string{standalone}, Dir: base, RWPaths: rw2, ROPaths: ro2, Network: true})
	if err != nil {
		t.Fatalf("renderSeatbeltProfile standalone: %v", err)
	}
	if !strings.Contains(prof2, `(literal "`+standalone+`")`) {
		t.Fatalf("seatbelt profile missing standalone agy literal rule:\n%s", prof2)
	}
	for _, forbid := range []string{
		`(subpath "` + rHome + `")`,
		`(subpath "` + filepath.Join(rHome, ".local") + `")`,
		`(subpath "` + filepath.Join(rHome, ".local", "bin") + `")`,
		`(subpath "` + filepath.Join(rHome, harness.StateDir) + `")`,
	} {
		if strings.Contains(prof2, forbid) {
			t.Errorf("standalone agy seatbelt profile leaked %s:\n%s", forbid, prof2)
		}
	}
}

func TestV910IsolateLandlockModelsAndAgyBundle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux Landlock test")
	}
	if err := CheckAvailable(); err != nil {
		t.Fatalf("Landlock unavailable: %v", err)
	}
	// Capture host Go caches before newRig(t) overrides HOME so neither go build
	// nor `fyr install --repo` creates read-only modcache files under r.real.
	goEnv := func(key string) string {
		out, envErr := exec.Command("go", "env", key).Output()
		if envErr != nil {
			t.Fatalf("go env %s: %v", key, envErr)
		}
		return strings.TrimSpace(string(out))
	}
	goModCache, goBuildCache := goEnv("GOMODCACHE"), goEnv("GOCACHE")

	// Build a real fyr binary so `fyr models --pick` runs inside Landlock.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	builtFyr := filepath.Join(t.TempDir(), "real-fyr")
	buildCmd := exec.Command("go", "build", "-o", builtFyr, "./cmd/fyr")
	buildCmd.Dir = repoRoot
	buildCmd.Env = append(FyrEnv(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOMODCACHE="+goModCache, "GOCACHE="+goBuildCache)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build fyr binary: %v\n%s", err, out)
	}

	r := newRig(t)
	t.Setenv("GOMODCACHE", goModCache)
	t.Setenv("GOCACHE", goBuildCache)
	outside := filepath.Join(r.tmp, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	// Stage a real models.json and tripwire files in r.real.
	now := time.Now().UTC().Format(time.RFC3339)
	cat := models.Catalog{
		UpdatedAt:      now,
		QuotaUpdatedAt: now,
		Entries: []models.CatalogEntry{
			{ID: "gemini-4-carbon-high", DisplayName: "Gemini 4 Carbon (High)", Family: "gemini", Tier: "pro", Status: "active"},
			{ID: "claude-opus-5-5-high", DisplayName: "Claude Opus 5.5 (High)", Family: "anthropic", Tier: "pro", Status: "active"},
		},
		Pools: []models.Pool{
			{Name: "Gemini Models", Five: 80, Week: 80},
			{Name: "Claude and GPT models", Five: 80, Week: 80},
		},
	}
	catBytes, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	realModels := filepath.Join(r.real, harness.StateDir, "models.json")
	realTripwire := filepath.Join(r.real, "private-note.txt")
	realBrain := filepath.Join(r.real, harness.StateDir, "brain.db")
	put(t, realModels, string(catBytes), 0o600)
	put(t, realTripwire, "do-not-read\n", 0o600)
	put(t, realBrain, "real-brain-data\n", 0o600)

	// Install fake agy inside a realHome app-bundle dir, symlinked from realHome/.local/bin/agy.
	bundleRoot := filepath.Join(r.real, ".local", "share", "antigravity")
	bundleRes := filepath.Join(bundleRoot, "resources", "runtime.txt")
	bundleBin := filepath.Join(bundleRoot, "bin", "agy")
	symlinkBin := filepath.Join(r.real, ".local", "bin", "agy")
	put(t, bundleRes, "bundle-runtime-ok\n", 0o644)
	probeScript := `#!/bin/bash
set +e
cat ` + ShellQuote(bundleRes) + ` > "$HOME/bundle_read.txt" 2> "$HOME/bundle_read.err"
echo "bundle_rc=$?" >> "$HOME/probe.status"
fyr models --pick builder > "$HOME/pick.txt" 2> "$HOME/pick.err"
echo "pick_rc=$?" >> "$HOME/probe.status"
(printf 'tamper' >> "$HOME/` + harness.StateDir + `/models.json") 2> "$HOME/ro_models.err"
echo "ro_models_rc=$?" >> "$HOME/probe.status"
(cat ` + ShellQuote(realTripwire) + `) >/dev/null 2> "$HOME/real_read.err"
echo "real_read_rc=$?" >> "$HOME/probe.status"
(cat ` + ShellQuote(realBrain) + `) >/dev/null 2> "$HOME/brain_read.err"
echo "brain_read_rc=$?" >> "$HOME/probe.status"
(cat ` + ShellQuote(realModels) + `) >/dev/null 2> "$HOME/real_models_read.err"
echo "real_models_read_rc=$?" >> "$HOME/probe.status"
(printf 'escape' > ` + ShellQuote(filepath.Join(outside, "escape.txt")) + `) 2> "$HOME/outside_write.err"
echo "outside_write_rc=$?" >> "$HOME/probe.status"
exit 0
`
	put(t, bundleBin, probeScript, 0o755)
	if err := os.MkdirAll(filepath.Dir(symlinkBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bundleBin, symlinkBin); err != nil {
		t.Fatal(err)
	}

	c := r.config("v910-models-bundle")
	c.Self = builtFyr
	c.Repo = repoRoot
	c.TestRO = nil
	c.TestRW = nil
	b := r.prepared(c)

	stagedModels := filepath.Join(b.Home(), harness.StateDir, "models.json")
	st, err := os.Lstat(stagedModels)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o400 {
		t.Fatalf("staged models.json in Box: err=%v mode=%v, want regular 0400", err, st)
	}

	tok := filepath.Join(r.real, TokenRel)
	if rc := b.Session(context.Background(), symlinkBin, tok, "probe", "out.txt"); rc != 0 {
		errTxt, _ := os.ReadFile(filepath.Join(b.Dir(), "err.txt"))
		t.Fatalf("confined session rc=%d\nnotes:\n%s\nerr.txt:\n%s", rc, r.out.String(), string(errTxt))
	}

	if got, err := os.ReadFile(filepath.Join(b.Home(), "bundle_read.txt")); err != nil || strings.TrimSpace(string(got)) != "bundle-runtime-ok" {
		bErr, _ := os.ReadFile(filepath.Join(b.Home(), "bundle_read.err"))
		t.Fatalf("bundle resource read failed: got=%q err=%v stderr=%q", string(got), err, string(bErr))
	}
	if got, err := os.ReadFile(filepath.Join(b.Home(), "pick.txt")); err != nil || strings.TrimSpace(string(got)) != "gemini-4-carbon-high" {
		pErr, _ := os.ReadFile(filepath.Join(b.Home(), "pick.err"))
		t.Fatalf("fyr models --pick inside Landlock failed: got=%q err=%v stderr=%q", string(got), err, string(pErr))
	}
	for _, errFile := range []string{"ro_models.err", "real_read.err", "brain_read.err", "real_models_read.err", "outside_write.err"} {
		msg, _ := os.ReadFile(filepath.Join(b.Home(), errFile))
		if !strings.Contains(string(msg), "Permission denied") {
			t.Errorf("%s = %q, want Permission denied (EACCES)", errFile, strings.TrimSpace(string(msg)))
		}
	}
	if !b.After() {
		t.Errorf("After() = false, reasons=%v\n%s", b.Reasons(), r.out.String())
	}
	b.ExitChecks()
	if !b.Intact() {
		t.Errorf("box not intact: %v", b.Reasons())
	}
}

func TestV910IsolateJudgeFailFastErrNoAuthToken(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "scratch-home")
	realHome := filepath.Join(base, "real-home")
	repoDir := filepath.Join(base, "repo")
	binDir := filepath.Join(base, "bin")
	for _, d := range []string{home, realHome, repoDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(base, "agy-ran.marker")
	slowAgy := filepath.Join(binDir, "agy")
	put(t, slowAgy, "#!/bin/sh\necho ran > "+ShellQuote(marker)+"\nsleep 60\n", 0o755)

	// 1. Missing token fails fast (< 1s) with ErrNoAuthToken without spawning agy.
	start := time.Now()
	_, err := RunJudgePrint(context.Background(), JudgeExecOptions{
		Home: home, RealHome: realHome, RepoDir: repoDir, AgyPath: slowAgy, Prompt: "judge", Timeout: 5 * time.Second,
	})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrNoAuthToken) {
		t.Fatalf("missing auth err = %v, want ErrNoAuthToken", err)
	}
	if elapsed >= time.Second {
		t.Fatalf("missing auth took %s, want < 1s", elapsed)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("agy was spawned despite missing auth token")
	}

	// 2. Symlink WorkTokenRel is refused with ErrNoAuthToken without spawning agy.
	dummyFile := filepath.Join(base, "dummy-auth")
	put(t, dummyFile, "auth-data", 0o600)
	workTokPath := filepath.Join(home, filepath.FromSlash(WorkTokenRel))
	if err := os.MkdirAll(filepath.Dir(workTokPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dummyFile, workTokPath); err != nil {
		t.Fatal(err)
	}
	_, err = RunJudgePrint(context.Background(), JudgeExecOptions{
		Home: home, RealHome: realHome, RepoDir: repoDir, AgyPath: slowAgy, Prompt: "judge", Timeout: 5 * time.Second,
	})
	if !errors.Is(err, ErrNoAuthToken) {
		t.Fatalf("symlink WorkTokenRel err = %v, want ErrNoAuthToken", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("agy was spawned despite symlink WorkTokenRel")
	}

	// 3. Regular WorkTokenRel under home succeeds.
	if err := os.Remove(workTokPath); err != nil {
		t.Fatal(err)
	}
	put(t, workTokPath, "work-auth-data", 0o600)
	okAgy := filepath.Join(binDir, "ok-agy")
	put(t, okAgy, "#!/bin/sh\nprintf '{\"verdict\":\"PASS\",\"reason\":\"ok\"}\\n'\n", 0o755)
	out, err := RunJudgePrint(context.Background(), JudgeExecOptions{
		Home: home, RealHome: realHome, RepoDir: repoDir, AgyPath: okAgy, Prompt: "judge", Unconfined: true,
	})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("regular WorkTokenRel failed: err=%v out=%+v", err, out)
	}
}
