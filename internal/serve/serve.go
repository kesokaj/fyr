// Package serve is `fyr serve` (SPEC-fyr-serve, ADR-075): a foreground HTTP
// server on 127.0.0.1 that shows what fyr keeps for one project and for the
// machine. Every request is checked for its Host, its fetch metadata and the
// run's token before it is routed (guard.go); the pages read through the
// packages the CLI's own verbs use, and edit nothing by hand.
package serve

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// Options is everything a run needs; cmd/fyr builds it (serveOptions), so
// this package never imports cmd/fyr's printers.
type Options struct {
	Home    string    // absolute, --home resolved
	Dir     string    // the working directory at start; its checkout is the project
	Port    int       // 1–65535, checked by cmd/fyr
	Out     io.Writer // the two start lines
	Err     io.Writer // one line per handler error; nil is os.Stderr
	Version string

	// Page printers, from cmd/fyr. Each writes the verb's text; a non-nil
	// error is a failure (500), never a verb's ordinary non-zero status.
	Status func(w io.Writer) error
	Render func(w io.Writer) error
	Check  func(w io.Writer) error

	// Actions, from cmd/fyr (row 9): the two verbs a POST runs (plan D39).
	Index  func(w io.Writer) error
	Ingest func(w io.Writer) error

	Listen func(network, addr string) (net.Listener, error) // nil: net.Listen
}

// Run is one server run: its options, the two tokens drawn for it, and the
// project resolved once at start.
type Run struct {
	o       Options
	token   string
	csrf    string
	host    string     // 127.0.0.1:<port>, the only Host accepted
	cookie  string     // fyr_serve_<port>
	base    string     // /s/<P>: every page lives under it, and the cookie is scoped to it
	checks  sync.Mutex // one /check at a time: each is a full fyr check
	acting  sync.Mutex // one action at a time, across both
	root    string     // the main worktree's root; "" with no project
	noProj  string     // the no-project line, "" with a project
	errOut  io.Writer
	handler http.Handler
}

// NewRun draws the run's tokens and resolves the project; nothing is bound.
func NewRun(o Options) (*Run, error) {
	t, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	c, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	// P keeps T from other ports: a browser sends a cookie to every port of a
	// host, but only for its path, which another port's server never learns
	// (plan D48, ADR-075a).
	p, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("serve: %w", err)
	}
	r := &Run{o: o, token: t, csrf: c, errOut: o.Err, base: "/s/" + p,
		host:   "127.0.0.1:" + strconv.Itoa(o.Port),
		cookie: "fyr_serve_" + strconv.Itoa(o.Port)}
	if r.errOut == nil {
		r.errOut = os.Stderr
	}
	main, _, err := trail.Roots(o.Dir)
	switch {
	case errors.Is(err, trail.ErrNotCheckout):
		r.noProj = "no project: " + o.Dir + " is not a git checkout"
	case err != nil:
		r.noProj = "no project: " + err.Error()
	default:
		r.root = main
	}
	r.handler = http.HandlerFunc(r.serveHTTP)
	return r, nil
}

// newToken is 32 bytes from crypto/rand, base64url without padding.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (r *Run) Handler() http.Handler { return r.handler }

// Token is T, for the printed URL and tests.
func (r *Run) Token() string { return r.token }

// Base is /s/<P>, the path every page lives under.
func (r *Run) Base() string { return r.base }

// CSRF is C, for tests; every action form carries it.
func (r *Run) CSRF() string { return r.csrf }

// projectLine is the start line's and every page's project: its root, or
// why there is none.
func (r *Run) projectLine() string {
	if r.root == "" {
		return r.noProj
	}
	return r.root
}

// Serve listens on 127.0.0.1:<port>, prints the two start lines, and serves
// until ctx ends; then it stops accepting and waits for the handlers in
// flight (cmd/fyr cancels ctx on the first signal and exits 1 on the
// second). Every error it returns is the operator's line, "serve: …".
func Serve(ctx context.Context, o Options) error {
	r, err := NewRun(o)
	if err != nil {
		return err
	}
	listen := o.Listen
	if listen == nil {
		listen = net.Listen
	}
	ln, err := listen("tcp", r.host)
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("serve: %s is in use; pass --port", r.host)
	}
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	fmt.Fprintf(o.Out, "fyr serve: http://%s%s/?token=%s\n", r.host, r.base, r.token)
	// The paths are a directory's name, which can hold a terminal's escape bytes.
	fmt.Fprintln(o.Out, search.Printable(fmt.Sprintf("fyr serve: serving %s and %s/.config/fyr; Ctrl-C stops it", r.projectLine(), o.Home)))
	srv := &http.Server{Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 64 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
