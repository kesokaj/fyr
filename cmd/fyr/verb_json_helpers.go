package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/check"
	"github.com/kesokaj/fyr/internal/eval"
	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

func writeVerbJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func captureVerbOutput(c *ctx, fn func() error) (string, error) {
	var buf bytes.Buffer
	orig := c.out
	c.out = &buf
	err := fn()
	c.out = orig
	return buf.String(), err
}

func verbSourceLabel(src harness.Source) string {
	if src.Dir != "" {
		return src.Dir
	}
	if src.Origin != "" {
		return src.Origin
	}
	return strings.TrimSpace("embedded " + src.Version)
}

type versionJSON struct {
	Version string `json:"version"`
}

type checkFindingJSON struct {
	Where string `json:"where"`
	Line  int    `json:"line,omitempty"`
	Rule  string `json:"rule"`
	Text  string `json:"text"`
}

type checkCountsJSON struct {
	Total        int `json:"total"`
	Docs         int `json:"docs"`
	Specs        int `json:"specs"`
	Requirements int `json:"requirements"`
	Traces       int `json:"traces"`
}

type checkReportJSON struct {
	OK       bool               `json:"ok"`
	Findings []checkFindingJSON `json:"findings"`
	Notes    []string           `json:"notes"`
	Counts   checkCountsJSON    `json:"counts"`
	Version  string             `json:"version"`
}

func writeCheckJSON(w io.Writer, rep check.Report) error {
	findings := make([]checkFindingJSON, 0, len(rep.Findings))
	for _, f := range rep.Findings {
		findings = append(findings, checkFindingJSON{
			Where: search.Printable(f.Where),
			Line:  f.Line,
			Rule:  search.Printable(f.Rule),
			Text:  search.Printable(f.Text),
		})
	}
	notes := make([]string, 0, len(rep.Notes))
	for _, n := range rep.Notes {
		notes = append(notes, search.Printable(n))
	}
	d, s, r, tr := check.Counts(rep.Findings)
	return writeVerbJSON(w, checkReportJSON{
		OK:       len(rep.Findings) == 0,
		Findings: findings,
		Notes:    notes,
		Counts: checkCountsJSON{
			Total:        len(rep.Findings),
			Docs:         d,
			Specs:        s,
			Requirements: r,
			Traces:       tr,
		},
		Version: buildVersion(),
	})
}

type dirOutputJSON struct {
	OK     bool   `json:"ok"`
	Dir    string `json:"dir"`
	Output string `json:"output"`
}

type listJSON struct {
	Source string `json:"source"`
	Output string `json:"output"`
}

type renderJSON struct {
	Source  string `json:"source"`
	Bytes   int    `json:"bytes"`
	Content string `json:"content"`
}

type diffJSON struct {
	OK     bool   `json:"ok"`
	Drift  bool   `json:"drift"`
	Output string `json:"output"`
}

type installJSON struct {
	Verb   string `json:"verb"`
	OK     bool   `json:"ok"`
	DryRun bool   `json:"dry_run"`
	Home   string `json:"home"`
	Output string `json:"output"`
}

type uninstallJSON struct {
	OK     bool   `json:"ok"`
	DryRun bool   `json:"dry_run"`
	Home   string `json:"home"`
	Output string `json:"output"`
}

type updateJSON struct {
	OK      bool   `json:"ok"`
	DryRun  bool   `json:"dry_run"`
	Check   bool   `json:"check"`
	FromZip string `json:"from_zip"`
	Behind  bool   `json:"behind"`
	Output  string `json:"output"`
}

func writeUpdateJSON(c *ctx, behind, ok bool, out string) error {
	return writeVerbJSON(c.out, updateJSON{
		OK:      ok,
		DryRun:  c.dryRun,
		Check:   c.check,
		FromZip: c.fromZip,
		Behind:  behind,
		Output:  out,
	})
}

func (c *ctx) runUpdateFromZip() error {
	if !c.jsonOut {
		return updateFromZip(c.fromZip, c.home, c.dryRun, c.out)
	}
	var buf bytes.Buffer
	if err := updateFromZip(c.fromZip, c.home, c.dryRun, &buf); err != nil {
		return err
	}
	return writeUpdateJSON(c, false, true, buf.String())
}

type adoptJSON struct {
	OK     bool     `json:"ok"`
	Force  bool     `json:"force"`
	Args   []string `json:"args"`
	Output string   `json:"output"`
}

type indexJSON struct {
	OK       bool   `json:"ok"`
	Global   bool   `json:"global"`
	Rebuild  bool   `json:"rebuild"`
	Register bool   `json:"register"`
	Dir      string `json:"dir"`
	Output   string `json:"output"`
}

func writeIndexJSON(c *ctx, dir, out string) error {
	return writeVerbJSON(c.out, indexJSON{
		OK:       true,
		Global:   c.global,
		Rebuild:  c.rebuild,
		Register: c.register,
		Dir:      dir,
		Output:   out,
	})
}

func (c *ctx) runIndex(o search.Options) error {
	if !c.jsonOut {
		return search.Index(o, c.out)
	}
	var buf bytes.Buffer
	if err := search.Index(o, &buf); err != nil {
		return err
	}
	return writeIndexJSON(c, o.Dir, buf.String())
}

type dreamJSON struct {
	OK      bool   `json:"ok"`
	All     bool   `json:"all"`
	Project string `json:"project"`
	DryRun  bool   `json:"dry_run"`
	Output  string `json:"output"`
}

func writeDreamJSON(c *ctx, project, out string) error {
	return writeVerbJSON(c.out, dreamJSON{
		OK:      true,
		All:     c.dreamAll,
		Project: project,
		DryRun:  c.dryRun,
		Output:  out,
	})
}

type autoJSON struct {
	OK         bool   `json:"ok"`
	Subcommand string `json:"subcommand"`
	DryRun     bool   `json:"dry_run"`
	Output     string `json:"output"`
}

func writeAutoJSON(c *ctx, sub, out string) error {
	return writeVerbJSON(c.out, autoJSON{
		OK:         true,
		Subcommand: sub,
		DryRun:     c.dryRun,
		Output:     out,
	})
}

type evalListRowJSON struct {
	ID      string `json:"id"`
	Agent   string `json:"agent"`
	Grants  string `json:"grants"`
	Version int    `json:"version"`
	Kind    string `json:"kind"`
}

type evalListJSON struct {
	OK    bool              `json:"ok"`
	Suite string            `json:"suite"`
	Rows  []evalListRowJSON `json:"rows"`
}

func evalSuiteLabel(c *ctx, root string) string {
	if path, set := evalSuiteFile(c); set {
		return path
	}
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(eval.SuitePath))); err == nil {
		return eval.SuitePath
	}
	return suiteID
}

func writeEvalListJSON(c *ctx, root string, suite []eval.Row) error {
	rows := make([]evalListRowJSON, 0, len(suite))
	for _, r := range suite {
		grants := "named"
		if _, wide := r.Wide(); wide {
			grants = "wide"
		}
		rows = append(rows, evalListRowJSON{
			ID:      r.ID,
			Agent:   r.Agent,
			Grants:  grants,
			Version: r.Version,
			Kind:    r.Kind,
		})
	}
	return writeVerbJSON(c.out, evalListJSON{
		OK:    true,
		Suite: evalSuiteLabel(c, root),
		Rows:  rows,
	})
}

type evalTrialResultJSON struct {
	Row     string   `json:"row"`
	Trial   int      `json:"trial"`
	Run     string   `json:"run"`
	Status  string   `json:"status"`
	Cause   string   `json:"cause,omitempty"`
	Reasons []string `json:"reasons,omitempty"`
	AgyExit int      `json:"agy_exit"`
	Seconds int      `json:"seconds"`
	Sandbox string   `json:"sandbox"`
}

type evalRunJSON struct {
	OK          bool                  `json:"ok"`
	Interrupted bool                  `json:"interrupted,omitempty"`
	Harness     string                `json:"harness"`
	Results     []evalTrialResultJSON `json:"results"`
	Output      string                `json:"output"`
}

func writeEvalRunJSON(w io.Writer, harnessRef string, results []eval.Result, out string, ok, interrupted bool) error {
	items := make([]evalTrialResultJSON, 0, len(results))
	for _, r := range results {
		items = append(items, evalTrialResultJSON{
			Row:     r.Row,
			Trial:   r.Trial,
			Run:     r.Run,
			Status:  r.Status,
			Cause:   r.Cause,
			Reasons: r.Reasons,
			AgyExit: r.AgyExit,
			Seconds: r.Seconds,
			Sandbox: r.Dir,
		})
	}
	return writeVerbJSON(w, evalRunJSON{
		OK:          ok,
		Interrupted: interrupted,
		Harness:     harnessRef,
		Results:     items,
		Output:      out,
	})
}

func statusWithoutSourceJSON(st harness.State, home string, out io.Writer) error {
	var buf bytes.Buffer
	err := statusWithoutSource(st, home, &buf)
	var code exitCode
	if err != nil && !errors.As(err, &code) {
		return err
	}
	sum := statusSummary{Drifted: err != nil, Lines: []string{}}
	for _, line := range strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		if line != "" {
			sum.Lines = append(sum.Lines, line)
			applyStatusLine(&sum, line)
		}
	}
	if jerr := writeVerbJSON(out, sum); jerr != nil {
		return jerr
	}
	return err
}
