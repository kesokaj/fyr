package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteMarkdownReport renders sum and evals as Markdown to file (mode 0600);
// an existing file is refused. Like WriteReport, it renders in memory first so
// a failure leaves no partial file on disk.
func WriteMarkdownReport(sum *Summary, evals *EvalRows, file string, now time.Time) error {
	var page bytes.Buffer
	if err := RenderMarkdownReport(&page, sum, evals, now); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("refusing to overwrite %s", file)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(page.Bytes()); err != nil {
		f.Close()
		os.Remove(file)
		return err
	}
	return f.Close()
}

// RenderMarkdownReport writes the Markdown usage report (Scorecard, sections 1-5, Background Autonomy, and Trigger coverage) to w.
func RenderMarkdownReport(w io.Writer, sum *Summary, evals *EvalRows, now time.Time) error {
	series := seriesOf(sum)
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent metrics (%s .. %s)\n\n",
		mdCell(sum.Window["since"]), mdCell(sum.Window["until"]))
	fmt.Fprintf(&b, "Generated %s by `fyr report` from the local metrics store.\n",
		now.UTC().Format("2006-01-02 15:04 UTC"))
	for _, sec := range reportSections(sum, series, evals) {
		fmt.Fprintf(&b, "\n## %s\n\n", sec.Title)
		writeMDTable(&b, sec.Table)
		if len(sec.SubTable.Head) > 0 {
			if sec.SubTitle != "" {
				fmt.Fprintf(&b, "\n### %s\n\n", sec.SubTitle)
			}
			writeMDTable(&b, sec.SubTable)
		}
	}
	if es := evalsSectionOf(evals); es != nil {
		b.WriteString("\n## Eval rows\n\n")
		writeMDTable(&b, es.Table)
		for _, line := range es.Lines {
			fmt.Fprintf(&b, "- %s\n", mdCell(line))
		}
	}
	if len(sum.Notes) > 0 {
		b.WriteString("\n## Notes\n\n")
		for _, n := range sum.Notes {
			fmt.Fprintf(&b, "- %s\n", mdCell(n))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeMDTable(b *strings.Builder, t table) {
	if len(t.Head) == 0 {
		return
	}
	writeMDRow(b, t.Head)
	sep := make([]string, len(t.Head))
	for i := range sep {
		if i == 0 {
			sep[i] = ":---"
		} else {
			sep[i] = "---:"
		}
	}
	writeMDRow(b, sep)
	for _, r := range t.Rows {
		writeMDRow(b, r)
	}
}

func writeMDRow(b *strings.Builder, cells []string) {
	b.WriteString("|")
	for _, c := range cells {
		fmt.Fprintf(b, " %s |", mdCell(c))
	}
	b.WriteString("\n")
}

func mdCell(s string) string {
	s = printable(s)
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}
