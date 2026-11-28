package isolate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type rig struct {
	t                        *testing.T
	tmp, src, real, bin, rec string
	self, agy                string
	out                      bytes.Buffer
}

const recAgy = `#!/bin/bash
REC=@REC@
n=$(( $(cat "$REC/agy.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$REC/agy.n"
printf '%s\n' "$@" > "$REC/agy.args.$n"
env > "$REC/agy.env.$n"; pwd > "$REC/agy.cwd.$n"
cp "$HOME/.gemini/antigravity-cli/settings.json" "$REC/settings.$n" 2>/dev/null
[ -L "$HOME/.gemini/antigravity-cli/antigravity-oauth-token" ] && echo yes > "$REC/linked.$n"
[ -L "$HOME/Library/Keychains" ] && readlink "$HOME/Library/Keychains" > "$REC/keychains.$n"
if [ -e "$REC/hang" ]; then exec sleep 300; fi
echo "stderr $n" >&2
echo "session $n"
`

const fakeSelf = `#!/bin/bash
case "$1" in
install) mkdir -p "$3/.local/bin"; printf '#!/bin/bash\necho "fyr $*"\n' > "$3/.local/bin/fyr"; chmod +x "$3/.local/bin/fyr" ;;
esac
`

func put(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func newRig(t *testing.T) *rig {
	t.Helper()
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	tmp := outsideTemp(t)
	r := &rig{
		t: t, tmp: tmp, src: filepath.Join(tmp, "src"), real: filepath.Join(tmp, "real"),
		bin: filepath.Join(tmp, "bin"), rec: filepath.Join(tmp, "rec"),
	}
	for _, d := range []string{r.bin, r.rec} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, r.src)
	put(t, filepath.Join(r.src, "README.md"), "demo\n", 0o644)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "source"}} {
		cmd := exec.Command("git", append([]string{"-C", r.src, "-c", "user.name=t", "-c", "user.email=t@localhost"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	put(t, filepath.Join(r.real, TokenRel), "token", 0o600)
	r.self, r.agy = filepath.Join(r.bin, "self"), filepath.Join(r.bin, "agy")
	put(t, r.self, fakeSelf, 0o755)
	put(t, r.agy, strings.ReplaceAll(recAgy, "@REC@", ShellQuote(r.rec)), 0o755)
	t.Setenv("HOME", r.real)
	return r
}

func (r *rig) config(name string) Config {
	return Config{
		Dir: filepath.Join(r.tmp, "boxes", name), Source: r.src, RealHome: r.real, Self: r.self,
		Grants: []string{"command(git)", "read_file(*)"}, GitName: "w", GitEmail: "w@localhost",
		Timeout: time.Minute, PrintTimeout: "20m", StopGrace: 100 * time.Millisecond,
		Prefix: "test: " + name, Out: &r.out,
		TestRO: []string{r.bin}, TestRW: []string{r.rec},
	}
}

func (r *rig) box(c Config) *Box {
	b := New(c)
	r.t.Cleanup(func() {
		if b.root != nil {
			b.root.Close()
		}
	})
	return b
}

func prepare(ctx context.Context, b *Box) bool {
	if !b.Clone(ctx) {
		return false
	}
	b.Fingerprint()
	if !b.Install(ctx) || !b.Settings() {
		return false
	}
	b.Baseline()
	return true
}

func (r *rig) prepared(c Config) *Box {
	r.t.Helper()
	b := r.box(c)
	if !b.Make() || !prepare(context.Background(), b) {
		r.t.Fatalf("prepare: %v\n%s", b.Reasons(), r.out.String())
	}
	return b
}

func (r *rig) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.rec, name))
	return string(b)
}

var (
	sharedFyrOnce sync.Once
	sharedFyrPath string
	sharedFyrRoot string
	sharedFyrErr  error
)

func goEnvVal(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// sharedFyrBin builds ./cmd/fyr once per test binary invocation so creds and
// Landlock tests reuse the same compiled binary instead of running go build
// twice.
func sharedFyrBin(t *testing.T) (bin, root string) {
	t.Helper()
	sharedFyrOnce.Do(func() {
		sharedFyrRoot, sharedFyrErr = filepath.Abs(filepath.Join("..", ".."))
		if sharedFyrErr != nil {
			return
		}
		goModCache, err := goEnvVal("GOMODCACHE")
		if err != nil {
			sharedFyrErr = err
			return
		}
		goBuildCache, err := goEnvVal("GOCACHE")
		if err != nil {
			sharedFyrErr = err
			return
		}
		dir, err := os.MkdirTemp("", "fyr-isolate-bin-*")
		if err != nil {
			sharedFyrErr = err
			return
		}
		sharedFyrPath = filepath.Join(dir, "fyr")
		cmd := exec.Command("go", "build", "-o", sharedFyrPath, "./cmd/fyr")
		cmd.Dir = sharedFyrRoot
		cmd.Env = append(FyrEnv(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOMODCACHE="+goModCache, "GOCACHE="+goBuildCache)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			sharedFyrErr = fmt.Errorf("go build ./cmd/fyr: %w\n%s", err, out)
		}
	})
	if sharedFyrErr != nil {
		t.Fatal(sharedFyrErr)
	}
	return sharedFyrPath, sharedFyrRoot
}

// TestScanCredsIgnoresFyrOwnPatterns covers BUG-538 and BUG-544: compiled fyr
// .rodata strings, post-Baseline() indexing/transcripts/WAL of the tracked repo
// and seeded brain.db trail records, and >maxScan ELF/Mach-O Go build-cache
// outputs pass, while novel credentials planted anywhere fail closed.
