package dream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// fakeOracle is an agy that answers -p /permissions, /config and /hooks with the
// files the test put under $HOME/oracle, so a report only reaches it under
// the home it was given; $HOME/oracle/mode makes it sleep or fail instead.
// It records its working directory and environment under rec.
func fakeOracle(t *testing.T) (argv0, rec string) {
	t.Helper()
	bin, rec := t.TempDir(), t.TempDir()
	script := "#!/bin/sh\n" +
		"pwd > \"" + rec + "/cwd\"\n" +
		"env > \"" + rec + "/env\"\n" +
		"case \"$(cat \"$HOME/oracle/mode\" 2>/dev/null)\" in\n" +
		"sleep) exec sleep 30 ;;\n" +
		"hold) sleep 30 & echo $! > \"$HOME/oracle/pid\"; sleep 30 ;;\n" +
		"leave) sleep 30 & echo $! > \"$HOME/oracle/pid\" ;;\n" +
		"exit1) exit 1 ;;\n" +
		"esac\n" +
		"[ \"$1 $3 $4\" = \"-p --output-format json\" ] || exit 9\n" +
		"cat \"$HOME/oracle/${2#/}.json\"\n"
	argv0 = filepath.Join(bin, "agy")
	if err := os.WriteFile(argv0, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return argv0, rec
}

// oracleHome is a home with a login, an empty dream-work, and the oracle's
// answers: the saved sandbox reports of variant (testdata/oracle, plan D24).
func oracleHome(t *testing.T, variant string) (home, work string) {
	t.Helper()
	home = t.TempDir()
	write(t, filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token"), "token", fresh)
	work = harness.DreamWork(home)
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"permissions", "config"} {
		b, err := os.ReadFile(filepath.Join("testdata", "oracle", variant+"."+c+".json"))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(home, "oracle", c+".json"), string(b), fresh)
	}
	// The one /hooks report the sandbox saw: none (plan D24).
	write(t, filepath.Join(home, "oracle", "hooks.json"), read(t, filepath.Join("testdata", "oracle", "hooks.json")), fresh)
	return home, work
}

// configReport is the saved "none" /config report with set applied: a
// value replaces the key's, nil removes the key.
func configReport(t *testing.T, set map[string]any) string {
	t.Helper()
	var r map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join("testdata", "oracle", "none.config.json"))), &r); err != nil {
		t.Fatal(err)
	}
	cfg := r["command"].(map[string]any)["data"].(map[string]any)["config"].(map[string]any)
	for k, v := range set {
		if v == nil {
			delete(cfg, k)
		} else {
			cfg[k] = v
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestVisibleHomeVariants is each settings.json variant the sandbox ran
// through the real agy 1.4.9, with the verdict the report must give.
func TestVisibleHomeVariants(t *testing.T) {
	argv0, _ := fakeOracle(t)
	for _, c := range []struct{ variant, why string }{
		{"none", ""},
		{"strict", ""},
		{"bogus-mode", ""}, // an unknown mode reads back as request-review
		{"agentmode-plan", ""},
		{"deny-only", ""},
		{"ask-only", ""},
		{"always-proceed", "tool permission always-proceed approves tools without review"},
		{"pis", "tool permission proceed-in-sandbox approves tools without review"},
		{"agent-auto", "agent mode auto approves tools without review"},
		{"agentmode-accept-edits", "agent mode accept-edits approves tools without review"},
		{"allow-cmd", "an allow rule in the global permissions (command(ls))"},
		{"allow-str", "an allow rule in the global permissions (ls)"}, // /config drops it; /permissions does not
		{"outside", "access outside the workspace is allowed"},
		{"trusted", "a trusted workspace covers dream-work (/)"},
	} {
		t.Run(c.variant, func(t *testing.T) {
			home, work := oracleHome(t, c.variant)
			ok, why := visibleHome(argv0, home, work, os.Environ())
			if ok != (c.why == "") || why != c.why {
				t.Errorf("visibleHome = %v, %q; want %v, %q", ok, why, c.why == "", c.why)
			}
		})
	}
}

// TestVisibleHomeShapes is every report fyr has not seen, each failing
// closed with its reason.
func TestVisibleHomeShapes(t *testing.T) {
	argv0, _ := fakeOracle(t)
	cfg := func(home, body string) { write(t, filepath.Join(home, "oracle", "config.json"), body, fresh) }
	perms := func(home, body string) { write(t, filepath.Join(home, "oracle", "permissions.json"), body, fresh) }
	hooks := func(home, body string) { write(t, filepath.Join(home, "oracle", "hooks.json"), body, fresh) }
	report := func(name, data string) string {
		return `{"status":"SUCCESS","command":{"name":"` + name + `","data":` + data + `}}`
	}
	for _, c := range []struct {
		name, why string
		edit      func(home string)
	}{
		{"shared allow", "an allow rule in the shared permissions (command(ls))", func(h string) {
			perms(h, report("permissions", `{"permissions":[{"scope":"project"},{"scope":"shared","allow":["command(ls)"]},{"scope":"global"}]}`))
		}},
		{"project allow", "an allow rule in the project permissions (read_url(*))", func(h string) {
			perms(h, report("permissions", `{"permissions":[{"scope":"project","allow":["read_url(*)"]},{"scope":"global"}]}`))
		}},
		{"unknown key", `the oracle /permissions: an unknown key "grant" in the project scope`, func(h string) {
			perms(h, report("permissions", `{"permissions":[{"scope":"project","grant":["x"]},{"scope":"global"}]}`))
		}},
		{"rules not a list", "the oracle /permissions: deny rules of an unknown shape in the global scope", func(h string) {
			perms(h, report("permissions", `{"permissions":[{"scope":"global","deny":"x"}]}`))
		}},
		{"no global scope", "the oracle /permissions: report of an unknown shape (no global scope)", func(h string) {
			perms(h, report("permissions", `{"permissions":[]}`))
		}},
		{"nameless scope", "the oracle /permissions: a scope without a name", func(h string) {
			perms(h, report("permissions", `{"permissions":[{"allow":[]},{"scope":"global"}]}`))
		}},
		{"not json", "the oracle /permissions: output is not a report: invalid character '#' looking for beginning of value", func(h string) {
			perms(h, "# no permissions here")
		}},
		{"two reports", "the oracle /permissions: output holds more than one report", func(h string) {
			perms(h, report("permissions", `{"permissions":[{"scope":"global"}]}`)+"\n{}")
		}},
		{"failed", `the oracle /permissions: report of an unknown shape (status "ERROR")`, func(h string) {
			perms(h, `{"status":"ERROR","command":{"name":"permissions","data":{}}}`)
		}},
		{"another command", `the oracle /config: report of an unknown shape (status "SUCCESS")`, func(h string) {
			cfg(h, report("permissions", `{"permissions":[{"scope":"global"}]}`))
		}},
		{"no data", `the oracle /config: report of an unknown shape (status "SUCCESS")`, func(h string) {
			cfg(h, report("config", `null`))
		}},
		{"field missing", "the oracle /config: no toolPermission", func(h string) {
			cfg(h, configReport(t, map[string]any{"toolPermission": nil}))
		}},
		{"harmless key missing", "the oracle /config: no showTips", func(h string) {
			cfg(h, configReport(t, map[string]any{"showTips": nil}))
		}},
		{"key added", `the oracle /config: an unknown key "autoApprove"`, func(h string) {
			cfg(h, configReport(t, map[string]any{"autoApprove": true}))
		}},
		{"field retyped", "the oracle /config: no allowNonWorkspaceAccess of the known type", func(h string) {
			cfg(h, configReport(t, map[string]any{"allowNonWorkspaceAccess": "no"}))
		}},
		{"settings allow", "an allow rule in the settings (command(go))", func(h string) {
			cfg(h, configReport(t, map[string]any{"permissions": map[string]any{"allow": []string{"command(go)"}}}))
		}},
		{"permissions key added", `the oracle /config: an unknown permissions key "grant"`, func(h string) {
			cfg(h, configReport(t, map[string]any{"permissions": map[string]any{"deny": []string{}, "grant": []string{"x"}}}))
		}},
		{"artifact policy", "artifact review policy always-proceed is not asks-for-review", func(h string) {
			cfg(h, configReport(t, map[string]any{"artifactReviewPolicy": "always-proceed"}))
		}},
		{"relative trust", "a trusted workspace covers dream-work (~/x)", func(h string) {
			cfg(h, configReport(t, map[string]any{"trustedWorkspaces": []string{"~/x"}}))
		}},
		{"a hook", "1 hook(s) loaded, which can approve a tool", func(h string) {
			hooks(h, report("hooks", `{"hooks":[{"event":"PreToolUse","command":"approve.sh"}]}`))
		}},
		{"hooks of another shape", "the oracle /hooks: report of an unknown shape", func(h string) {
			hooks(h, report("hooks", `{"hooks":null}`))
		}},
		{"hooks key added", "the oracle /hooks: report of an unknown shape", func(h string) {
			hooks(h, report("hooks", `{"hooks":[],"plugins":[]}`))
		}},
		{"hooks not reported", "the oracle /hooks: output is not a report: EOF", func(h string) {
			hooks(h, "")
		}},
		{"exit 1", "the oracle /permissions: exit status 1", func(h string) {
			write(t, filepath.Join(h, "oracle", "mode"), "exit1", fresh)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, work := oracleHome(t, "none")
			c.edit(home)
			ok, why := visibleHome(argv0, home, work, os.Environ())
			if ok || why != c.why {
				t.Errorf("visibleHome = %v, %q; want false, %q", ok, why, c.why)
			}
		})
	}
}

// TestVisibleHomeTrustBelow is a trusted workspace that is not dream-work
// or above it: the operator's own checkouts, which add nothing here.
func TestVisibleHomeTrustBelow(t *testing.T) {
	argv0, _ := fakeOracle(t)
	home, work := oracleHome(t, "none")
	write(t, filepath.Join(home, "oracle", "config.json"), configReport(t, map[string]any{
		"trustedWorkspaces": []string{work + "-other", filepath.Join(home, "src")},
		"permissions":       map[string]any{"deny": []string{"command(rm)"}}}), fresh)
	if ok, why := visibleHome(argv0, home, work, os.Environ()); !ok {
		t.Errorf("visibleHome = false, %q; want true", why)
	}
}

// TestVisibleHomeWork is rules 1 and 2: the login under home, an empty
// dream-work, and no checkout or foreign .agents above it, as written and
// as it resolves; home's own .agents is the operator's (plan D8).
func TestVisibleHomeWork(t *testing.T) {
	argv0, _ := fakeOracle(t)
	for _, c := range []struct {
		name, why string
		edit      func(home, work string) string // returns the reason's path, if any
	}{
		{"home's .agents", "", func(h, w string) string { mkdir(t, filepath.Join(h, ".agents", "skills")); return "" }},
		{"no login", "no Antigravity login in %h", func(h, w string) string {
			os.Remove(filepath.Join(h, ".gemini", "antigravity-cli", "antigravity-oauth-token"))
			return ""
		}},
		{"work not empty", "dream-work is not empty (.agents)", func(h, w string) string { mkdir(t, filepath.Join(w, ".agents")); return "" }},
		{"work gone", "dream-work unreadable: open %w: no such file or directory", func(h, w string) string { os.Remove(w); return "" }},
		{"home a checkout", "dream-work is inside a git checkout (%h)", func(h, w string) string { mkdir(t, filepath.Join(h, ".git")); return "" }},
		{".agents between", "a .agents directory above dream-work (%h/.config)", func(h, w string) string {
			mkdir(t, filepath.Join(h, ".config", ".agents"))
			return ""
		}},
		{"checkout behind a link", "dream-work is inside a git checkout (%r)", func(h, w string) string {
			// dream-work's parent is a link into a checkout: only the
			// resolved walk sees the .git.
			real := filepath.Join(h, "real")
			mkdir(t, filepath.Join(real, ".git"))
			mkdir(t, filepath.Join(real, "fyr", "dream-work"))
			conf := filepath.Join(h, harness.StateDir)
			os.RemoveAll(conf)
			if err := os.Symlink(filepath.Join(real, "fyr"), conf); err != nil {
				t.Fatal(err)
			}
			return real
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, work := oracleHome(t, "none")
			real := c.edit(home, work)
			if r, err := filepath.EvalSymlinks(real); err == nil {
				real = r
			}
			want := strings.NewReplacer("%h", home, "%w", work, "%r", real).Replace(c.why)
			ok, why := visibleHome(argv0, home, work, os.Environ())
			if ok != (want == "") || why != want {
				t.Errorf("visibleHome = %v, %q; want %v, %q", ok, why, want == "", want)
			}
		})
	}
}

// TestVisibleHomeChild is how the oracle runs: from work, under HOME=home,
// with ADR-067's allow-list and nothing else from fyr's environment.
func TestVisibleHomeChild(t *testing.T) {
	argv0, rec := fakeOracle(t)
	home, work := oracleHome(t, "none")
	env := append(os.Environ(), "FYR_TEST_SECRET=hunter2", "HOME=/elsewhere")
	if ok, why := visibleHome(argv0, home, work, env); !ok {
		t.Fatalf("visibleHome = false, %q", why)
	}
	cwd, _ := os.ReadFile(filepath.Join(rec, "cwd"))
	if got := strings.TrimSpace(string(cwd)); got != work {
		t.Errorf("cwd = %q, want %q", got, work)
	}
	got, _ := os.ReadFile(filepath.Join(rec, "env"))
	if strings.Contains(string(got), "FYR_TEST_SECRET") || !strings.Contains(string(got), "HOME="+home+"\n") {
		t.Errorf("env = %s; want HOME=%s and no FYR_TEST_SECRET", got, home)
	}
}

// TestVisibleHomeTimeout is an oracle that does not answer: killed at the
// bound, the private branch.
func TestVisibleHomeTimeout(t *testing.T) {
	argv0, _ := fakeOracle(t)
	home, work := oracleHome(t, "none")
	write(t, filepath.Join(home, "oracle", "mode"), "sleep", fresh)
	old := oracleTimeout
	oracleTimeout = 200 * time.Millisecond
	t.Cleanup(func() { oracleTimeout = old })
	t0 := time.Now()
	ok, why := visibleHome(argv0, home, work, os.Environ())
	if ok || why != "the oracle /permissions timed out after 200ms" {
		t.Errorf("visibleHome = %v, %q; want false and the timeout", ok, why)
	}
	if d := time.Since(t0); d > 10*time.Second {
		t.Errorf("visibleHome took %s, past the bound and the grace", d)
	}
}

// TestVisibleHomeGroup is a helper agy started that keeps the output pipe:
// killed with agy's group at the deadline, and after a clean exit, whose
// answer counts once the grace is over (BUG-169's shape, as for the call).
func TestVisibleHomeGroup(t *testing.T) {
	argv0, _ := fakeOracle(t)
	oldT, oldG := oracleTimeout, oracleGrace
	oracleTimeout, oracleGrace = 2*time.Second, 300*time.Millisecond
	t.Cleanup(func() { oracleTimeout, oracleGrace = oldT, oldG })
	for _, c := range []struct{ mode, why string }{
		{"hold", "the oracle /permissions timed out after 2s"},
		{"leave", ""},
	} {
		t.Run(c.mode, func(t *testing.T) {
			home, work := oracleHome(t, "none")
			write(t, filepath.Join(home, "oracle", "mode"), c.mode, fresh)
			t0 := time.Now()
			ok, why := visibleHome(argv0, home, work, os.Environ())
			if ok != (c.why == "") || why != c.why {
				t.Errorf("visibleHome = %v, %q; want %v, %q", ok, why, c.why == "", c.why)
			}
			if d := time.Since(t0); d > 10*time.Second {
				t.Errorf("visibleHome took %s: the helper held it", d)
			}
			pid := readPid(t, filepath.Join(home, "oracle", "pid"))
			for i := 0; i < 20 && alive(pid); i++ {
				time.Sleep(100 * time.Millisecond)
			}
			if alive(pid) {
				syscall.Kill(pid, syscall.SIGKILL)
				t.Errorf("the helper (pid %d) outlived the oracle", pid)
			}
		})
	}
}

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}
