package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeDocker stands in for the docker CLI: it logs each call, and `run`
// writes the cid file and runs the rig's agy with exactly the container's
// -e environment in the -w directory, as a container would. Control files
// in @CTL@ make each verb fail, hang or list containers.
const fakeDocker = `#!/bin/bash
CTL=@CTL@; REC=@REC@
printf '%s\n' "$*" >> "$REC/docker.calls"
case "$1" in
run)
  shift; envs=(); cid=; wd=
  while [ $# -gt 0 ]; do
    case "$1" in
      -e) envs+=("$2"); shift 2 ;;
      --cidfile) cid=$2; shift 2 ;;
      -w) wd=$2; shift 2 ;;
      --read-only|--init) shift ;;
      -*) shift 2 ;;
      *) break ;;
    esac
  done
  shift 2 # the image, then the container's agy path
  env > "$REC/client.env"
  printf '%s\n' "${envs[@]}" > "$REC/container.env"
  for kv in "${envs[@]}"; do case "$kv" in HOME=*) readlink "${kv#HOME=}/.gemini/antigravity-cli/antigravity-oauth-token" > "$REC/link.target" ;; esac; done
  [ -e "$CTL/nocid" ] || echo cid123 > "$cid"
  if [ -e "$CTL/exitcode" ]; then echo "docker: Error response from daemon" >&2; exit "$(cat "$CTL/exitcode")"; fi
  if [ -e "$CTL/hang" ]; then echo $$ > "$REC/run.pid"; exec sleep 300; fi
  cd "$wd" && exec env -i "${envs[@]}" "$(cat "$CTL/agy")" "$@" ;;
kill|stop)
  if [ -e "$CTL/$1-fail" ]; then echo "no such container" >&2; exit 1; fi
  [ -e "$REC/run.pid" ] && kill "$(cat "$REC/run.pid")" ;;
ps)
  if [ -e "$CTL/ps-fail" ]; then echo "Cannot connect to the Docker daemon" >&2; exit 1; fi
  n=$(( $(cat "$REC/ps.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$REC/ps.n"
  # Real docker filters by label: a call without this worker's filters
  # lists nothing of it.
  case "$*" in *"--filter label=fyr.worker=$(cat "$CTL/worker" 2>/dev/null)"*) ;; *) exit 0 ;; esac
  if [ "$n" -gt 1 ] && [ -e "$CTL/ps.after" ]; then cat "$CTL/ps.after"; elif [ "$n" -eq 1 ] && [ -e "$CTL/ps" ]; then cat "$CTL/ps"; fi ;;
rm)
  if [ -e "$CTL/rm-fail" ]; then echo "removal failed" >&2; exit 1; fi ;;
esac
`

// dockerRig is a rig whose PATH finds the fake docker first.
type dockerRig struct {
	*rig
	ctl string
}

func newDockerRig(t *testing.T) *dockerRig {
	r := newRig(t)
	d := &dockerRig{rig: r, ctl: filepath.Join(r.tmp, "ctl")}
	if err := os.MkdirAll(d.ctl, 0o755); err != nil {
		t.Fatal(err)
	}
	script := strings.NewReplacer("@CTL@", ShellQuote(d.ctl), "@REC@", ShellQuote(r.rec)).Replace(fakeDocker)
	put(t, filepath.Join(r.bin, "docker"), script, 0o755)
	put(t, filepath.Join(d.ctl, "agy"), r.agy, 0o644)
	put(t, filepath.Join(d.ctl, "worker"), "w1", 0o644)
	t.Setenv("PATH", r.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return d
}

func (d *dockerRig) set(name, content string) { put(d.t, filepath.Join(d.ctl, name), content, 0o644) }

// config is a docker-mode Box config: the worker directory sits in a stamp
// directory, as fyr work makes it, so the cid file has a parent outside it.
func (d *dockerRig) config(name string) Config {
	c := d.rig.config(name)
	c.Dir = filepath.Join(d.tmp, "work", "stamp1", name)
	c.Docker = &Docker{Image: "fyr-work:local", Agy: d.agy, Stamp: "stamp1", Name: name, Network: RunNetwork("stamp1")}
	return c
}

func (d *dockerRig) calls() []string {
	return strings.Split(strings.TrimSpace(d.read("docker.calls")), "\n")
}

// The argv is the plan's, flag for flag: every hardening flag and limit,
// the run's network, the four mounts and nothing else of the real home,
// the environment as -e pairs with the sandbox's bin first on PATH, then
// the image and agy's own args.
func TestDockerArgs(t *testing.T) {
	d := &Docker{Image: "fyr-work:local", Agy: "/opt/agy", Stamp: "s1", Name: "w1", Network: "fyr-work-s1"}
	got, err := dockerArgs(d, "/h/w/s1/w1/app", "/h/w/s1/w1/home", "/real/.gemini/tok", "/h/w/s1/w1.cid", 1000, 1001,
		[]string{"PATH=/h/w/s1/w1/home/bin:" + ImagePath, "HOME=/h/w/s1/w1/home"}, []string{"-p", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run",
		"--name", "fyr-work-s1-w1", "--label", "fyr.work=s1", "--label", "fyr.worker=w1",
		"--cidfile", "/h/w/s1/w1.cid", "--pull", "never", "--user", "1000:1001",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--init",
		"--memory", "8g", "--memory-swap", "8g", "--cpus", "2", "--pids-limit", "1024",
		"--network", "fyr-work-s1",
		"--read-only", "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=2g,mode=1777",
		"--mount", "type=bind,src=/h/w/s1/w1/app,dst=/h/w/s1/w1/app",
		"--mount", "type=bind,src=/h/w/s1/w1/home,dst=/h/w/s1/w1/home",
		"--mount", "type=bind,src=/real/.gemini/tok,dst=/real/.gemini/tok",
		"--mount", "type=bind,src=/opt/agy,dst=/usr/local/bin/agy,readonly",
		"-w", "/h/w/s1/w1/app",
		"-e", "PATH=/h/w/s1/w1/home/bin:" + ImagePath, "-e", "HOME=/h/w/s1/w1/home",
		"fyr-work:local", "/usr/local/bin/agy", "-p", "hi"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	networks := 0
	for _, a := range got {
		if strings.Contains(a, "docker.sock") || a == "--privileged" || strings.HasPrefix(a, "--net=") || strings.HasPrefix(a, "--network=") || a == "-v" {
			t.Fatalf("argv holds %q", a)
		}
		if a == "--network" {
			networks++
		}
	}
	if networks != 1 {
		t.Fatalf("argv names %d networks, want the run's one", networks)
	}
}

// A mount path that --mount's syntax would split or misread is refused, as
// is a relative one; so are an image ref docker would read as a flag or
// split, and an environment entry docker would fill from the host.
func TestDockerArgsRefuses(t *testing.T) {
	for _, bad := range []string{"/h/a,b", `/h/a"b`, "/h/a\nb", "rel/dir"} {
		d := &Docker{Image: "i", Agy: "/opt/agy", Stamp: "s", Name: "n", Network: "fyr-work-s"}
		if _, err := dockerArgs(d, bad, "/h/ok/home", "/t", "/c", 1, 1, nil, nil); err == nil {
			t.Errorf("app %q: no refusal", bad)
		}
		if _, err := dockerArgs(d, "/h/ok/app", bad, "/t", "/c", 1, 1, nil, nil); err == nil {
			t.Errorf("home %q: no refusal", bad)
		}
		d.Agy = bad
		if _, err := dockerArgs(d, "/h/ok/app", "/h/ok/home", "/t", "/c", 1, 1, nil, nil); err == nil {
			t.Errorf("agy %q: no refusal", bad)
		}
	}
	for _, img := range []string{"", "-v", "--privileged", "a b", "a\tb"} {
		d := &Docker{Image: img, Agy: "/opt/agy", Stamp: "s", Name: "n", Network: "fyr-work-s"}
		if _, err := dockerArgs(d, "/h/ok/app", "/h/ok/home", "/t", "/c", 1, 1, nil, nil); err == nil {
			t.Errorf("image %q: no refusal", img)
		}
	}
	d := &Docker{Image: "i", Agy: "/opt/agy", Stamp: "s", Name: "n", Network: "fyr-work-s"}
	for _, kv := range []string{"GH_TOKEN", "=x"} {
		if _, err := dockerArgs(d, "/h/ok/app", "/h/ok/home", "/t", "/c", 1, 1, []string{kv}, nil); err == nil {
			t.Errorf("env %q: no refusal", kv)
		}
	}
	// No run network is never docker's default bridge (PLAN.md item 13).
	for _, net := range []string{"", "bridge", "host", "default", "none", "-x", "a b", "a\nb", "container:x"} {
		d := &Docker{Image: "i", Agy: "/opt/agy", Stamp: "s", Name: "n", Network: net}
		if _, err := dockerArgs(d, "/h/ok/app", "/h/ok/home", "/t", "/c", 1, 1, nil, nil); err == nil {
			t.Errorf("network %q: no refusal", net)
		}
	}
}

// A nil Docker is today's host session, field for field: eval's argv,
// environment, directory and process group do not move.
func TestHostCmdUnchanged(t *testing.T) {
	r := newRig(t)
	b := r.prepared(r.config("host"))
	cmd := b.hostCmd("/bin/agy", "hello")
	wantArgs := []string{"/bin/agy", "-p", "The project root is the current directory, " + b.App() + "; ignore any other checkout. This is a print-mode run (`agy -p`): the turn's end is the run's end. hello", "--mode", "accept-edits", "--print-timeout", "20m"}
	if !slices.Equal(cmd.Args, wantArgs) || cmd.Dir != b.App() || !slices.Equal(cmd.Env, b.agyEnv()) || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("host cmd: args %q dir %q setpgid %v", cmd.Args, cmd.Dir, cmd.SysProcAttr)
	}
	if !strings.HasPrefix(cmd.Env[0], "PATH="+filepath.Join(b.Home(), "bin")+string(os.PathListSeparator)) {
		t.Fatalf("host PATH %q", cmd.Env[0])
	}
}

// The wrapper is today's bytes with no Docker, and in docker mode execs the
// image's fyr with the same --home pin.
func TestWrapperByMode(t *testing.T) {
	d := newDockerRig(t)
	host := d.prepared(d.rig.config("host"))
	wrapper := func(b *Box) string {
		w, err := os.ReadFile(filepath.Join(b.Home(), "bin", "fyr"))
		if err != nil {
			t.Fatal(err)
		}
		return string(w)
	}
	want := "#!/usr/bin/env bash\nfor a in \"$@\"; do\n  case \"$a\" in --home|--home=*|-home|-home=*) echo \"fyr: this sandbox pins --home to \"" + ShellQuote(host.Home()) + " >&2; exit 2 ;; esac\ndone\nexec " + ShellQuote(filepath.Join(host.Home(), ".local/bin/fyr")) + " \"$1\" --home " + ShellQuote(host.Home()) + " \"${@:2}\"\n"
	if got := wrapper(host); got != want {
		t.Fatalf("host wrapper\n got %q\nwant %q", got, want)
	}
	box := d.prepared(d.config("dock"))
	if got := wrapper(box); !strings.Contains(got, "exec '/usr/local/bin/fyr' \"$1\" --home "+ShellQuote(box.Home())) {
		t.Fatalf("docker wrapper %q", got)
	}
}

// One session in a container: the client runs with the real environment
// minus DOCKER_DEFAULT_PLATFORM (DockerEnv), the container gets the
// sandbox's PATH first, Go's paths in the home and no host PATH; agy ran in
// the clone; the cid file sits outside the worker directory; the exited
// container is listed, removed and passes check e.
func TestDockerSession(t *testing.T) {
	t.Setenv("DOCKER_DEFAULT_PLATFORM", "linux/s390x")
	d := newDockerRig(t)
	d.set("ps", "cid123 exited\n")
	b := d.prepared(d.config("w1"))
	if rc := b.Session(context.Background(), "agy-unused", filepath.Join(d.real, TokenRel), "hello", "out.txt"); rc != 0 || !b.Started() {
		t.Fatalf("rc %d started %v\n%s\n%s", rc, b.Started(), d.out.String(), d.read("docker.calls"))
	}
	// The reaped client's group is not in step 14's sweep.
	if len(b.pgids) != 0 {
		t.Fatalf("pgids %v", b.pgids)
	}
	b.After()
	b.ExitChecks()
	if len(b.Reasons()) > 0 {
		t.Fatalf("reasons %v\n%s", b.Reasons(), d.out.String())
	}
	if strings.Contains(d.read("client.env"), "HOME="+b.Home()+"\n") || !strings.Contains(d.read("client.env"), "HOME="+d.real+"\n") {
		t.Fatal("the docker client did not run with the real HOME")
	}
	if strings.Contains(d.read("client.env"), "DOCKER_DEFAULT_PLATFORM=") {
		t.Fatal("the docker client asks for a platform: DOCKER_DEFAULT_PLATFORM reached it")
	}
	cenv := strings.Split(strings.TrimSpace(d.read("container.env")), "\n")
	if cenv[0] != "PATH="+filepath.Join(b.Home(), "bin")+":"+ImagePath {
		t.Fatalf("container PATH %q", cenv[0])
	}
	for _, kv := range []string{"HOME=" + b.Home(), "GOPATH=" + filepath.Join(b.Home(), "go"), "GOTOOLCHAIN=local", "GODEBUG=installgoroot=all"} {
		if !slices.Contains(cenv, kv) {
			t.Fatalf("container env lacks %q: %q", kv, cenv)
		}
	}
	if got := strings.TrimSpace(d.read("agy.cwd.1")); got != b.App() {
		t.Fatalf("agy ran in %q", got)
	}
	cid := filepath.Join(filepath.Dir(b.Dir()), "w1.cid")
	if _, err := os.Stat(cid); err == nil {
		t.Fatal("the cid file was left beside the worker directory")
	}
	calls := d.calls()
	// The cid file is written outside the worker directory, the one tree
	// the session can change.
	if !strings.HasPrefix(calls[0], "run --name fyr-work-stamp1-w1 ") || !strings.Contains(calls[0], " --cidfile "+cid+" ") ||
		strings.HasPrefix(cid, b.Dir()+string(filepath.Separator)) || !slices.Contains(calls, "rm -f cid123") {
		t.Fatalf("calls %q", calls)
	}
	// The token mount's target is where the box's link points: inside the
	// container the link resolves.
	target := strings.TrimSpace(d.read("link.target"))
	if target == "" || !strings.Contains(calls[0], "--mount type=bind,src="+target+",dst="+target+" ") {
		t.Fatalf("token link %q, mount: %q", target, calls[0])
	}
}

// docker's own errors (125-127, here after the container was created) and a
// container that never wrote its cid read as agy not started.
func TestDockerNotStarted(t *testing.T) {
	for ctl, v := range map[string]string{"exit125": "125", "exit126": "126", "exit127": "127", "nocid": ""} {
		t.Run(ctl, func(t *testing.T) {
			d := newDockerRig(t)
			if v != "" {
				d.set("exitcode", v)
			} else {
				d.set("nocid", "")
			}
			b := d.prepared(d.config("w1"))
			if rc := b.Session(context.Background(), "agy-unused", filepath.Join(d.real, TokenRel), "hello", "out.txt"); rc != -1 || b.Started() {
				t.Fatalf("rc %d started %v", rc, b.Started())
			}
		})
	}
}

// At the timeout the container is killed; when that kill fails, the
// client's group is killed after StopGrace, so the wait ends either way.
func TestDockerTimeout(t *testing.T) {
	for _, killFails := range []bool{false, true} {
		d := newDockerRig(t)
		d.set("hang", "")
		if killFails {
			d.set("kill-fail", "")
		}
		c := d.config("w1")
		c.Timeout = 300 * time.Millisecond
		b := d.prepared(c)
		done := make(chan int, 1)
		go func() {
			done <- b.Session(context.Background(), "agy-unused", filepath.Join(d.real, TokenRel), "x", "out.txt")
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("kill fails %v: the wait did not end", killFails)
		}
		if !b.TimedOut() || !slices.Contains(d.calls(), "kill fyr-work-stamp1-w1") {
			t.Fatalf("kill fails %v: timed out %v, calls %q", killFails, b.TimedOut(), d.calls())
		}
	}
}

// At the context's end the container is stopped with StopGrace rounded up
// to a whole second, at least 1, then the client's group gets SIGTERM: a
// failed stop still ends the wait well before the SIGKILL StopGrace later.
func TestDockerCancel(t *testing.T) {
	for grace, want := range map[time.Duration]string{100 * time.Millisecond: "1", 1500 * time.Millisecond: "2"} {
		dockerCancel(t, grace, want, false)
	}
	dockerCancel(t, 3*time.Second, "3", true)
}

func dockerCancel(t *testing.T, grace time.Duration, want string, stopFails bool) {
	d := newDockerRig(t)
	d.set("hang", "")
	if stopFails {
		d.set("stop-fail", "")
	}
	c := d.config("w1")
	c.StopGrace = grace
	b := d.prepared(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- b.Session(ctx, "agy-unused", filepath.Join(d.real, TokenRel), "x", "out.txt") }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(d.read("docker.calls"), "run ") || d.read("run.pid") == "" {
		if time.Now().After(deadline) {
			t.Fatal("the fake container never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	t0 := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the wait did not end")
	}
	if stopFails && time.Since(t0) >= grace {
		t.Fatalf("with a failed stop the wait took %s: the group's SIGTERM did not end it", time.Since(t0))
	}
	if !slices.Contains(d.calls(), "stop -t "+want+" fyr-work-stamp1-w1") {
		t.Fatalf("calls %q", d.calls())
	}
}

// Check e's container half fails closed: a container left running or
// created, one rm could not remove, a failing ps or rm each fail the box
// processes left; an exited one passes and is removed.
func TestContainersLeft(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctl  map[string]string
		fail bool
	}{
		{"exited", map[string]string{"ps": "c1 exited\n"}, false},
		{"dead", map[string]string{"ps": "c1 dead\n"}, false},
		{"none", map[string]string{}, false},
		{"running", map[string]string{"ps": "c1 running\n"}, true},
		{"created", map[string]string{"ps": "c1 created\n"}, true},
		{"still listed", map[string]string{"ps": "c1 exited\n", "ps.after": "c1 exited\n"}, true},
		{"ps fails", map[string]string{"ps-fail": ""}, true},
		{"rm fails", map[string]string{"ps": "c1 exited\n", "rm-fail": ""}, true},
		{"unreadable", map[string]string{"ps": "garbage\n"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDockerRig(t)
			b := d.prepared(d.config("w1"))
			for k, v := range tc.ctl {
				d.set(k, v)
			}
			b.containersLeft()
			if got := b.Has(ReasonProcs); got != tc.fail {
				t.Fatalf("processes left %v, want %v\n%s", got, tc.fail, d.out.String())
			}
			if strings.Contains(tc.ctl["ps"], "c1") && tc.ctl["rm-fail"] == "" && !slices.Contains(d.calls(), "rm -f c1") {
				t.Fatalf("not removed: %q", d.calls())
			}
			if tc.ctl["ps-fail"] == "" && !slices.Contains(d.calls(), "ps -a --filter label=fyr.work=stamp1 --filter label=fyr.worker=w1 --format {{.ID}} {{.State}}") {
				t.Fatalf("not listed by both labels: %q", d.calls())
			}
		})
	}
}

// The token is mounted read-write, so only the regular file the login
// wrote: a link or a directory in its place is refused before docker runs.
// Checked when the session starts, not when docker mounts it: a host
// process swapping the file in between is the operator's own (D109).
func TestDockerTokenNotRegular(t *testing.T) {
	d := newDockerRig(t)
	b := d.prepared(d.config("w1"))
	tok := filepath.Join(d.real, TokenRel)
	other := filepath.Join(d.tmp, "elsewhere")
	put(t, other, "x", 0o600)
	if err := os.Remove(tok); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, tok); err != nil {
		t.Fatal(err)
	}
	if rc := b.Session(context.Background(), "agy-unused", tok, "hello", "out.txt"); rc != -1 || !b.Has(ReasonToken) {
		t.Fatalf("rc %d reasons %v", rc, b.Reasons())
	}
	// A session that never started a client asks docker nothing, not even
	// for the container check, and step 14 asks nothing either.
	b.After()
	b.ExitChecks()
	if calls := d.read("docker.calls"); calls != "" {
		t.Fatalf("docker was called: %q", calls)
	}
}

// The session runs the container half at its end, before After unlinks the
// token: a container left running fails the box processes left by then.
func TestSessionListsContainers(t *testing.T) {
	d := newDockerRig(t)
	d.set("ps", "cid123 running\n")
	b := d.prepared(d.config("w1"))
	if rc := b.Session(context.Background(), "agy-unused", filepath.Join(d.real, TokenRel), "hello", "out.txt"); rc != 0 {
		t.Fatalf("rc %d", rc)
	}
	if !b.Has(ReasonProcs) {
		t.Fatalf("reasons %v\n%s", b.Reasons(), d.out.String())
	}
}
