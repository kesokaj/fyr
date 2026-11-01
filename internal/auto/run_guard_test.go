package auto

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

const observeConfig = `{"enabled": true, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`

func bugProject(t *testing.T, lines string) string {
	t.Helper()
	root := idleProject(t)
	writeTree(t, root, ".agents/trail.jsonl", lines)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "a bug")
	git(t, root, "push", "-q")
	backdate(t, root)
	return root
}

func TestGatePartialCloneIsNotRead(t *testing.T) {
	readable = func(string) bool { return false }
	t.Cleanup(func() { readable = trail.Readable })
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	e := &fakeEnv{targets: targetsOf(idleProject(t))}
	e.onConv = func() { t.Error("a git-backed gate ran") }
	if out := reportRun(t, home, e); !strings.Contains(out, "partial clone") {
		t.Errorf("%s", out)
	}
}

// `readable` runs git, so a hostile config can hang it as it can any read.
func TestGateHungReadableIsLeftBehind(t *testing.T) {
	release := make(chan struct{})
	readable = func(string) bool { <-release; return true }
	t.Cleanup(func() { close(release) })
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env, o.ReadLimit = &fakeEnv{targets: targetsOf(idleProject(t))}, 200*time.Millisecond
	if exit(Run(o, false)) != 1 || !strings.Contains(out.String(), "a read took over") {
		t.Errorf("%s", out.String())
	}
}

func TestGateHungTargetsIsLeftBehind(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	e := &fakeEnv{onTargets: func() { <-release }}
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env, o.ReadLimit = e, 200*time.Millisecond
	if exit(Run(o, false)) != 1 || !strings.Contains(out.String(), "G6 registered: a read took over") {
		t.Errorf("%s", out.String())
	}
}

// off during the install check, past the re-read before it.
func TestGateRereadRightBeforeUsage(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	e := &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")}
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = e
	o.Install = func() []Finding {
		writeConfig(t, home, `{"enabled": false, "level": "observe", "project_idle": "6h", "user_idle": "15m"}`)
		return nil
	}
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	if e.usageCalls != 0 || !strings.Contains(out.String(), "G1 switch: off, stopped before the quota") {
		t.Errorf("%d calls: %s", e.usageCalls, out.String())
	}
}

// A read that hangs is left behind: the run goes on and exits 1.
func TestGateHungReadIsLeftBehind(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	first, second := idleProject(t), idleProject(t)
	var calls atomic.Int32
	e := &fakeEnv{targets: targetsOf(first, second), usage: usageText("90%", "90%")}
	e.onConv = func() {
		if calls.Add(1) == 1 {
			<-release
		}
	}
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env, o.ReadLimit = e, 200*time.Millisecond
	if exit(Run(o, false)) != 1 {
		t.Errorf("%s", out.String())
	}
	if !strings.Contains(out.String(), first+": G7 idle: a read took over") || !strings.Contains(out.String(), second+": health:") {
		t.Errorf("%s", out.String())
	}
}

func TestChecksHungInstallIsAFinding(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env, o.ReadLimit = &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")}, 200*time.Millisecond
	o.Install = func() []Finding { <-release; return nil }
	if exit(Run(o, false)) != 1 {
		t.Errorf("%s", out.String())
	}
	if got := reports(t, home); len(got) != 1 || !strings.Contains(got[0].Body, "status/failed") {
		t.Errorf("%+v", got)
	}
}

func TestReportIsRedacted(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	secret := "ghp_" + strings.Repeat("a1", 18)
	e := &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")}
	var out bytes.Buffer
	o := runOpts(home, &out)
	o.Env = e
	o.Install = func() []Finding { return []Finding{{"status", "x", "fyr", "a line with " + secret}} }
	if err := Run(o, false); err != nil {
		t.Fatal(err)
	}
	got := reports(t, home)
	if len(got) != 1 || strings.Contains(got[0].Body, secret) {
		t.Errorf("%+v", got)
	}
}

func TestReportOneLongLineKeepsTheReport(t *testing.T) {
	fs := []Finding{{"check", "path", "x", strings.Repeat("y", 1<<20)}, {"check", "path", "z", "short"}}
	body := reportBody("p", "observe", fs)
	if len(body) > 4*lineLimit || !strings.Contains(body, "short") {
		t.Errorf("%d bytes", len(body))
	}
}

// off written while the only project is read: no `/usage`, no machine checks.
func TestGateRereadBeforeUsage(t *testing.T) {
	for _, level := range []string{"observe", "project"} {
		t.Run(level, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, strings.ReplaceAll(observeConfig, "observe", level))
			e := &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")}
			e.onConv = func() {
				writeConfig(t, home, `{"enabled": false, "level": "project", "project_idle": "6h", "user_idle": "15m"}`)
			}
			installs := 0
			var out bytes.Buffer
			o := runOpts(home, &out)
			o.Env = e
			o.Install = func() []Finding { installs++; return nil }
			if err := Run(o, false); err != nil {
				t.Fatal(err)
			}
			if e.usageCalls != 0 || installs != 0 || !strings.Contains(out.String(), "G1 switch: off, stopped before") {
				t.Errorf("%d calls, %d installs: %s", e.usageCalls, installs, out.String())
			}
		})
	}
}

// A stop before the second project keeps the first's mark: three such runs
// file one report.
func TestReportMarkSurvivesAStop(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	first := bugProject(t, trailLine(t, "bug", "BUG-001", "open", "no rule"))
	second := idleProject(t)
	for range 3 {
		e := &fakeEnv{targets: targetsOf(first, second)}
		e.onConv = func() { e.ptys = []time.Time{runAt} }
		reportRun(t, home, e)
	}
	if got := reports(t, home); len(got) != 1 {
		t.Errorf("%d reports", len(got))
	}
	if st, _, _ := loadState(home); st.Projects[first].Mark.Report == "" {
		t.Errorf("%+v", st)
	}
}

func TestReportGoneProjectLeavesTheState(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	root := idleProject(t)
	reportRun(t, home, &fakeEnv{targets: targetsOf(root), usage: usageText("90%", "90%")})
	reportRun(t, home, &fakeEnv{usage: usageText("90%", "90%")})
	if st, _, _ := loadState(home); len(st.Projects) != 0 {
		t.Errorf("%+v", st.Projects)
	}
}

func TestChecksQueuedProposal(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, observeConfig)
	add := func(title string, at time.Time) {
		o := brain.Options{Home: home, Global: true, Now: func() time.Time { return at }}
		if _, err := brain.Add(o, brain.AddInput{Kind: "proposal", Title: title, Status: "queued", Body: strings.NewReader("x")}); err != nil {
			t.Fatal(err)
		}
	}
	add("old one", runAt.Add(-31*24*time.Hour))
	add("fresh one", runAt.Add(-29*24*time.Hour))
	reportRun(t, home, &fakeEnv{targets: targetsOf(idleProject(t)), usage: usageText("90%", "90%")})
	got := reports(t, home)
	if len(got) != 1 || strings.Count(got[0].Body, "proposal-queued") != 1 || !strings.Contains(got[0].Body, "old-one") {
		t.Errorf("%+v", got)
	}
}

func fakeTool(t *testing.T, name, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestGateToolKillsItsGroupAtTheDeadline(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	fakeTool(t, "hang", "sleep 60 &\necho $! > "+pidFile+"\nwait\n")
	start := time.Now()
	if _, err := tool(300*time.Millisecond, "", nil, "hang"); err == nil {
		t.Fatal("no error")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the tool outlived its deadline")
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	for i := 0; i < 100 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if out, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output(); len(out) > 0 && !strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
		t.Errorf("the child survived: %s", out)
	}
}

func TestGateToolRefusesOutputPastItsLimit(t *testing.T) {
	fakeTool(t, "loud", "head -c 5000000 /dev/zero\n")
	if _, err := tool(10*time.Second, "", nil, "loud"); err == nil {
		t.Error("no error")
	}
}

func TestGateRealUsageRunsInDirWithHome(t *testing.T) {
	fakeTool(t, "agy", `printf '%s\n%s\n%s\n' "$PWD" "$HOME" "$*"`+"\n")
	home, work := t.TempDir(), t.TempDir()
	got, err := machine{}.Usage(home, work)
	want := work + "\n" + home + "\n-p /usage\n"
	if err != nil || got != want {
		t.Errorf("%q, %v", got, err)
	}
}

func TestGateRealSessionsFiltersByUser(t *testing.T) {
	fakeTool(t, "loginctl", `case "$1" in
list-sessions) echo " 7 1000 a - x user - no -"; echo " 8 1001 b"; echo "../bad 1";;
show-session) if [ "$2" = 7 ]; then printf 'User=1000\nType=wayland\n'; else printf 'User=1001\nType=x11\n'; fi;;
esac
`)
	got, err := machine{}.Sessions(1000)
	if err != nil || len(got) != 1 || got[0]["Type"] != "wayland" {
		t.Errorf("%v, %v", got, err)
	}
}
