package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/serve"
)

// captureServe replaces serveRun for one test: each call's options are
// kept, and fn, when set, runs in its place.
func captureServe(t *testing.T, fn func(context.Context, serve.Options) error) *[]serve.Options {
	t.Helper()
	var got []serve.Options
	old := serveRun
	serveRun = func(ctx context.Context, o serve.Options) error {
		got = append(got, o)
		if fn != nil {
			return fn(ctx, o)
		}
		return nil
	}
	t.Cleanup(func() { serveRun = old })
	return &got
}

// The closures serveOptions builds: an ordinary non-zero status (nothing
// installed, findings, a refusal) is the page, never an error.
func TestServeOptionsMapExit(t *testing.T) {
	harnessTree, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	got := captureServe(t, nil)
	checkRepo(t, map[string]string{"README.md": "x\n`fyr frobnicate`\n"})
	if err := run([]string{"serve", "--home", home, "--repo", harnessTree}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 {
		t.Fatalf("serveRun called %d times", len(*got))
	}
	o := (*got)[0]
	if o.Port != 3000 || o.Home != home {
		t.Errorf("options: port %d, home %q", o.Port, o.Home)
	}
	var st bytes.Buffer
	if err := o.Status(&st); err != nil || !strings.Contains(st.String(), "installed  nothing") {
		t.Errorf("Status on an uninstalled home: %v\n%s", err, st.String())
	}
	var ck bytes.Buffer
	if err := o.Check(&ck); err != nil || !strings.Contains(ck.String(), "fyr has no verb frobnicate") || !strings.Contains(ck.String(), "check: 1 findings") {
		t.Errorf("Check with a finding: %v\n%s", err, ck.String())
	}
	var ctxt bytes.Buffer
	if err := o.Render(&ctxt); err != nil || ctxt.Len() == 0 {
		t.Errorf("Render: %v, %d bytes", err, ctxt.Len())
	}

	// Outside a checkout: the refusal's reason on the writer, nil.
	t.Chdir(t.TempDir())
	*got = nil
	if err := run([]string{"serve", "--home", home, "--repo", harnessTree}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	ck.Reset()
	if err := (*got)[0].Check(&ck); err != nil || !strings.Contains(ck.String(), "check: ") || !strings.Contains(ck.String(), "is not a git checkout") {
		t.Errorf("Check outside a checkout: %v\n%s", err, ck.String())
	}

	// Another error stays an error.
	if err := pageErr(errors.New("disk")); err == nil || pageErr(exitCode(1)) != nil {
		t.Error("pageErr maps the wrong errors")
	}
}

// The two actions serveOptions builds are the bare verbs: the index of the
// checkout serve started in, and an ingest whose failed source, exit 1 at
// the terminal, is the action's error.
func TestServeActions(t *testing.T) {
	harnessTree, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	got := captureServe(t, nil)
	repo := checkRepo(t, map[string]string{"README.md": "x\n"})
	if err := run([]string{"serve", "--home", home, "--repo", harnessTree}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	o := (*got)[0]
	var ix bytes.Buffer
	if err := o.Index(&ix); err != nil || !strings.Contains(ix.String(), "files    ") {
		t.Errorf("Index: %v\n%s", err, ix.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents", "scratch", "search.db")); err != nil {
		t.Errorf("Index wrote no index in the checkout: %v", err)
	}
	var in bytes.Buffer
	if err := o.Ingest(&in); err != nil || !strings.Contains(in.String(), "0 sources failed") {
		t.Errorf("Ingest on an empty home: %v\n%s", err, in.String())
	}
	if _, err := os.Stat(filepath.Join(home, harness.StateDir, "metrics.db")); err != nil {
		t.Errorf("Ingest opened no store in the home: %v", err)
	}

	// A source that is not a database fails; the verb exits 1 on the same.
	bad := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a database\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in.Reset()
	if err := o.Ingest(&in); err == nil || err.Error() != "1 source(s) failed" || !strings.Contains(in.String(), "1 sources failed") {
		t.Errorf("Ingest with a failed source: %v\n%s", err, in.String())
	}
	var out bytes.Buffer
	if err := run([]string{"metrics", "ingest", "--home", home}, &out); exitOf(err) != 1 || !strings.Contains(out.String(), "1 sources failed") {
		t.Errorf("metrics ingest with a failed source: exit %d\n%s", exitOf(err), out.String())
	}
}

func TestServePortRange(t *testing.T) {
	got := captureServe(t, nil)
	for _, p := range []string{"0", "70000", "-1", "x"} {
		var out bytes.Buffer
		err := run([]string{"serve", "--home", t.TempDir(), "--port", p}, &out)
		if exitOf(err) != 2 {
			t.Errorf("--port %s: exit %d, want 2\n%s", p, exitOf(err), out.String())
		}
	}
	if len(*got) != 0 {
		t.Errorf("serveRun was called for a bad port")
	}
	var out bytes.Buffer
	if err := run([]string{"serve", "--home", t.TempDir(), "extra"}, &out); exitOf(err) != 2 || !strings.Contains(out.String(), "serve takes no arguments") {
		t.Errorf("a positional argument: exit %d\n%s", exitOf(err), out.String())
	}
}

// A serve error is the operator's line on stdout and exit 1.
func TestServeErrorLine(t *testing.T) {
	captureServe(t, func(context.Context, serve.Options) error {
		return errors.New("serve: 127.0.0.1:3000 is in use; pass --port")
	})
	var out bytes.Buffer
	err := run([]string{"serve", "--home", t.TempDir()}, &out)
	if exitOf(err) != 1 || out.String() != "serve: 127.0.0.1:3000 is in use; pass --port\n" {
		t.Errorf("exit %d, out %q", exitOf(err), out.String())
	}
}

// The first signal cancels the run; a second while it shuts down exits 1.
func TestServeSecondSignal(t *testing.T) {
	var mu sync.Mutex
	var sigs chan<- os.Signal
	oldNotify, oldExit := serveNotify, serveExit
	serveNotify = func(ch chan<- os.Signal) { mu.Lock(); sigs = ch; mu.Unlock() }
	exited := make(chan int, 1)
	serveExit = func(code int) { exited <- code }
	t.Cleanup(func() { serveNotify, serveExit = oldNotify, oldExit })

	cancelled, stuck := make(chan struct{}), make(chan struct{})
	captureServe(t, func(ctx context.Context, _ serve.Options) error {
		<-ctx.Done()
		close(cancelled)
		<-stuck // a shutdown that waits on a handler
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- run([]string{"serve", "--home", t.TempDir()}, &bytes.Buffer{}) }()
	var ch chan<- os.Signal
	for deadline := time.Now().Add(5 * time.Second); ch == nil && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		mu.Lock()
		ch = sigs
		mu.Unlock()
	}
	if ch == nil {
		t.Fatal("runServe never subscribed to signals")
	}
	ch <- os.Interrupt
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the first signal did not cancel the run")
	}
	select {
	case code := <-exited:
		t.Fatalf("exited %d on the first signal", code)
	default:
	}
	ch <- os.Interrupt
	select {
	case code := <-exited:
		if code != 1 {
			t.Errorf("second signal: exit %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second signal did not exit")
	}
	close(stuck)
	if err := <-done; err != nil {
		t.Errorf("run: %v", err)
	}
}
