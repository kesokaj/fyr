package dream

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func TestV910Ephemeral(t *testing.T) {
	fakeHome := filepath.Join(t.TempDir(), "user-home")
	t.Setenv("HOME", fakeHome)

	blocked := []string{
		"/",
		"/tmp",
		fakeHome,
		fakeHome + "/",
		"/home/u/.config/fyr/auto/wt-1",
		"/home/u/.config/fyr/eval/r1",
		"/home/u/.config/fyr/sandbox/s1",
		"/home/u/.config/fyr/dev",
		"/home/u/.config/fyr/dev/fyr-eval",
		"/home/u/.config/fyr/dream-agy-home-1234",
		"/tmp/real-home-smoke",
		"/tmp/real-home-smoke-20261122",
		"/home/u/.gemini/antigravity-cli/brain/conv-1/scratch",
		"/home/u/.gemini/antigravity-cli/brain/conv-1/scratch/file",
	}
	for _, p := range blocked {
		if !IsEphemeralPath(p) {
			t.Errorf("IsEphemeralPath(%q) = false, want true", p)
		}
		if !IsRefusedWorkspace(p, fakeHome) {
			t.Errorf("IsRefusedWorkspace(%q, %q) = false, want true", p, fakeHome)
		}
	}

	allowed := []string{
		"",
		".",
		search.GlobalWorkspace,
		filepath.Join(fakeHome, "workspace", "fyr"),
		"/tmp/TestDreamFixture123/repo",
	}
	for _, p := range allowed {
		if IsEphemeralPath(p) {
			t.Errorf("IsEphemeralPath(%q) = true, want false", p)
		}
		if IsRefusedWorkspace(p, fakeHome) {
			t.Errorf("IsRefusedWorkspace(%q, %q) = true, want false", p, fakeHome)
		}
	}
}

func TestDefaultProjectsFiltersRefusedRoots(t *testing.T) {
	home := t.TempDir()
	realRepo := t.TempDir()
	sumDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(sumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(sumDir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE conversation_summaries (`conversation_id` text, `last_modified_time` datetime NOT NULL, `workspace_uris` text NOT NULL)")
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	badRoots := []string{
		"/",
		"/tmp",
		home,
		filepath.Join(home, harness.StateDir, "dev", "fyr-eval"),
		filepath.Join(home, harness.StateDir, "dream-agy-home-999"),
		filepath.Join(home, harness.StateDir, "auto", "wt-1"),
		"/tmp/real-home-smoke-20261122",
		filepath.Join(home, ".gemini", "antigravity-cli", "brain", "abc", "scratch"),
	}
	for i, root := range append(badRoots, realRepo) {
		_, err = db.Exec(`INSERT INTO conversation_summaries VALUES (?, '2026-11-22 10:00:00+00:00', ?)`,
			"c"+string(rune('0'+i)), `["file://`+root+`"]`)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()

	got, err := defaultProjects(home)
	if err != nil {
		t.Fatalf("defaultProjects: %v", err)
	}
	if len(got) != 1 || got[0].Root != realRepo {
		t.Fatalf("defaultProjects = %+v, want only %q", got, realRepo)
	}
}

func TestV910DreamFit328KBWithSIM(t *testing.T) {
	// 328 KB transcript + 6 KB kindSIM + memory + skill + metrics under 256 KB maxPrompt.
	huge := "HEAD_USER_GOAL: verify 328KB transcript chunking with kindSIM\n" +
		strings.Repeat("intermediate step log payload 0123456789abcdef\n", 3700) +
		"DECISION_MARKER: keep tail and session ratings intact\n" +
		strings.Repeat("trailing tool output payload abcdef0123456789\n", 3700) +
		"TAIL_OUTCOME_MARKER: session_ratings preserved cleanly"
	if len(huge) < 328*1024 {
		t.Fatalf("fixture transcript size = %d, want >= 328 KiB", len(huge))
	}
	simBlock := "SIM_HEADER: prior attempts\n" + strings.Repeat("SIM-P001 attempt summary line\n", 200)
	ts := day(2)
	c := Collected{Name: "fyr", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: strings.Repeat("m", 4096)},
		{Kind: kindSkill, Label: "skill fyr-feature", Text: strings.Repeat("s", 2048)},
		{Kind: kindSIM, Label: "optimizer-memory", Text: simBlock},
		{Kind: kindMetrics, Label: "metrics", Text: strings.Repeat("9", 1024)},
		{Kind: kindTranscript, Label: "antigravity conv-328k", Text: huge, Time: ts},
	}}
	if err := c.fit(maxPrompt); err != nil {
		t.Fatalf("fit(maxPrompt): %v", err)
	}
	if rendered := prompt(c); len(rendered) > maxPrompt {
		t.Fatalf("rendered prompt %d > maxPrompt %d", len(rendered), maxPrompt)
	}
	var gotTranscript *Input
	for i := range c.Inputs {
		if c.Inputs[i].Kind == kindTranscript {
			gotTranscript = &c.Inputs[i]
			break
		}
	}
	if gotTranscript == nil {
		t.Fatalf("328 KB transcript was dropped instead of chunked; Dropped=%v", c.Dropped)
	}
	for _, marker := range []string{"HEAD_USER_GOAL", "DECISION_MARKER", "TAIL_OUTCOME_MARKER"} {
		if !strings.Contains(gotTranscript.Text, marker) {
			t.Errorf("chunked transcript missing %q", marker)
		}
	}

	// Tight room with oversized kindSIM triggers bounded trimSIMBlock fallback so transcript still chunks cleanly.
	cTight := Collected{Name: "fyr", Inputs: []Input{
		{Kind: kindMemory, Label: "memory/a.md", Text: "mem"},
		{Kind: kindSIM, Label: "optimizer-memory", Text: simBlock},
		{Kind: kindTranscript, Label: "antigravity conv-tight", Text: huge, Time: ts},
	}}
	tightLimit := len(head(cTight)) + len(block(cTight.Inputs[0])) + len(block(cTight.Inputs[1])) + 200
	if err := cTight.fit(tightLimit); err != nil {
		t.Fatalf("fit(tightLimit): %v", err)
	}
	if rendered := prompt(cTight); len(rendered) > tightLimit {
		t.Fatalf("tight rendered prompt %d > tightLimit %d", len(rendered), tightLimit)
	}
	hasTranscript := false
	for _, in := range cTight.Inputs {
		if in.Kind == kindTranscript && strings.Contains(in.Text, "TAIL_OUTCOME_MARKER") {
			hasTranscript = true
		}
	}
	if !hasTranscript {
		t.Fatalf("trimSIMBlock fallback failed to preserve chunked transcript tail; inputs=%s dropped=%v", labels(cTight), cTight.Dropped)
	}
}

func TestV910DreamAgyRemoteControlServe(t *testing.T) {
	const me = 1000
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"remote-control serve", []string{"/home/u/.local/bin/agy", "remote-control", "serve"}, true},
		{"remote-control serve with flags", []string{"agy", "remote-control", "serve", "--host", "127.0.0.1"}, true},
		{"remote-control start", []string{"agy", "remote-control", "start"}, false},
		{"remote-control bare", []string{"agy", "remote-control"}, false},
		{"remote-control serve headless -p", []string{"agy", "remote-control", "serve", "-p", "hi"}, false},
	}
	for _, tc := range cases {
		t.Run("linux/"+tc.name, func(t *testing.T) {
			fakeProc(t, proc{uid: me, args: tc.args})
			got, err := agySession("linux", me)
			if err != nil || got != tc.want {
				t.Errorf("agySession(linux, %v) = %v, %v; want %v", tc.args, got, err, tc.want)
			}
		})
	}
	oldPS := psArgs
	t.Cleanup(func() { psArgs = oldPS })
	psArgs = func(int) ([]byte, error) {
		return []byte("/Users/u/.local/bin/agy remote-control serve --port 8080\n"), nil
	}
	if got, err := agySession("darwin", 501); err != nil || !got {
		t.Errorf("agySession(darwin, remote-control serve) = %v, %v; want true", got, err)
	}
}

func TestV910ResolveBinaryLocalBin(t *testing.T) {
	origPATH := os.Getenv("PATH")
	home := t.TempDir()
	envHome := t.TempDir()
	siblingDir := t.TempDir()
	t.Setenv("HOME", envHome)
	t.Setenv("PATH", t.TempDir())

	origExe := dreamExecutable
	t.Cleanup(func() { dreamExecutable = origExe })
	dreamExecutable = func() (string, error) { return filepath.Join(siblingDir, "fyr"), nil }

	// Nothing installed yet: must return false (never leaks real operator $HOME).
	if p, ok := ResolveBinary("agy", home); ok {
		t.Fatalf("ResolveBinary with bare PATH/HOME leaked %q", p)
	}
	// Non-executable file in home/.local/bin must be ignored.
	homeBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(homeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	nonExec := filepath.Join(homeBin, "agy")
	if err := os.WriteFile(nonExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, ok := ResolveBinary("agy", home); ok {
		t.Fatalf("ResolveBinary accepted non-executable %q", p)
	}
	// Executable in $HOME/.local/bin resolves when home/.local/bin is not executable.
	envBin := filepath.Join(envHome, ".local", "bin")
	if err := os.MkdirAll(envBin, 0o755); err != nil {
		t.Fatal(err)
	}
	envAgy := filepath.Join(envBin, "agy")
	if err := os.WriteFile(envAgy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := ResolveBinary("agy", home); !ok || p != envAgy {
		t.Fatalf("ResolveBinary($HOME/.local/bin) = (%q, %v), want (%q, true)", p, ok, envAgy)
	}
	// Executable in home/.local/bin takes precedence over $HOME/.local/bin.
	if err := os.Chmod(nonExec, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := ResolveBinary("agy", home); !ok || p != nonExec {
		t.Fatalf("ResolveBinary(home/.local/bin) = (%q, %v), want (%q, true)", p, ok, nonExec)
	}
	// Executable sibling dir takes precedence over home/.local/bin.
	sibAgy := filepath.Join(siblingDir, "agy")
	if err := os.WriteFile(sibAgy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if p, ok := ResolveBinary("agy", home); !ok || p != sibAgy {
		t.Fatalf("ResolveBinary(sibling) = (%q, %v), want (%q, true)", p, ok, sibAgy)
	}
	// Bare-PATH dream --dry-run locates home/.local/bin/agy cleanly.
	t.Setenv("PATH", origPATH)
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dreamExecutable = origExe
	fBin := filepath.Join(f.home, ".local", "bin")
	if err := os.MkdirAll(fBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fBin, "agy"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	o := Options{Home: f.home, All: true, DryRun: true, Out: &out, Now: func() time.Time { return now }, Active: idle, Scopes: noScopes,
		Projects: func(string) ([]search.Project, error) { return []search.Project{project(f)}, nil }}
	if err := Run(o); err != nil {
		t.Fatalf("bare-PATH dry-run with home/.local/bin/agy: %v\n%s", err, out.String())
	}
}
