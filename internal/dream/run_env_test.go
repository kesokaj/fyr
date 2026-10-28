package dream

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/trail"
)

// TestAgyEnvAllowList: agy gets only agyEnv's names and the private HOME
// (ADR-067, SD25 of the 2026-10-27 audit). A credential set in fyr's own
// environment, checked as set first, does not arrive; a listed locale name
// does, an unlisted LC_ name does not. The shell running the fake adds PWD,
// SHLVL and _ itself.
func TestAgyEnvAllowList(t *testing.T) {
	f := newFixture(t)
	scaffold(t, f)
	agyLogin(t)
	rec := fakeCLI(t, "agy", agyResult(`{"proposals":[],"skills":[]}`, false), 0)
	t.Setenv("GEMINI_API_KEY", "canary-gemini")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "canary-aws")
	t.Setenv("LC_TIME", "sv_SE.UTF-8")
	t.Setenv("LC_SMUGGLED", "canary-lc")
	if os.Getenv("GEMINI_API_KEY") != "canary-gemini" {
		t.Fatal("the parent's GEMINI_API_KEY is not set")
	}
	var out bytes.Buffer
	if err := Run(runOpts(f, &out)); err != nil {
		t.Fatal(err)
	}
	env := read(t, filepath.Join(rec, "env"))
	got := map[string]string{}
	for _, kv := range strings.Split(strings.TrimSpace(env), "\n") {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	for k := range got {
		if !inAgyEnv(k) && k != "HOME" && k != "PWD" && k != "SHLVL" && k != "_" {
			t.Errorf("agy got %s, which agyEnv does not list", k)
		}
	}
	if strings.Contains(env, "canary-") {
		t.Errorf("a credential reached agy:\n%s", env)
	}
	if got["PATH"] != os.Getenv("PATH") || got["LC_TIME"] != "sv_SE.UTF-8" {
		t.Errorf("a listed variable did not arrive: PATH=%q LC_TIME=%q", got["PATH"], got["LC_TIME"])
	}
	if !strings.Contains(got["HOME"], "dream-agy-home-") {
		t.Errorf("HOME = %q, want the private one", got["HOME"])
	}
}

// inAgyEnv reports whether agyEnv lets name through.
func inAgyEnv(name string) bool {
	return len(trail.AllowEnv([]string{name + "=x"}, agyEnv...)) == 1
}

// TestCLICommandRefusesNilEnv: a nil env would hand the CLI fyr's whole
// environment, so it is refused rather than inherited.
func TestCLICommandRefusesNilEnv(t *testing.T) {
	if _, _, _, err := cliCommand(context.Background(), []string{"agy"}, "", t.TempDir(), nil); err == nil {
		t.Error("a nil env was accepted")
	}
	if _, _, _, err := cliCommand(context.Background(), []string{"agy"}, "", t.TempDir(), []string{}); err != nil {
		t.Errorf("an empty env: %v", err)
	}
}
