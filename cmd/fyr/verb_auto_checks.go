package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/auto"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/trail"
)

// autoInstallFindings is catalog 2: what `fyr status` would flag, read without
// its retro line, which opens the store. A source that cannot be resolved is a
// finding; only the lines that ask for action become one.
func autoInstallFindings(repoFlag, home string) []auto.Finding {
	var fs []auto.Finding
	add := func(rule, text string) {
		fs = append(fs, auto.Finding{Check: "status", Rule: rule, Where: "fyr", Text: text})
	}
	if !auto.TimerInstalledHere(home, runtime.GOOS) {
		add("timer", "auto is on and its timer is not installed")
	}
	src, err := harness.ResolveSource(repoFlag, home, buildVersion())
	if err != nil {
		add("source", err.Error())
		return fs
	}
	var buf bytes.Buffer
	var info harness.RepoInfo
	if src.Dir != "" && harness.IsCheckout(src.Dir) {
		// Not harness.Inspect: its git status starts what the checkout's config
		// names, and this runs from a timer.
		if out, err := trail.Command(src.Dir, "rev-parse", "--short", "HEAD").Output(); err == nil {
			info.Commit = strings.TrimSpace(string(out))
		}
	}
	binaryLines(home, src.Dir, info, &buf)
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "run `fyr update`") || strings.HasPrefix(line, "shadowed") || strings.HasPrefix(line, "leftover") {
			add("binary", strings.Join(strings.Fields(line), " "))
		}
	}
	buf.Reset()
	if err := driftLines(src, home, &buf); err != nil {
		var code exitCode
		if !errors.As(err, &code) {
			add("drift", err.Error())
		}
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "drift") && !strings.HasPrefix(line, "drift      none") {
			add("drift", strings.Join(strings.Fields(line), " "))
		}
	}
	return fs
}

// autoMetricsFindings is catalog 3: ingest, then every eval row's flags. A
// failed ingest source is an error: the row flags still read what is stored.
func autoMetricsFindings(home string) ([]auto.Finding, error) {
	ingest := ingestTo(home, io.Discard)
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return nil, errors.Join(ingest, err)
	}
	defer s.Close()
	e, err := s.Evals(metrics.EvalOptions{Now: time.Now()})
	if err != nil {
		return nil, errors.Join(ingest, err)
	}
	var fs []auto.Finding
	for _, row := range e.Rows {
		for _, flag := range row.Flags {
			fs = append(fs, auto.Finding{Check: "metrics", Rule: flag, Where: row.Row, Text: fmt.Sprintf("%d trials, last %s", row.N, row.Last)})
		}
	}
	return fs, ingest
}
