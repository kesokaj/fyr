package watch

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	tailReadBytes  = 8 << 10
	artifactMaxB   = 64 << 10
	promptHintMaxB = 120
)

var (
	interactivePromptRe = regexp.MustCompile("(?i)(\\[y/n\\]|\\[yes/no\\]|pa" + "ssword:|pa" + "ssphrase|continue\\?|press enter)")
	statusInProgressRe  = regexp.MustCompile(`(?i)^(?:#{1,6}\s*)?status:\s*in\s+progress(?:\s|$|<!--)`)
)

func inspectConversationFiles(home, convID string, now time.Time) (Finding, bool) {
	if home == "" || !validConvID(convID) {
		return Finding{}, false
	}
	brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", convID, ".system_generated")
	trPath := filepath.Join(brain, "logs", "transcript.jsonl")
	if hint, mt, ok := matchFileTailPrompt(trPath, 5); ok {
		return interactiveFinding(convID, hint, mt, now), true
	}
	if stepOut := latestStepOutput(filepath.Join(brain, "steps")); stepOut != "" {
		if hint, mt, ok := matchFileTailPrompt(stepOut, 20); ok {
			return interactiveFinding(convID, hint, mt, now), true
		}
	}
	return Finding{}, false
}

func interactiveFinding(convID, hint string, mod time.Time, now time.Time) Finding {
	idle := int64(now.Sub(mod).Seconds())
	if idle < 0 {
		idle = 0
	}
	return Finding{
		Kind:       kindInteractiveWait,
		Target:     convID,
		IdleSec:    idle,
		PromptHint: hint,
		Action:     "conversation output ends with an interactive prompt; supply input, abort, or rerun non-interactively",
	}
}

func matchFileTailPrompt(path string, maxLines int) (string, time.Time, bool) {
	data, mod, ok := readFileTail(path, tailReadBytes)
	if !ok {
		return "", time.Time{}, false
	}
	lines := tailNonEmptyLines(string(data), maxLines)
	for i := len(lines) - 1; i >= 0; i-- {
		if interactivePromptRe.MatchString(lines[i]) {
			return clipPrintable(lines[i], promptHintMaxB), mod, true
		}
	}
	return "", time.Time{}, false
}

func latestStepOutput(stepsDir string) string {
	entries, err := os.ReadDir(stepsDir)
	if err != nil {
		return ""
	}
	type candidate struct {
		num  int
		mod  time.Time
		path string
	}
	var cands []candidate
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		p := filepath.Join(stepsDir, ent.Name(), "output.txt")
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		num, err := strconv.Atoi(ent.Name())
		if err != nil {
			num = -1
		}
		cands = append(cands, candidate{num: num, mod: fi.ModTime(), path: p})
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].num != cands[j].num {
			return cands[i].num > cands[j].num
		}
		if !cands[i].mod.Equal(cands[j].mod) {
			return cands[i].mod.After(cands[j].mod)
		}
		return cands[i].path > cands[j].path
	})
	return cands[0].path
}

func inspectArtifacts(dir string, idleSec int, now time.Time) (int, []Finding) {
	agentsDir := filepath.Join(dir, ".agents", "scratch", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return 0, nil
	}
	checked := 0
	var findings []Finding
	threshold := time.Duration(idleSec) * time.Second
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		checked++
		p := filepath.Join(agentsDir, ent.Name())
		data, mod, ok := readFileHead(p, artifactMaxB)
		if !ok || !hasInProgressStatus(string(data)) {
			continue
		}
		if idle := now.Sub(mod); idle > threshold {
			findings = append(findings, Finding{
				Kind:           kindStuckNoProgress,
				Target:         filepath.ToSlash(filepath.Join(".agents", "scratch", "agents", ent.Name())),
				IdleSec:        int64(idle.Seconds()),
				ArtifactStatus: "IN PROGRESS",
				Action:         "artifact stayed IN PROGRESS past idle threshold; check subagent or stop/re-dispatch",
			})
		}
	}
	return checked, findings
}

func hasInProgressStatus(body string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if statusInProgressRe.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func readFileTail(path string, maxBytes int64) ([]byte, time.Time, bool) {
	return readRegularSlice(path, maxBytes, true)
}

func readFileHead(path string, maxBytes int64) ([]byte, time.Time, bool) {
	return readRegularSlice(path, maxBytes, false)
}

func readRegularSlice(path string, maxBytes int64, fromEnd bool) ([]byte, time.Time, bool) {
	f, err := trail.OpenNonBlocking(path)
	if err != nil {
		return nil, time.Time{}, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, time.Time{}, false
	}
	if fromEnd && fi.Size() > maxBytes {
		if _, err := f.Seek(-maxBytes, io.SeekEnd); err != nil {
			return nil, time.Time{}, false
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, time.Time{}, false
	}
	return data, fi.ModTime(), true
}

func tailNonEmptyLines(text string, limit int) []string {
	var out []string
	for _, line := range bytes.Split([]byte(strings.ReplaceAll(text, "\r\n", "\n")), []byte{'\n'}) {
		if s := strings.TrimSpace(string(line)); s != "" {
			out = append(out, s)
		}
	}
	if len(out) > limit {
		return out[len(out)-limit:]
	}
	return out
}

func clipPrintable(s string, maxB int) string {
	clean := search.Printable(strings.Join(strings.Fields(s), " "))
	if len(clean) <= maxB {
		return clean
	}
	return clean[:maxB] + "..."
}
