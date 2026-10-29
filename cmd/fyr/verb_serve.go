package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/serve"
)

// serve is registered here, not in the verbs literal, as check is: its Check
// closure reaches vocabulary(), which reads verbs (an initialization cycle).
func init() {
	verbs["serve"] = verb{flags: func(c *ctx) {
		c.fs.IntVar(&c.port, "port", 3000, "the port `N` on 127.0.0.1, 1-65535 (default 3000)")
	}, source: true, run: runServe}
}

// serveRun, serveExit and serveNotify are what runServe calls to serve, to
// exit on a second signal and to subscribe to signals; tests replace them.
var (
	serveRun    = serve.Serve
	serveExit   = os.Exit
	serveNotify = func(ch chan<- os.Signal) { signal.Notify(ch, os.Interrupt, syscall.SIGTERM) }
)

// runServe serves `fyr serve` (SPEC-fyr-serve, ADR-075) in the foreground:
// the first SIGINT or SIGTERM stops it after the handlers in flight, a
// second exits 1 at once. Its errors are the operator's line on stdout,
// exit 1.
func runServe(c *ctx) error {
	if c.port < 1 || c.port > 65535 {
		return usageErr(c.out, "serve", fmt.Sprintf("fyr serve: --port %d is outside 1-65535", c.port))
	}
	o, err := serveOptions(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 2)
	serveNotify(sigs)
	defer signal.Stop(sigs)
	go watchSignals(sigs, cancel)
	if err := serveRun(ctx, o); err != nil {
		fmt.Fprintln(c.out, search.Printable(err.Error()))
		return exitCode(1)
	}
	return nil
}

// watchSignals cancels on the first signal and exits 1 on the second, read
// from one channel so the second is seen (signal.NotifyContext would hide
// it).
func watchSignals(sigs <-chan os.Signal, cancel func()) {
	<-sigs
	cancel()
	<-sigs
	serveExit(1)
}

// serveOptions builds the run's options: the home, the working directory at
// start, and cmd/fyr's printers, whose ordinary exit statuses (nothing
// installed, drift, findings) are the page, not a failure.
func serveOptions(c *ctx) (serve.Options, error) {
	wd, err := os.Getwd()
	if err != nil {
		return serve.Options{}, err
	}
	src, home := c.src, c.home
	return serve.Options{
		Home: home, Dir: wd, Port: c.port, Out: c.out, Err: stderr, Version: buildVersion(),
		Status: func(w io.Writer) error { return pageErr(status(src, home, w)) },
		Render: func(w io.Writer) error { return renderTo(src, w) },
		Check:  func(w io.Writer) error { return checkTo(wd, w) },
	}, nil
}

// pageErr maps an exitCode, a verb's ordinary non-zero status, to nil.
func pageErr(err error) error {
	var code exitCode
	if errors.As(err, &code) {
		return nil
	}
	return err
}

func renderTo(src harness.Source, w io.Writer) error {
	inv, err := harness.LoadInventory(src)
	if err != nil {
		return err
	}
	data, err := harness.RenderContext(src, inv)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// checkTo is `fyr check` in dir, the directory serve started in: its
// findings and summary, or a refusal's reason (stderr at the terminal) on w.
func checkTo(dir string, w io.Writer) error {
	rep, err := check.Run(dir, vocabulary())
	if errors.Is(err, check.ErrRefused) {
		fmt.Fprintln(w, search.Printable("check: "+err.Error()))
		return nil
	}
	if err != nil {
		return err
	}
	printCheck(w, rep)
	return nil
}
