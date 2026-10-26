package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

// namedGrants is the allow list of a named row (run.sh:212).
var namedGrants = func() []string {
	var out []string
	for _, c := range strings.Fields("git go gofmt cat ls date mkdir pwd grep find wc head tail printf echo cp ln test diff fyr chmod readlink bash python3 sleep govulncheck") {
		out = append(out, "command("+c+")")
	}
	return append(out, "read_file(*)", "write_file(*)")
}()

// wrapperScript is SB/bin/fyr, given SB and the installed binary. fyr's
// flags are last-wins, so a --home of the agent's own would override the one
// pinned here: the wrapper refuses it rather than pass it on.
const wrapperScript = `#!/usr/bin/env bash
for a in "$@"; do
  case "$a" in --home|--home=*|-home|-home=*) echo "fyr: this sandbox pins --home to "%[1]s >&2; exit 2 ;; esac
done
exec %[2]s "$1" --home %[1]s "${@:2}"
`

// prepare is steps 2-8b; false means the row has failed and no CLI runs.
func (w *row) prepare(ctx context.Context) bool {
	// Step 2: the clone, its own toplevel, outside every checkout.
	clone := exec.CommandContext(ctx, "git", "clone", "-q", "--no-hardlinks", "--", w.o.Fixture, w.app)
	clone.Env = withoutGit(os.Environ())
	if out, err := clone.CombinedOutput(); err != nil {
		w.note("git clone: %v: %s", err, strings.TrimSpace(string(out)))
		w.fail(ReasonClone)
		return false
	}
	top := exec.Command("git", "-C", w.app, "rev-parse", "--show-toplevel")
	top.Env = withoutGit(os.Environ())
	gotTop, err := top.Output()
	wantTop, rerr := filepath.EvalSymlinks(w.app)
	if err != nil || rerr != nil || strings.TrimSpace(string(gotTop)) != wantTop {
		w.note("%s is not its own repository", w.app)
		w.fail(ReasonClone)
		return false
	}
	if err := checkRoots(w.o.RealHome, w.o.Repo, w.app, w.sb); err != nil {
		w.note("%v", err)
		w.fail(ReasonClone)
		return false
	}
	if err := outsideCheckout(w.dir); err != nil {
		w.note("%v (BUG-106)", err)
		w.fail(ReasonClone)
		return false
	}
	if err := os.MkdirAll(filepath.Join(w.sb, ".gemini", "antigravity-cli"), 0o700); err != nil {
		w.fail(ReasonSetup)
		return false
	}
	w.resolved = map[string]string{}
	for _, p := range []string{w.dir, w.app, w.sb} {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			w.fail(ReasonSetup)
			return false
		}
		w.resolved[p] = r
	}
	// Step 4: what the trail held before the agent ran; a check counts only
	// the records the run adds (run.sh:52-62).
	before := filepath.Join(w.sb, "records-before.txt")
	var list []byte
	if _, err := os.Stat(filepath.Join(w.app, ".agents", "trail.jsonl")); err == nil {
		cmd := exec.CommandContext(ctx, w.o.Self, "record", "list", "--home", w.sb)
		cmd.Dir = w.app
		cmd.Env = w.fyrEnv()
		var errb strings.Builder
		cmd.Stderr = &errb
		if list, err = cmd.Output(); err != nil {
			w.note("record list: %v: %s", err, strings.TrimSpace(errb.String()))
			w.fail(ReasonRecords)
			return false
		}
	}
	if err := os.WriteFile(before, list, 0o600); err != nil {
		w.fail(ReasonRecords)
		return false
	}
	// Step 5: the row's setup, in the clone.
	if w.r.Setup != "" {
		if !w.bash(ctx, w.r.Setup, w.setupEnv(), "setup.log") {
			w.fail(ReasonSetup)
			return false
		}
	}
	// Step 6: the real config, before anything installs.
	w.realBefore = fingerprint(w.o.RealHome)
	// Step 7: the harness, as `fyr install` gives it to a place (D16), and
	// the wrapper that pins --home on every fyr call the agent makes.
	args := []string{"install", "--home", w.sb}
	if w.o.Repo != "" {
		args = append(args, "--repo", w.o.Repo)
	}
	inst := exec.CommandContext(ctx, w.o.Self, args...)
	inst.Dir = w.sb
	inst.Env = w.fyrEnv()
	log, err := os.Create(filepath.Join(w.dir, "install.log"))
	if err != nil {
		w.fail(ReasonInstall)
		return false
	}
	inst.Stdout, inst.Stderr = log, log
	err = inst.Run()
	log.Close()
	if err != nil {
		w.note("fyr install: %v (see install.log)", err)
		w.fail(ReasonInstall)
		return false
	}
	bin := filepath.Join(w.sb, "bin")
	wrapper := fmt.Sprintf(wrapperScript, shellQuote(w.sb), shellQuote(filepath.Join(w.sb, filepath.FromSlash(harness.BinPath))))
	if err := os.MkdirAll(bin, 0o700); err != nil || os.WriteFile(filepath.Join(bin, "fyr"), []byte(wrapper), 0o755) != nil {
		w.fail(ReasonInstall)
		return false
	}
	// Step 8: the grants, and trust in the clone only.
	allow := namedGrants
	if reason, wide := w.r.Wide(); wide {
		w.note("runs with command(*) in its throwaway clone: %s", reason)
		allow = []string{"command(*)", "read_file(*)", "write_file(*)"}
	}
	settings, _ := json.MarshalIndent(map[string]any{"permissions": map[string]any{"allow": allow}, "trustedWorkspaces": []string{w.app}}, "", " ")
	sp := filepath.Join(w.sb, ".gemini", "antigravity-cli", "settings.json")
	if err := os.WriteFile(sp, settings, 0o600); err != nil {
		w.fail(ReasonSettings)
		return false
	}
	if b, err := os.ReadFile(sp); err != nil || strings.Contains(string(b), "toolPermission") {
		w.fail(ReasonSettings)
		return false
	}
	// Step 8b: the credential baseline, before the token is linked (D17),
	// over the whole sandbox directory: the clone, the CLI's output and the
	// logs are kept as long as the home is.
	w.credsBefore = scanCreds(w.dir)
	return ctx.Err() == nil
}
