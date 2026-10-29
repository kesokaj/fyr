package serve

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startServe runs Serve on a free loopback port until ctx ends, and returns
// the start lines it printed and its result channel.
func startServe(t *testing.T, ctx context.Context, o Options) (string, <-chan error) {
	t.Helper()
	pr, pw := io.Pipe()
	o.Out = pw
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, o); pw.Close() }()
	sc := bufio.NewScanner(pr)
	var lines []string
	for len(lines) < 2 && sc.Scan() {
		lines = append(lines, sc.Text())
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	if len(lines) < 2 {
		t.Fatalf("Serve printed %q, then: %v", lines, <-done)
	}
	return strings.Join(lines, "\n"), done
}

// freePort is a loopback port nothing holds at the moment.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestServeAddress(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	o.Port = freePort(t)
	var got []string
	o.Listen = func(network, addr string) (net.Listener, error) {
		got = append(got, network+" "+addr)
		return net.Listen(network, addr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	lines, done := startServe(t, ctx, o)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := "tcp 127.0.0.1:" + itoa(o.Port)
	if len(got) != 1 || got[0] != want {
		t.Errorf("listened on %v, want %q", got, want)
	}
	first, second, _ := strings.Cut(lines, "\n")
	if !regexp.MustCompile(`^fyr serve: http://127\.0\.0\.1:` + itoa(o.Port) + `/s/[A-Za-z0-9_-]{43}/\?token=[A-Za-z0-9_-]{43}$`).MatchString(first) {
		t.Errorf("start line 1: %q", first)
	}
	if second != "fyr serve: serving "+f.repo+" and "+f.home+"/.config/fyr; Ctrl-C stops it" {
		t.Errorf("start line 2: %q", second)
	}
}

func TestServeBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	o := newFix(t).options(nil)
	o.Port = ln.Addr().(*net.TCPAddr).Port
	var out strings.Builder
	o.Out = &out
	err = Serve(context.Background(), o)
	want := "serve: 127.0.0.1:" + itoa(o.Port) + " is in use; pass --port"
	if err == nil || err.Error() != want {
		t.Errorf("Serve on a held port: %v, want %q", err, want)
	}
	if out.Len() != 0 {
		t.Errorf("printed start lines on a held port: %q", out.String())
	}
}

// TestServeShutdown cancels the context with a handler in flight: Serve
// waits for it, the handler's response arrives whole, and Serve returns nil.
func TestServeShutdown(t *testing.T) {
	f := newFix(t)
	o := f.options(nil)
	o.Port = freePort(t)
	entered, release := make(chan struct{}), make(chan struct{})
	o.Render = func(w io.Writer) error {
		close(entered)
		<-release
		_, err := io.WriteString(w, "late but whole")
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	lines, done := startServe(t, ctx, o)
	url := strings.TrimPrefix(strings.SplitN(lines, "\n", 2)[0], "fyr serve: ")
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	got := make(chan string, 1)
	go func() {
		res, err := client.Get(strings.Replace(url, "/?token", "/context?token", 1))
		if err != nil {
			got <- "error: " + err.Error()
			return
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		got <- string(b)
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		t.Fatalf("Serve returned with a handler in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Errorf("Serve after shutdown: %v", err)
	}
	if body := <-got; !strings.Contains(body, "late but whole") {
		t.Errorf("the in-flight response: %q", body)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The start line names a directory, whose name can hold a terminal's escape
// bytes: they reach the terminal as '?'.
func TestStartLinePrintable(t *testing.T) {
	o := newFix(t).options(nil)
	o.Dir = filepath.Join(t.TempDir(), "evil\x1b]0;title\x07dir")
	if err := os.Mkdir(o.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	o.Port = freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	lines, done := startServe(t, ctx, o)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(lines, "\x1b\x07") || !strings.Contains(lines, "evil?]0;title?dir") {
		t.Errorf("start lines carry raw control bytes: %q", lines)
	}
}
