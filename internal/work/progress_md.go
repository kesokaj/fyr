package work

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// RenderWaveMarkdown renders wave as a Markdown document with a Mermaid
// flowchart, a worker summary table, and a per-worker carousel for the
// Antigravity Artifact Sidepanel (ADR-513, D7).
func RenderWaveMarkdown(wave WaveStatus) string {
	var b strings.Builder
	stamp := sanitizeCell(wave.Stamp)
	if stamp == "" {
		stamp = "active"
	}
	fmt.Fprintf(&b, "# fyr work — wave %s\n\n", stamp)
	if wave.UpdatedAt != "" {
		fmt.Fprintf(&b, "Updated: `%s`\n\n", sanitizeCell(wave.UpdatedAt))
	}
	writeWaveMermaid(&b, stamp, wave.Workers)
	writeWaveTable(&b, wave.Workers)
	writeWaveCarousel(&b, wave.Workers)
	return b.String()
}

func writeWaveMermaid(b *strings.Builder, stamp string, workers []WorkerStatus) {
	b.WriteString("```mermaid\nflowchart LR\n")
	fmt.Fprintf(b, "  wave[\"%s\"]\n", mermaidLabel("wave "+stamp))
	for i, w := range workers {
		wid := fmt.Sprintf("w%d", i)
		state := w.State
		if state == "" {
			state = "queued"
		}
		fmt.Fprintf(b, "  wave --> %s[\"%s (%s)\"]\n",
			wid, mermaidLabel(w.Name), mermaidLabel(state))
		for j, sub := range w.Subagents {
			sid := fmt.Sprintf("%s_s%d", wid, j)
			fmt.Fprintf(b, "  %s --> %s[\"%s\"]\n",
				wid, sid, mermaidLabel("subagent: "+sub))
		}
		if w.JudgeVerdict != "" {
			jm := w.JudgeModel
			if jm == "" {
				jm = "judge"
			}
			fmt.Fprintf(b, "  %s --> %s_j[\"%s\"]\n",
				wid, wid, mermaidLabel(fmt.Sprintf("judge: %s (%s)", jm, w.JudgeVerdict)))
		}
	}
	b.WriteString("```\n\n")
}

func writeWaveTable(b *strings.Builder, workers []WorkerStatus) {
	b.WriteString("| Worker | State | Model | Branch | Elapsed | Steps | Commits | Subagents | Last Step |\n")
	b.WriteString("|---|---|---|---|---|---:|---:|---|---|\n")
	for _, w := range workers {
		subs := "-"
		if len(w.Subagents) > 0 {
			subs = sanitizeCell(strings.Join(w.Subagents, ", "))
		}
		last := "-"
		if w.LastStep != "" {
			last = sanitizeCell(w.LastStep)
		}
		fmt.Fprintf(b, "| `%s` | `%s` | `%s` | `%s` | `%s` | %d | %d | %s | %s |\n",
			sanitizeCell(w.Name), sanitizeCell(w.State), sanitizeCell(w.Model),
			sanitizeCell(w.Branch), fmtMMSS(w.ElapsedSec), w.StepCount, w.Commits, subs, last)
	}
	b.WriteByte('\n')
}

func writeWaveCarousel(b *strings.Builder, workers []WorkerStatus) {
	if len(workers) == 0 {
		return
	}
	b.WriteString("````carousel\n")
	for i, w := range workers {
		if i > 0 {
			b.WriteString("<!-- slide -->\n")
		}
		writeWorkerSlide(b, w)
	}
	b.WriteString("````\n")
}

func writeWorkerSlide(b *strings.Builder, w WorkerStatus) {
	files := "(drafts)"
	if len(w.Files) > 0 {
		files = strings.Join(w.Files, ", ")
	}
	subs := "none"
	if len(w.Subagents) > 0 {
		subs = strings.Join(w.Subagents, ", ")
	}
	last := "(waiting)"
	if w.LastStep != "" {
		last = w.LastStep
	}
	fmt.Fprintf(b, "### Worker `%s` (`%s`)\n\n", sanitizeCell(w.Name), sanitizeCell(w.State))
	fmt.Fprintf(b, "- **Branch:** `%s` · **Model:** `%s` · **Elapsed:** `%s` · **Steps:** %d · **Commits:** %d\n",
		sanitizeCell(w.Branch), sanitizeCell(w.Model), fmtMMSS(w.ElapsedSec), w.StepCount, w.Commits)
	fmt.Fprintf(b, "- **Files:** `%s`\n", sanitizeCell(files))
	fmt.Fprintf(b, "- **Subagents:** `%s`\n", sanitizeCell(subs))
	if w.BaseCheck != "" {
		fmt.Fprintf(b, "- **Base-Check:** `%s`\n", sanitizeCell(w.BaseCheck))
	}
	if w.JudgeVerdict != "" {
		jm := w.JudgeModel
		if jm == "" {
			jm = "judge"
		}
		fmt.Fprintf(b, "- **Judge:** `%s (%s)`\n", sanitizeCell(jm), sanitizeCell(w.JudgeVerdict))
	}
	fmt.Fprintf(b, "- **Last step:** `%s`\n", sanitizeCell(last))
	if len(w.Reasons) > 0 {
		fmt.Fprintf(b, "- **Reasons:** `%s`\n", sanitizeCell(strings.Join(w.Reasons, "; ")))
	}
}

func mermaidLabel(s string) string {
	clean := sandbox.Printable(s)
	r := strings.NewReplacer(`"`, `'`, `[`, `(`, `]`, `)`, `\`, `/`, "\n", " ")
	return r.Replace(clean)
}

func sanitizeCell(s string) string {
	clean := sandbox.Printable(s)
	r := strings.NewReplacer("|", "/", "`", "'", "\n", " ")
	return strings.TrimSpace(r.Replace(clean))
}

func writeStatusMD(path string, wave WaveStatus) {
	if path == "" {
		return
	}
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return
	}
	_, werr := f.WriteString(RenderWaveMarkdown(wave))
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(tmpPath, path) != nil {
		_ = os.Remove(tmpPath)
	}
}
