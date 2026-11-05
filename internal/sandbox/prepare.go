package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// wrapperScript is SB/bin/fyr, given SB and the installed binary. fyr's
// flags are last-wins, so a --home of the agent's own would override the one
// pinned here: the wrapper refuses it rather than pass it on.
const wrapperScript = `#!/usr/bin/env bash
for a in "$@"; do
  case "$a" in --home|--home=*|-home|-home=*) echo "fyr: this sandbox pins --home to "%[1]s >&2; exit 2 ;; esac
done
exec %[2]s "$1" --home %[1]s "${@:2}"
`

// Make is step 1: a directory of this invocation, never one that already
// exists. False means the box has failed and no later step runs.
func (b *Box) Make() bool {
	if err := os.MkdirAll(filepath.Dir(b.dir), 0o700); err != nil {
		b.Note("%v", err)
		b.Fail(ReasonSetup)
		return false
	}
	if err := os.Mkdir(b.dir, 0o700); os.IsExist(err) {
		b.Fail(ReasonExists)
		return false
	} else if err != nil {
		b.Note("%v", err)
		b.Fail(ReasonSetup)
		return false
	}
	return true
}

// Clone is step 2, then SB's CLI directory, the resolved paths and the held
// root on SB.
func (b *Box) Clone(ctx context.Context) bool {
	return b.cloneApp(ctx) && b.openSandbox()
}

func (b *Box) cloneApp(ctx context.Context) bool {
	// Step 2: the clone, its own toplevel, outside every checkout.
	// The clone stays a plain git call: it has no -C root for trail.Command
	// and clones the operator's own repository into a fresh directory (plan
	// 2026-10-27-harness-audit-fix D13).
	clone := exec.CommandContext(ctx, "git", "clone", "-q", "--template=", "--no-hardlinks", "--", b.c.Source, b.app)
	clone.Env = WithoutGit(os.Environ())
	if out, err := clone.CombinedOutput(); err != nil {
		b.Note("git clone: %v: %s", err, strings.TrimSpace(string(out)))
		b.Fail(ReasonClone)
		return false
	}
	top := trail.Command(b.app, "rev-parse", "--show-toplevel")
	top.Env = WithoutGit(os.Environ())
	gotTop, err := top.Output()
	wantTop, rerr := filepath.EvalSymlinks(b.app)
	if err != nil || rerr != nil || strings.TrimSpace(string(gotTop)) != wantTop {
		b.Note("%s is not its own repository", b.app)
		b.Fail(ReasonClone)
		return false
	}
	if err := checkRoots(b.c.RealHome, b.c.Repo, b.app, b.sb); err != nil {
		b.Note("%v", err)
		b.Fail(ReasonClone)
		return false
	}
	if err := outsideCheckout(b.dir); err != nil {
		b.Note("%v (BUG-106)", err)
		b.Fail(ReasonClone)
		return false
	}
	return true
}

// openSandbox makes SB's CLI directory, records where the sandbox
// directory, the clone and SB resolve, as the guards approved them, and
// holds SB open.
func (b *Box) openSandbox() bool {
	if err := os.MkdirAll(filepath.Join(b.sb, ".gemini", "antigravity-cli"), 0o700); err != nil {
		b.Fail(ReasonSetup)
		return false
	}
	b.resolved = map[string]string{}
	for _, p := range []string{b.dir, b.app, b.sb} {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			b.Fail(ReasonSetup)
			return false
		}
		b.resolved[p] = r
	}
	roots := make([]string, 0, len(b.resolved))
	for _, r := range b.resolved {
		roots = append(roots, r)
	}
	mounts, err := mountChains(roots...)
	if err != nil {
		b.Note("the mount table: %v", err)
		b.Fail(ReasonSetup)
		return false
	}
	b.mounts = mounts
	root, err := os.OpenRoot(b.sb)
	if err != nil {
		b.Fail(ReasonSetup)
		return false
	}
	b.root = root
	return true
}

// Fingerprint is step 4: the real config, before the verb's setup and
// before anything installs, so a setup that writes it is seen (IN50 of the
// 2026-10-27 audit).
func (b *Box) Fingerprint() {
	b.realBefore = fingerprintWith(b.c.RealHome, b.c.SkipStores, b.c.BrainWatch)
}

// Install is step 6: the harness, as `fyr install` gives it to a place
// (D16), and the wrapper that pins --home on every fyr call the agent makes.
func (b *Box) Install(ctx context.Context) bool {
	args := []string{"install", "--home", b.sb}
	if b.c.Repo != "" {
		args = append(args, "--repo", b.c.Repo)
	}
	inst := exec.CommandContext(ctx, b.c.Self, args...)
	inst.Dir = b.sb
	inst.Env = FyrEnv()
	log, err := os.Create(filepath.Join(b.dir, "install.log"))
	if err != nil {
		b.Fail(ReasonInstall)
		return false
	}
	inst.Stdout, inst.Stderr = log, log
	err = inst.Run()
	log.Close()
	if err != nil {
		b.Note("fyr install: %v (see install.log)", err)
		b.Fail(ReasonInstall)
		return false
	}
	bin := filepath.Join(b.sb, "bin")
	// In a container the installed binary is the host's (a darwin one on
	// the Mac): the wrapper execs the image's fyr, with the same pin.
	self := filepath.Join(b.sb, filepath.FromSlash(harness.BinPath))
	if b.c.Docker != nil {
		self = ContainerFyr
	}
	wrapper := fmt.Sprintf(wrapperScript, ShellQuote(b.sb), ShellQuote(self))
	if err := os.MkdirAll(bin, 0o700); err != nil || os.WriteFile(filepath.Join(bin, "fyr"), []byte(wrapper), 0o755) != nil {
		b.Fail(ReasonInstall)
		return false
	}
	return true
}

// Settings is step 7: the grants, and trust in the clone only.
func (b *Box) Settings() bool {
	settings, _ := json.MarshalIndent(map[string]any{"permissions": map[string]any{"allow": b.c.Grants}, "trustedWorkspaces": []string{b.app}}, "", " ")
	sp := filepath.Join(b.sb, ".gemini", "antigravity-cli", "settings.json")
	if err := os.WriteFile(sp, settings, 0o600); err != nil {
		b.Fail(ReasonSettings)
		return false
	}
	if got, err := os.ReadFile(sp); err != nil || strings.Contains(string(got), "toolPermission") {
		b.Fail(ReasonSettings)
		return false
	}
	return true
}

// Baseline is step 8: the credential baseline, before the token is linked
// (D17), over the whole sandbox directory: the clone, the CLI's output and
// the logs are kept as long as the home is.
func (b *Box) Baseline() { b.credsBefore = scanCreds(b.dir) }
