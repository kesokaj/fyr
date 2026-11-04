package main

import (
	"bufio"
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/work"
)

// workAgy is the worker's CLI the verb tests drive: by default one commit in
// the Kickoff's scope and one handoff record, the strict export line written
// as fyr record writes it. internal/work's tests cover every other shape.
const workAgy = `#!/bin/bash
CTL=@CTL@
if [ -e "$CTL/hang" ]; then touch "$CTL/started"; exec sleep 300; fi
[ -e "$CTL/nocommit" ] && { echo "agy says ok"; exit 0; }
f=a.txt; me=$(basename "$(dirname "$HOME")"); [ -e "$CTL/file-$me" ] && f=$(cat "$CTL/file-$me")
echo work >> "$f"; git add "$f"; git commit -qm "work on $f"
mkdir -p .agents
# Dated now: the verb refuses a record dated outside the worker's run (D140).
printf '%s\n' '{"date":"'"$(date +%F)"'","id":"HANDOFF-2026-10-29-1800-'"$me"'","kind":"handoff","status":"","title":"Handoff","updated":"'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'","body":"# Handoff\n\n## Next Steps\n\n1. next\n"}' >> .agents/trail.jsonl
git add .agents/trail.jsonl; git commit -qm records
echo "agy says ok"
`

// workSelfScript stands in for the fyr each sandbox installs: install makes
// the binary the sandbox's wrapper execs.
const workSelfScript = `#!/bin/bash
case "$1" in
install) mkdir -p "$3/.local/bin"; printf '#!/bin/bash\necho "fyr $*"\n' > "$3/.local/bin/fyr"; chmod +x "$3/.local/bin/fyr" ;;
esac
`

// workRig is a lead checkout with an identity and a gitignored scratch, a
// real home with a login, a fyr home, the fake agy on PATH and the fake fyr
// as the sandbox's, all outside every checkout (D11).
type workRig struct {
	t                                   *testing.T
	project, home, real, ctl, kick, src string
}

func newWorkRig(t *testing.T) *workRig {
	t.Helper()
	for _, tool := range []string{"git", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s here", tool)
		}
	}
	checkout, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	if r, err := filepath.EvalSymlinks(tmp); err == nil {
		tmp = r
	}
	if out, err := exec.Command("git", "-C", tmp, "rev-parse", "--git-dir").CombinedOutput(); err == nil {
		t.Skipf("the test's temporary directory is inside a git checkout (%s): work refuses that by design", strings.TrimSpace(string(out)))
	}
	r := &workRig{t: t, project: filepath.Join(tmp, "project"), home: filepath.Join(tmp, "fyr-home"), real: filepath.Join(tmp, "real"),
		ctl: filepath.Join(tmp, "ctl"), kick: filepath.Join(tmp, "kick"), src: checkout}
	bin := filepath.Join(tmp, "bin")
	for _, d := range []string{r.project, r.home, r.ctl, r.kick, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(r.real, sandbox.TokenRel), "token")
	writeTestFile(t, filepath.Join(bin, "agy"), strings.ReplaceAll(workAgy, "@CTL@", sandbox.ShellQuote(r.ctl)))
	self := filepath.Join(tmp, "self")
	writeTestFile(t, self, workSelfScript)
	for _, f := range []string{filepath.Join(bin, "agy"), self} {
		if err := os.Chmod(f, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := workSelf
	workSelf = func() (string, error) { return self, nil }
	t.Cleanup(func() { workSelf = old })
	// A recording fake docker: a --kill's sweep of a stale run never asks
	// the machine's daemon (the v5.0.0 gate's security review).
	writeTestFile(t, filepath.Join(bin, "docker"), "#!/bin/bash\nprintf '%s\\n' \"$*\" >> "+
		sandbox.ShellQuote(filepath.Join(r.ctl, "docker.calls"))+"\n"+strings.TrimPrefix(fakeVerbDocker, "#!/bin/bash\n"))
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("HOME", r.real)
	t.Setenv("FYR_REPO", "")
	// A known zone for refusal 22, as internal/work's rig pins it: a
	// developer's TZ=:/etc/localtime would refuse every run (workers D145, G3).
	t.Setenv("TZ", "UTC")

	r.git("init", "-q")
	r.git("config", "user.name", "Lead Person")
	r.git("config", "user.email", "lead@localhost")
	writeTestFile(t, filepath.Join(r.project, ".gitignore"), ".agents/scratch/\n")
	writeTestFile(t, filepath.Join(r.project, "a.txt"), "a\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")
	t.Chdir(r.project)
	return r
}

func (r *workRig) git(args ...string) string {
	r.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", r.project}, args...)...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// kickoff writes the Kickoff file name.md, its scope a.txt.
func (r *workRig) kickoff(name string) string { return r.kickoffFiles(name, "a.txt") }

// kickoffFiles writes the Kickoff file name.md with the Files: line files.
func (r *workRig) kickoffFiles(name, files string) string {
	p := filepath.Join(r.kick, name+".md")
	writeTestFile(r.t, p, "Project: this clone. Do the phase.\nFiles: "+files+"\nStanding approvals: commit.\n")
	return p
}

// work runs `fyr work` with the rig's home and install source and returns
// the output and the exit status.
func (r *workRig) work(args ...string) (string, int) {
	r.t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"work", "--repo", r.src, "--home", r.home}, args...), &out)
	var code exitCode
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &code):
		return out.String(), int(code)
	}
	r.t.Fatalf("work %v: %v\n%s", args, err, out.String())
	return "", -1
}

// sandboxes is every worker directory made under the project's sandbox root.
func (r *workRig) sandboxes() []string {
	m, _ := filepath.Glob(filepath.Join(work.SandboxRoot(r.home, r.project), "*", "*"))
	return m
}

// TestWorkVerbPass: one Kickoff, one good worker, exit 0, the branch
// fetched into the checkout and the result contract printed.
func TestWorkVerbPass(t *testing.T) {
	r := newWorkRig(t)
	out, code := r.work(r.kickoff("fix-a"))
	lines := strings.Split(out, "\n")
	if code != 0 || !strings.HasPrefix(out, "work: stamp ") || !strings.Contains(out, "\nwork: fix-a  PASS  agy exit 0  ") ||
		!strings.Contains(out, "work: fix-a: handoff HANDOFF-2026-10-29-1800-fix-a") ||
		!strings.HasSuffix(lines[0], "  runner host (--runner host)") ||
		!strings.HasSuffix(lines[1], "  runner host (--runner host)") {
		t.Fatalf("exit %d, want 0 with the stamp, the PASS line, the runner and the handoff:\n%s", code, out)
	}
	if got := r.git("log", "-1", "--format=%s", "work/fix-a"); got != "records" {
		t.Errorf("work/fix-a in the checkout ends at %q", got)
	}
}

// TestWorkVerbFail: a worker that commits nothing fails, exit 1.
func TestWorkVerbFail(t *testing.T) {
	r := newWorkRig(t)
	writeTestFile(t, filepath.Join(r.ctl, "nocommit"), "")
	out, code := r.work(r.kickoff("fix-a"))
	if code != 1 || !strings.Contains(out, "work: fix-a  FAIL (") || !strings.Contains(out, "no commits") {
		t.Fatalf("exit %d, want 1 with no commits:\n%s", code, out)
	}
}

// TestWorkVerbRefusals: every refusal and usage error exits 2 before any
// sandbox exists: two files sharing a path, a --max-workers outside 1 to 32,
// --kill with a file among them, and a --runner the verb cannot run.
func TestWorkVerbRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(r *workRig) []string
		want  string
	}{
		"no file":      {func(r *workRig) []string { return nil }, "usage: fyr work"},
		"shared scope": {func(r *workRig) []string { return []string{r.kickoff("fix-a"), r.kickoff("fix-b")} }, "share scope: a.txt and a.txt"},
		"max 33":       {func(r *workRig) []string { return []string{"--max-workers", "33", r.kickoff("fix-a")} }, "--max-workers 33: give 1 to 32"},
		"max 0":        {func(r *workRig) []string { return []string{"--max-workers", "0", r.kickoff("fix-a")} }, "--max-workers 0: give 1 to 32"},
		"kill a file":  {func(r *workRig) []string { return []string{"--kill", "2026-10-29-170000-42", r.kickoff("fix-a")} }, "--kill takes a stamp and no Kickoff file"},
		"kill no run":  {func(r *workRig) []string { return []string{"--kill", "2026-10-29-170000-42"} }, "work: no run 2026-10-29-170000-42"},
		"kill a path":  {func(r *workRig) []string { return []string{"--kill", "../x"} }, "not a stamp"},
		"dirty tree": {func(r *workRig) []string {
			writeTestFile(r.t, filepath.Join(r.project, "new.txt"), "x\n")
			return []string{r.kickoff("fix-a")}
		}, "uncommitted or untracked changes"},
		"not a checkout": {func(r *workRig) []string {
			r.t.Chdir(r.kick)
			return []string{r.kickoff("fix-a")}
		}, "is not a git checkout"},
		"short timeout": {func(r *workRig) []string { return []string{"--timeout", "9m", r.kickoff("fix-a")} }, "--timeout 9m0s"},
		"zero timeout":  {func(r *workRig) []string { return []string{"--timeout", "0", r.kickoff("fix-a")} }, "--timeout 0s"},
		"no Files line": {func(r *workRig) []string {
			p := filepath.Join(r.kick, "fix-a.md")
			writeTestFile(r.t, p, "Project: this clone.\n")
			return []string{p}
		}, "Files:"},
		"unknown runner": {func(r *workRig) []string { return []string{"--runner", "podman", r.kickoff("fix-a")} }, `work: --runner "podman": give host or docker`},
		// A test binary is no installed build: refused before any docker
		// call, whether or not this machine has docker (D108).
		"docker from a dev build":        {func(r *workRig) []string { return []string{"--runner", "docker", r.kickoff("fix-a")} }, "work: --runner docker: this fyr is a development build"},
		"image without docker":           {func(r *workRig) []string { return []string{"--image", "x:1", r.kickoff("fix-a")} }, "work: --image and --agy-linux need --runner docker"},
		"agy-linux without docker":       {func(r *workRig) []string { return []string{"--agy-linux", "/x/agy", r.kickoff("fix-a")} }, "work: --image and --agy-linux need --runner docker"},
		"check a file":                   {func(r *workRig) []string { return []string{"--check", r.kickoff("fix-a")} }, "work: --check takes no Kickoff file"},
		"check and kill":                 {func(r *workRig) []string { return []string{"--check", "--kill", "2026-10-29-170000-42"} }, "work: --check and --kill cannot be used together"},
		"check unknown runner":           {func(r *workRig) []string { return []string{"--check", "--runner", "podman"} }, `work: --runner "podman": give host or docker`},
		"check host with image":          {func(r *workRig) []string { return []string{"--check", "--runner", "host", "--image", "x:1"} }, "work: --image and --agy-linux need --runner docker"},
		"check image without docker":     {func(r *workRig) []string { return []string{"--check", "--image", "x:1"} }, "work: --image and --agy-linux need --runner docker"},
		"check agy-linux without docker": {func(r *workRig) []string { return []string{"--check", "--agy-linux", "/x/agy"} }, "work: --image and --agy-linux need --runner docker"},
		"bad install source": {func(r *workRig) []string {
			return []string{"--repo", r.kick, r.kickoff("fix-a")}
		}, "work: "},
	} {
		t.Run(name, func(t *testing.T) {
			r := newWorkRig(t)
			out, code := r.work(tc.setup(r)...)
			if code != 2 || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d, want 2 with %q:\n%s", code, tc.want, out)
			}
			if strings.Contains(out, "work: stamp ") || len(r.sandboxes()) > 0 {
				t.Errorf("a refusal started a run: sandboxes %v\n%s", r.sandboxes(), out)
			}
		})
	}
}

// TestWorkVerbSignals: SIGINT, SIGTERM, SIGHUP and SIGQUIT during the session each
// end the worker killed, exit 1, with the result contract printed and the
// token link gone from every sandbox left.
func TestWorkVerbSignals(t *testing.T) {
	for _, s := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT} {
		t.Run(s.String(), func(t *testing.T) {
			r := newWorkRig(t)
			writeTestFile(t, filepath.Join(r.ctl, "hang"), "")
			go func() {
				for i := 0; i < 600; i++ {
					if _, err := os.Stat(filepath.Join(r.ctl, "started")); err == nil {
						syscall.Kill(os.Getpid(), s)
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
			}()
			out, code := r.work(r.kickoff("fix-a"))
			if code != 1 || !strings.Contains(out, "work: fix-a  FAIL (") || !strings.Contains(out, "killed") {
				t.Fatalf("exit %d, want 1 with the worker killed:\n%s", code, out)
			}
			for _, dir := range r.sandboxes() {
				if _, err := os.Lstat(filepath.Join(dir, "home", sandbox.TokenRel)); !os.IsNotExist(err) {
					t.Errorf("the token link is still in %s: %v", dir, err)
				}
			}
		})
	}
}

// TestWorkVerbSeveral: two Kickoffs with disjoint Files: lines both run,
// exit 0, and their blocks print in the order given.
func TestWorkVerbSeveral(t *testing.T) {
	r := newWorkRig(t)
	writeTestFile(t, filepath.Join(r.ctl, "file-fix-b"), "b.txt")
	out, code := r.work("--max-workers", "2", r.kickoff("fix-a"), r.kickoffFiles("fix-b", "b.txt"))
	if code != 0 || !strings.Contains(out, "  2 workers  ") {
		t.Fatalf("exit %d, want 0 with two workers:\n%s", code, out)
	}
	i, j := strings.Index(out, "\nwork: fix-a  PASS"), strings.Index(out, "\nwork: fix-b  PASS")
	if i < 0 || j < i {
		t.Fatalf("the blocks are not both PASS in file order:\n%s", out)
	}
	for _, b := range []string{"work/fix-a", "work/fix-b"} {
		r.git("rev-parse", "--verify", b)
	}
}

// workHolderEnv makes the test binary a live run's stand-in for --kill: it
// makes <root>/<stamp>/pid with its own pid in the stamp, holds the file's
// exclusive flock, prints the stamp and exits 3 on SIGTERM. Never the go
// test process itself (D33).
const workHolderEnv = "FYR_WORK_VERB_HOLDER"

func workHolder(root string) int {
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	stamp := fmt.Sprintf("2026-10-29-170000-%d", os.Getpid())
	dir := filepath.Join(root, stamp)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 1
	}
	f, err := os.OpenFile(filepath.Join(dir, "pid"), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil || syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
		return 1
	}
	fmt.Fprintf(f, "%d 1\n", os.Getpid())
	fmt.Println(stamp)
	select {
	case <-term:
		return 3
	case <-time.After(2 * time.Minute):
		return 4
	}
}

// TestWorkVerbKill: --kill sends SIGTERM to the live run its stamp names,
// exit 0; the same stamp once the run is gone is a stale pid file, exit 2.
func TestWorkVerbKill(t *testing.T) {
	r := newWorkRig(t)
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), workHolderEnv+"="+sandbox.Root(r.home, "work"))
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	line, _ := bufio.NewReader(pipe).ReadString('\n')
	stamp := strings.TrimSpace(line)
	out, code := r.work("--kill", stamp)
	if code != 0 || !strings.Contains(out, fmt.Sprintf("work: SIGTERM sent to %d, the run %s", cmd.Process.Pid, stamp)) {
		t.Fatalf("exit %d, want 0 naming the pid:\n%s", code, out)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("the stand-in ended %v, want exit 3 on SIGTERM", err)
	}
	if out, code := r.work("--kill", stamp); code != 2 || !strings.Contains(out, "is stale: no run holds it") {
		t.Errorf("exit %d, want 2 on the stale pid file:\n%s", code, out)
	}
	if calls, _ := os.ReadFile(filepath.Join(r.ctl, "docker.calls")); !strings.Contains(string(calls), "label=fyr.work="+stamp) {
		t.Errorf("the stale sweep did not reach the fake docker: %q", calls)
	}
}

// fakeVerbDocker has one image, good:1, at version vtest, on a local
// daemon that applies every limit: enough for the verb's flags to be seen
// in its refusals.
const fakeVerbDocker = `#!/bin/bash
case "$1 $2" in
"context inspect") echo unix:///var/run/docker.sock ;;
"info --format") echo "true true true true 4 17179869184" ;;
"image inspect") [ "${@: -1}" = good:1 ] || { echo "Error: No such image" >&2; exit 1; }
  echo "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef amd64" ;;
"run --rm") echo "fyr vtest" ;;
esac
`

// --image and --agy-linux reach the run: from an installed build, a
// missing image is refused by the ref given, and a given linux agy by its
// path.
func TestWorkVerbDockerFlags(t *testing.T) {
	r := newWorkRig(t)
	old := version
	version = "vtest"
	t.Cleanup(func() { version = old })
	bin := t.TempDir()
	writeTestFile(t, filepath.Join(bin, "docker"), fakeVerbDocker)
	if err := os.Chmod(filepath.Join(bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("DOCKER_CONTEXT", "")
	if out, code := r.work("--runner", "docker", "--image", "other:1", r.kickoff("fix-a")); code != 2 || !strings.Contains(out, "work: --image other:1: not on this machine") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if out, code := r.work("--runner", "docker", "--image", "good:1", "--agy-linux", "/nowhere/agy", r.kickoff("fix-a")); code != 2 || !strings.Contains(out, "--agy-linux /nowhere/agy") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(r.sandboxes()) > 0 {
		t.Fatalf("a refusal made sandboxes %v", r.sandboxes())
	}
}

// TestWorkVerbCheck pins `fyr work --check [flags]`: it prints the runner and
// why, exits 0, and makes no sandbox, network or slot.
func TestWorkVerbCheck(t *testing.T) {
	r := newWorkRig(t)

	out, code := r.work("--check", "--runner", "host")
	if want := "work: runner host (--runner host)\n"; code != 0 || out != want {
		t.Fatalf("--check --runner host: exit %d, got %q, want %q", code, out, want)
	}
	if calls, _ := os.ReadFile(filepath.Join(r.ctl, "docker.calls")); len(calls) > 0 {
		t.Fatalf("--check --runner host called docker: %q", calls)
	}

	out, code = r.work("--check")
	wantDevel := "work: runner host (this fyr is a development build ((devel)); run fyr install, then build the image from the installed fyr: " + work.BuildLine + ")\n"
	if code != 0 || out != wantDevel {
		t.Fatalf("--check on devel: exit %d\ngot:  %q\nwant: %q", code, out, wantDevel)
	}

	old := version
	version = "vtest"
	t.Cleanup(func() { version = old })

	var elfBin [64]byte
	copy(elfBin[:4], "\x7fELF")
	elfBin[4], elfBin[5], elfBin[6] = byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)
	elfBin[16], elfBin[18], elfBin[20], elfBin[52] = byte(elf.ET_EXEC), byte(elf.EM_X86_64), byte(elf.EV_CURRENT), 64
	agyLinux := filepath.Join(t.TempDir(), "agy-linux")
	if err := os.WriteFile(agyLinux, elfBin[:], 0o755); err != nil {
		t.Fatal(err)
	}

	out, code = r.work("--check", "--runner", "docker", "--image", "good:1", "--agy-linux", agyLinux)
	if want := "work: runner docker\n"; code != 0 || out != want {
		t.Fatalf("--check docker ready: exit %d, got %q, want %q", code, out, want)
	}
	if _, err := os.Lstat(sandbox.Root(r.home, "work")); !os.IsNotExist(err) || len(r.sandboxes()) > 0 {
		t.Fatalf("--check made sandbox state: %v %v", err, r.sandboxes())
	}
	if calls, _ := os.ReadFile(filepath.Join(r.ctl, "docker.calls")); strings.Contains(string(calls), "network create") || strings.Contains(string(calls), "--name fyr-work-") {
		t.Fatalf("--check created a network or session:\n%s", calls)
	}
	if out, code := r.work("--check", "--ram", "1"); code != 2 || !strings.Contains(out, "--ram 1: give 2 to 64") {
		t.Fatalf("--check --ram 1: exit %d, got %q", code, out)
	}
}

// TestWorkVerbConfig pins `fyr work config [--workers N] [--ram GB] [--cpu N] [--disk GB]`:
// reading defaults, persisting a partial or full update, and refusing invalid bounds
// or unknown flags.
func TestWorkVerbConfig(t *testing.T) {
	r := newWorkRig(t)

	out, code := r.work("config")
	if want := "work config: workers=8  ram=8g  cpu=2  disk=10g\n"; code != 0 || out != want {
		t.Fatalf("config default: exit %d, got %q, want %q", code, out, want)
	}

	out, code = r.work("--workers", "12", "--ram", "16", "--cpu", "4", "--disk", "20", "config")
	if want := "work config: workers=12  ram=16g  cpu=4  disk=20g\n"; code != 0 || out != want {
		t.Fatalf("config write: exit %d, got %q, want %q", code, out, want)
	}

	out, code = r.work("config")
	if want := "work config: workers=12  ram=16g  cpu=4  disk=20g\n"; code != 0 || out != want {
		t.Fatalf("config read persisted: exit %d, got %q, want %q", code, out, want)
	}

	if out, code := r.work("--ram", "1", "config"); code != 2 || !strings.Contains(out, "--ram 1: give 2 to 64") {
		t.Fatalf("config bad ram: exit %d, got %q", code, out)
	}
	if out, code := r.work("config", "extra"); code != 2 || !strings.Contains(out, "config takes no arguments") {
		t.Fatalf("config extra arg: exit %d, got %q", code, out)
	}
	if out, code := r.work("--keep", "config"); code != 2 || !strings.Contains(out, "config takes only --workers") {
		t.Fatalf("config bad flag: exit %d, got %q", code, out)
	}
}
