package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
)

func mustJSONObject(t *testing.T, label, raw string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatalf("%s: invalid JSON (%v):\n%s", label, err, raw)
	}
	return obj
}

func assertNotJSON(t *testing.T, label, raw string) {
	t.Helper()
	var obj map[string]any
	if json.Unmarshal([]byte(raw), &obj) == nil {
		t.Fatalf("%s: expected non-JSON text output, got JSON:\n%s", label, raw)
	}
}

func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, &out)
	return out.String(), exitOf(err)
}

func TestV910All30VerbsJSON(t *testing.T) {
	t.Setenv(harness.ChildEnv, "1")
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("all_33_verbs_declare_json_and_help_fits", func(t *testing.T) {
		if len(allVerbs()) != 33 {
			t.Fatalf("len(verbs) = %d, want 33", len(allVerbs()))
		}
		for _, name := range verbKeys() {
			c := newCtx(name, allVerbs()[name], io.Discard)
			if c.fs.Lookup("json") == nil {
				t.Errorf("verb %q does not declare --json flag", name)
			}
			for i, line := range strings.Split(verbHelp(name), "\n") {
				if n := utf8.RuneCountInString(line); n > 79 {
					t.Errorf("%s help line %d has %d runes (>79): %q", name, i+1, n, line)
				}
			}
		}
		vocab := vocabulary()
		for _, sub := range autoSubverbs {
			if !vocab["auto"].Subverbs[sub]["json"] {
				t.Errorf("vocabulary auto %s missing --json", sub)
			}
		}
	})

	t.Run("version", func(t *testing.T) {
		home := t.TempDir()
		out, code := runCLI(t, "version", "--json", "--home", home)
		if code != 0 {
			t.Fatalf("version --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "version --json", out)
		if obj["version"] != buildVersion() {
			t.Errorf("version = %v, want %q", obj["version"], buildVersion())
		}
		txt, code := runCLI(t, "version", "--home", home)
		if code != 0 || !strings.HasPrefix(txt, "fyr ") {
			t.Fatalf("version text exit %d: %q", code, txt)
		}
		assertNotJSON(t, "version text", txt)
	})

	t.Run("list_and_render", func(t *testing.T) {
		home := t.TempDir()
		out, code := runCLI(t, "list", "--json", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("list --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "list --json", out)
		if obj["source"] == "" || obj["output"] == "" {
			t.Errorf("list --json missing fields: %v", obj)
		}
		txt, code := runCLI(t, "list", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("list text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "list text", txt)

		out, code = runCLI(t, "render", "--json", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("render --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "render --json", out)
		n, _ := obj["bytes"].(float64)
		if obj["source"] == "" || n <= 0 || obj["content"] == "" {
			t.Errorf("render --json missing fields: %v", obj)
		}
		txt, code = runCLI(t, "render", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("render text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "render text", txt)
	})

	t.Run("install_sync_diff_uninstall", func(t *testing.T) {
		home := t.TempDir()
		out, code := runCLI(t, "install", "--dry-run", "--json", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("install --dry-run --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "install --dry-run --json", out)
		if obj["verb"] != "install" || obj["ok"] != true || obj["dry_run"] != true || obj["home"] != home {
			t.Errorf("install --dry-run --json unexpected: %v", obj)
		}

		out, code = runCLI(t, "sync", "--dry-run", "--json", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("sync --dry-run --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "sync --dry-run --json", out)
		if obj["verb"] != "sync" || obj["ok"] != true || obj["dry_run"] != true {
			t.Errorf("sync --dry-run --json unexpected: %v", obj)
		}

		txt, code := runCLI(t, "install", "--dry-run", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("install text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "install text", txt)

		out, code = runCLI(t, "install", "--json", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("install --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "install --json", out)
		if obj["ok"] != true || obj["dry_run"] != false {
			t.Errorf("install --json unexpected: %v", obj)
		}

		out, code = runCLI(t, "diff", "--json", "--repo", repo, "--home", home)
		if code != 0 {
			t.Fatalf("diff --json clean exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "diff --json clean", out)
		if obj["ok"] != true || obj["drift"] != false {
			t.Errorf("diff --json clean unexpected: %v", obj)
		}

		emptyHome := t.TempDir()
		out, _ = runCLI(t, "diff", "--json", "--repo", repo, "--home", emptyHome)
		obj = mustJSONObject(t, "diff --json drift", out)
		if obj["drift"] != true || obj["output"] == "" {
			t.Errorf("diff --json drift unexpected: %v", obj)
		}

		out, code = runCLI(t, "uninstall", "--dry-run", "--json", "--home", home)
		if code != 0 {
			t.Fatalf("uninstall --dry-run --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "uninstall --dry-run --json", out)
		if obj["ok"] != true || obj["dry_run"] != true || obj["home"] != home {
			t.Errorf("uninstall --dry-run --json unexpected: %v", obj)
		}

		out, code = runCLI(t, "uninstall", "--json", "--home", home)
		if code != 0 {
			t.Fatalf("uninstall --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "uninstall --json", out)
		if obj["ok"] != true || obj["dry_run"] != false {
			t.Errorf("uninstall --json unexpected: %v", obj)
		}
	})

	t.Run("update_checkout", func(t *testing.T) {
		home := t.TempDir()
		fixture := makeUpdateGitFixture(t, repo)
		out, code := runCLI(t, "update", "--check", "--json", "--repo", fixture, "--home", home)
		if code != 0 {
			t.Fatalf("update --check --json clean exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "update --check --json clean", out)
		if obj["ok"] != true || obj["check"] != true || obj["behind"] != false {
			t.Errorf("update --check clean unexpected: %v", obj)
		}

		advanceOrigin(t, fixture)
		out, code = runCLI(t, "update", "--check", "--json", "--repo", fixture, "--home", home)
		if code != 2 {
			t.Fatalf("update --check --json behind exit %d, want 2:\n%s", code, out)
		}
		obj = mustJSONObject(t, "update --check --json behind", out)
		if obj["ok"] != false || obj["check"] != true || obj["behind"] != true {
			t.Errorf("update --check behind unexpected: %v", obj)
		}

		out, code = runCLI(t, "update", "--dry-run", "--json", "--repo", fixture, "--home", home)
		if code != 0 {
			t.Fatalf("update --dry-run --json behind exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "update --dry-run --json behind", out)
		if obj["ok"] != true || obj["dry_run"] != true || obj["behind"] != true {
			t.Errorf("update --dry-run behind unexpected: %v", obj)
		}

		txt, code := runCLI(t, "update", "--check", "--repo", fixture, "--home", home)
		if code != 2 {
			t.Fatalf("update --check text exit %d, want 2:\n%s", code, txt)
		}
		assertNotJSON(t, "update --check text", txt)

		zipPath := makeHarnessZipFixture(t, repo)
		zipHome := t.TempDir()
		out, code = runCLI(t, "update", "--from-zip", zipPath, "--dry-run", "--json", "--home", zipHome)
		if code != 0 {
			t.Fatalf("update --from-zip --dry-run --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "update --from-zip --dry-run --json", out)
		if obj["ok"] != true || obj["dry_run"] != true || obj["from_zip"] != zipPath {
			t.Errorf("update --from-zip unexpected: %v", obj)
		}
	})

	t.Run("adopt", func(t *testing.T) {
		home := t.TempDir()
		out, code := runCLI(t, "adopt", "--json", "--repo", repo, "--home", home)
		if code != 2 {
			t.Fatalf("adopt usage err exit %d, want 2:\n%s", code, out)
		}
		assertNotJSON(t, "adopt usage err", out)

		scratchRepo := makeAdoptRepoFixture(t, repo)
		skillSrc := filepath.Join(t.TempDir(), "sample-json-skill")
		if err := os.MkdirAll(skillSrc, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillSrc, "SKILL.md"), []byte("---\nname: sample-json-skill\ndescription: sample skill for test\n---\n# Sample\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, code = runCLI(t, "adopt", skillSrc, "--json", "--repo", scratchRepo, "--home", home)
		if code != 0 {
			t.Fatalf("adopt --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "adopt --json", out)
		if obj["ok"] != true || obj["force"] != false || obj["output"] == "" {
			t.Errorf("adopt --json unexpected: %v", obj)
		}
	})

	t.Run("check_gitcheck_resume_index", func(t *testing.T) {
		home := t.TempDir()
		cleanRoot := checkRepo(t, map[string]string{
			"AGENTS.md":     "# AGENTS\n",
			"README.md":     "run `fyr check --json`\n",
			"internal/a.go": "package a\n",
		})

		out, code := runCLI(t, "check", "--json", "--dir", cleanRoot, "--home", home)
		if code != 0 {
			t.Fatalf("check --json clean exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "check --json clean", out)
		if obj["ok"] != true || obj["version"] != buildVersion() {
			t.Errorf("check --json clean unexpected: %v", obj)
		}
		counts, _ := obj["counts"].(map[string]any)
		if counts == nil || counts["total"] != float64(0) {
			t.Errorf("check --json clean counts: %v", obj["counts"])
		}

		dirtyRoot := checkRepo(t, map[string]string{
			"README.md":     "`fyr frobnicate` `internal/gone.go`\n",
			"internal/a.go": "package a\n",
		})
		out, code = runCLI(t, "check", "--json", "--dir", dirtyRoot, "--home", home)
		if code != 1 {
			t.Fatalf("check --json findings exit %d, want 1:\n%s", code, out)
		}
		obj = mustJSONObject(t, "check --json findings", out)
		if obj["ok"] != false {
			t.Errorf("check --json findings ok = %v, want false", obj["ok"])
		}
		counts, _ = obj["counts"].(map[string]any)
		if counts == nil || counts["total"] != float64(2) || counts["docs"] != float64(2) {
			t.Errorf("check --json findings counts: %v", obj["counts"])
		}

		out, code = runCLI(t, "git-check", "--json", "--dir", cleanRoot, "--home", home)
		if code != 0 {
			t.Fatalf("git-check --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "git-check --json", out)
		if obj["ok"] != true || obj["dir"] == "" || obj["output"] == "" {
			t.Errorf("git-check --json unexpected: %v", obj)
		}

		out, code = runCLI(t, "resume", "--json", "--dir", cleanRoot, "--home", home)
		if code != 0 {
			t.Fatalf("resume --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "resume --json", out)
		if obj["ok"] != true || obj["dir"] == "" || obj["output"] == "" {
			t.Errorf("resume --json unexpected: %v", obj)
		}

		out, code = runCLI(t, "index", "--json", "--dir", cleanRoot, "--home", home)
		if code != 0 {
			t.Fatalf("index --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "index --json", out)
		if obj["ok"] != true || obj["register"] != false || obj["dir"] == "" {
			t.Errorf("index --json unexpected: %v", obj)
		}

		out, code = runCLI(t, "index", "--register", "--json", "--dir", cleanRoot, "--home", home)
		if code != 0 {
			t.Fatalf("index --register --json exit %d:\n%s", code, out)
		}
		obj = mustJSONObject(t, "index --register --json", out)
		if obj["ok"] != true || obj["register"] != true {
			t.Errorf("index --register --json unexpected: %v", obj)
		}
	})

	t.Run("dream_and_auto", func(t *testing.T) {
		home, _ := autoHome(t)
		out, code := runCLI(t, "dream", "--all", "--dry-run", "--json", "--home", home)
		if code != 0 {
			t.Fatalf("dream --all --dry-run --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "dream --json", out)
		if obj["ok"] != true || obj["all"] != true || obj["dry_run"] != true {
			t.Errorf("dream --json unexpected: %v", obj)
		}
		txt, code := runCLI(t, "dream", "--all", "--dry-run", "--home", home)
		if code != 0 {
			t.Fatalf("dream text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "dream text", txt)

		for _, tc := range []struct {
			sub  string
			args []string
		}{
			{"on", []string{"auto", "on", "--dry-run", "--json", "--home", home}},
			{"status", []string{"auto", "status", "--json", "--home", home}},
			{"run", []string{"auto", "run", "--dry-run", "--json", "--home", home}},
			{"log", []string{"auto", "log", "--json", "--home", home}},
			{"off", []string{"auto", "off", "--dry-run", "--json", "--home", home}},
		} {
			out, code := runCLI(t, tc.args...)
			if code != 0 {
				t.Fatalf("auto %s --json exit %d:\n%s", tc.sub, code, out)
			}
			obj := mustJSONObject(t, "auto "+tc.sub+" --json", out)
			if obj["ok"] != true || obj["subcommand"] != tc.sub {
				t.Errorf("auto %s --json unexpected: %v", tc.sub, obj)
			}
		}
		txt, code = runCLI(t, "auto", "status", "--home", home)
		if code != 0 {
			t.Fatalf("auto status text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "auto status text", txt)

		// Error exits with --json must still emit valid JSON with ok=false, verb, error, output.
		corruptHome := t.TempDir()
		cfgPath := filepath.Join(corruptHome, harness.StateDir, "auto.json")
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfgPath, []byte("{corrupt"), 0o600); err != nil {
			t.Fatal(err)
		}
		errOut, errCode := runCLI(t, "auto", "run", "--dry-run", "--json", "--home", corruptHome)
		if errCode != 1 {
			t.Fatalf("auto corrupt --json exit=%d, want 1:\n%s", errCode, errOut)
		}
		errObj := mustJSONObject(t, "auto error --json", errOut)
		if errObj["ok"] != false || errObj["verb"] != "auto" || errObj["output"] == "" {
			t.Errorf("auto error --json unexpected: %v", errObj)
		}

		noAgyHome := t.TempDir()
		t.Setenv("PATH", t.TempDir())
		dErrOut, dErrCode := runCLI(t, "dream", "--project", filepath.Join(noAgyHome, "missing-proj"), "--json", "--home", noAgyHome)
		if dErrCode == 0 {
			t.Fatalf("dream missing project --json unexpectedly succeeded:\n%s", dErrOut)
		}
		dErrObj := mustJSONObject(t, "dream error --json", dErrOut)
		if dErrObj["ok"] != false || dErrObj["verb"] != "dream" || dErrObj["error"] == "" {
			t.Errorf("dream error --json unexpected: %v", dErrObj)
		}
	})

	t.Run("eval_list_and_trial_json", func(t *testing.T) {
		home := t.TempDir()
		out, code := runCLI(t, "eval", "--list", "--json", "--dir", repo, "--home", home)
		if code != 0 {
			t.Fatalf("eval --list --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "eval --list --json", out)
		rows, _ := obj["rows"].([]any)
		if obj["ok"] != true || obj["suite"] == "" || len(rows) == 0 {
			t.Errorf("eval --list --json unexpected: %v", obj)
		}
		txt, code := runCLI(t, "eval", "--list", "--dir", repo, "--home", home)
		if code != 0 {
			t.Fatalf("eval --list text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "eval --list text", txt)

		testEvalTrialJSON(t, repo)
	})

	t.Run("ast_vet_hook", func(t *testing.T) {
		home := t.TempDir()
		proj := checkRepo(t, map[string]string{
			".gitignore": ".agents/scratch/*\n",
			"svc.go":     "package main\n\nfunc CheckFlags() error { return nil }\n",
		})

		out, code := runCLI(t, "ast", "--map", "--json", "--dir", proj, "--home", home)
		if code != 0 {
			t.Fatalf("ast --map --json exit %d:\n%s", code, out)
		}
		_ = mustJSONObject(t, "ast --map --json", out)
		if !strings.Contains(out, "god_nodes") {
			t.Errorf("ast --map --json missing god_nodes:\n%s", out)
		}
		txt, code := runCLI(t, "ast", "--map", "--dir", proj, "--home", home)
		if code != 0 || !strings.Contains(txt, "god_nodes") {
			t.Fatalf("ast --map text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "ast --map text", txt)

		out, code = runCLI(t, "vet", "--json", "--dir", proj, "--home", home, "check flags")
		if code != 0 {
			t.Fatalf("vet --json exit %d:\n%s", code, out)
		}
		_ = mustJSONObject(t, "vet --json", out)
		if !strings.Contains(out, "[FYR_TURN_CONTRACT]") {
			t.Errorf("vet --json missing turn contract:\n%s", out)
		}
		txt, code = runCLI(t, "vet", "--dir", proj, "--home", home, "check flags")
		if code != 0 || !strings.Contains(txt, "[FYR_TURN_CONTRACT]") {
			t.Fatalf("vet text exit %d:\n%s", code, txt)
		}
		assertNotJSON(t, "vet text", txt)

		origStdin := stdin
		t.Cleanup(func() { stdin = origStdin })
		stdin = strings.NewReader("")
		out, code = runCLI(t, "hook", "session-start", "--json", "--dir", proj, "--home", home)
		if code != 0 {
			t.Fatalf("hook session-start --json exit %d:\n%s", code, out)
		}
		obj := mustJSONObject(t, "hook session-start --json", out)
		if obj["injectSteps"] == nil && obj["ok"] != true {
			t.Errorf("hook session-start --json unexpected: %v", obj)
		}
		stdin = strings.NewReader("")
		txt, code = runCLI(t, "hook", "session-start", "--dir", proj, "--home", home)
		if code != 0 || !strings.Contains(txt, "SESSION_START") {
			t.Fatalf("hook session-start text exit %d:\n%s", code, txt)
		}
	})
}

func testEvalTrialJSON(t *testing.T, repo string) {
	t.Helper()
	rig := newEvalRig(t, evalSuite, true)
	origRun := evalRun
	defer func() { evalRun = origRun }()
	evalRun = func(_ context.Context, o eval.Options, rows []eval.Row) ([]eval.Result, error) {
		return []eval.Result{{
			Row: rows[0].ID, Trial: 1, Run: "r1", Status: eval.StatusPass, Started: time.Now().UTC(), Seconds: 1,
			Dir: filepath.Join(o.Home, "sb1"),
		}}, nil
	}
	out, code := runCLI(t, "eval", "--json", "--repo", repo, "--dir", rig.project, "--home", rig.home, "ok")
	if code != 0 {
		t.Fatalf("eval trial --json exit %d:\n%s", code, out)
	}
	obj := mustJSONObject(t, "eval trial --json", out)
	res, _ := obj["results"].([]any)
	if obj["ok"] != true || len(res) != 1 {
		t.Errorf("eval trial --json unexpected: %v", obj)
	}

	evalRun = func(_ context.Context, o eval.Options, rows []eval.Row) ([]eval.Result, error) {
		return []eval.Result{{
			Row: rows[0].ID, Trial: 1, Run: "r2", Status: eval.StatusFail, Cause: "check failed", Started: time.Now().UTC(), Seconds: 1,
			Dir: filepath.Join(o.Home, "sb2"),
		}}, nil
	}
	out, code = runCLI(t, "eval", "--json", "--repo", repo, "--dir", rig.project, "--home", rig.home, "ok")
	if code != 1 {
		t.Fatalf("eval trial fail --json exit %d, want 1:\n%s", code, out)
	}
	obj = mustJSONObject(t, "eval trial fail --json", out)
	if obj["ok"] != false {
		t.Errorf("eval trial fail --json ok = %v, want false", obj["ok"])
	}
}

func makeUpdateGitFixture(t *testing.T, realRepo string) string {
	t.Helper()
	fixture, _ := gitFixture(t)
	for _, d := range []string{"context", "rules", "skills", "agents", "commands"} {
		if err := os.MkdirAll(filepath.Join(fixture, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(realRepo, "context", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "context", "AGENTS.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "context/AGENTS.md"}, {"commit", "-q", "-m", "harness"}, {"push", "-q", "origin", "main"}} {
		cmd := exec.Command("git", append([]string{"-C", fixture}, args...)...)
		cmd.Env = append(gitCleanEnv(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return fixture
}

func makeHarnessZipFixture(t *testing.T, realRepo string) string {
	t.Helper()
	zipPath := filepath.Join(t.TempDir(), "fyr-test.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, dir := range harness.ManagedDirs {
		root := filepath.Join(realRepo, dir)
		err := filepath.Walk(root, func(p string, info os.FileInfo, walkErr error) error {
			if walkErr != nil || info.IsDir() {
				return walkErr
			}
			rel, err := filepath.Rel(realRepo, p)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			w, err := zw.Create("fyr-test/" + filepath.ToSlash(rel))
			if err != nil {
				return err
			}
			_, err = w.Write(b)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}

func makeAdoptRepoFixture(t *testing.T, realRepo string) string {
	t.Helper()
	dst := t.TempDir()
	for _, d := range []string{"context", "rules", "skills", "agents", "commands"} {
		if err := os.MkdirAll(filepath.Join(dst, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(realRepo, "context", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "context", "AGENTS.md"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}
