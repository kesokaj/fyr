package isolate

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
