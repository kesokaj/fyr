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
	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	workisolate "github.com/kesokaj/fyr/internal/isolate"
	"github.com/kesokaj/fyr/internal/metrics"
	"github.com/kesokaj/fyr/internal/models"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

func autoPorts(vocab check.Vocabulary) auto.Ports {
	return auto.Ports{
		CheckRepo:            func(dir, home string) ([]auto.CheckFinding, error) { return autoCheckRepo(dir, vocab, home) },
		InstalledCommit:      autoInstalledCommit,
		ParseUsage:           autoParseUsage,
		PickModel:            autoPickModel,
		ReadTrackedSuite:     autoReadTrackedSuite,
		ReadSuiteFile:        autoReadSuiteFile,
		ParseSuite:           autoParseSuite,
		CapsuleDeps:          autoCapsuleDeps,
		ReadAgyConversations: autoReadAgyConversations,
		NewFence:             autoNewFence,
	}
}

func autoCheckRepo(dir string, vocab check.Vocabulary, home string) ([]auto.CheckFinding, error) {
	rep, err := check.Run(dir, vocab, home)
	out := make([]auto.CheckFinding, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		out = append(out, auto.CheckFinding{Rule: f.Rule, Where: f.Where, Line: f.Line, Text: f.Text})
	}
	return out, err
}

func autoInstalledCommit(home string) string {
	hst, found, err := harness.LoadState(home)
	if err != nil || !found {
		return ""
	}
	return hst.Commit
}

func autoParseUsage(text string) ([]auto.Pool, error) {
	pools, err := models.ParseUsage(text)
	if err != nil {
		return nil, err
	}
	out := make([]auto.Pool, len(pools))
	for i, p := range pools {
		out[i] = auto.Pool{Name: p.Name, Five: p.Five, Week: p.Week}
	}
	return out, nil
}

func autoPickModel(home, role, tier string) string {
	cat, ok, err := models.LoadCatalog(home)
	if err != nil || !ok {
		return ""
	}
	pick, err := models.PickFromCatalog(cat, role, tier)
	if err != nil {
		return ""
	}
	return pick.Model
}

func autoMapEvalRows(rows []eval.Row) []auto.EvalRow {
	out := make([]auto.EvalRow, len(rows))
	for i, r := range rows {
		out[i] = auto.EvalRow{ID: r.ID, Prompt: r.Prompt, Prompt2: r.Prompt2, Agent: r.Agent, Kind: r.Kind}
	}
	return out
}

func autoReadTrackedSuite(root string) ([]auto.EvalRow, bool, error) {
	rows, found, err := eval.ReadTrackedSuite(root, false)
	if err != nil || !found {
		return nil, found, err
	}
	return autoMapEvalRows(rows), true, nil
}

func autoReadSuiteFile(path string) ([]auto.EvalRow, error) {
	rows, err := eval.ReadSuiteFile(path)
	if err != nil {
		return nil, err
	}
	return autoMapEvalRows(rows), nil
}

func autoParseSuite(body string) ([]auto.EvalRow, error) {
	rows, err := eval.ParseSuite(body)
	if err != nil {
		return nil, err
	}
	return autoMapEvalRows(rows), nil
}

func autoCapsuleDeps(sliceSession, preseedSession string) workisolate.CapsuleDeps {
	return workisolate.CapsuleDeps{
		SliceFn: func(dir, query string, budget int) (string, error) {
			var buf bytes.Buffer
			err := search.BuildContextSlice(search.Options{
				Dir:     dir,
				Slice:   query,
				Budget:  budget,
				Hops:    2,
				Session: sliceSession,
			}, &buf)
			return buf.String(), err
		},
		PreseedFn: func(dir string, pivots []workisolate.CapsulePivot) error {
			sp := make([]search.CapsulePivot, len(pivots))
			for i, p := range pivots {
				sp[i] = search.CapsulePivot{Path: p.Path, StartLine: p.StartLine, EndLine: p.EndLine}
			}
			return search.RegisterCapsulePivots(dir, preseedSession, 1, sp)
		},
	}
}

func autoReadAgyConversations(home, root string, since, cutoff time.Time) ([]auto.AgyConversation, error) {
	agy, _, err := search.AgyConversationTextInWindow(home, root, since, cutoff)
	if err != nil {
		return nil, err
	}
	out := make([]auto.AgyConversation, len(agy))
	for i, a := range agy {
		out[i] = auto.AgyConversation{ID: a.ID, Text: a.Text}
	}
	return out, nil
}

func autoNewFence(texts ...string) func(name, who, body string) string {
	f := search.New(texts...)
	return f.Data
}

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
	fs, err := autoMetricsEvalFindings(home)
	return fs, errors.Join(ingest, err)
}

func autoMetricsEvalFindings(home string) ([]auto.Finding, error) {
	s, err := metrics.Open(metrics.Path(home))
	if err != nil {
		return nil, err
	}
	defer s.Close()
	e, err := s.Evals(metrics.EvalOptions{Now: time.Now()})
	if err != nil {
		return nil, err
	}
	var fs []auto.Finding
	for _, row := range e.Rows {
		for _, flag := range row.Flags {
			fs = append(fs, auto.Finding{Check: "metrics", Rule: flag, Where: row.Row, Text: fmt.Sprintf("%d trials, last %s", row.N, row.Last)})
		}
	}
	return fs, nil
}
