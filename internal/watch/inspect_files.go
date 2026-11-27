package watch

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

const (
	tailReadBytes  = 8 << 10
	artifactMaxB   = 64 << 10
	promptHintMaxB = 120
)

var (
	interactivePromptRe = regexp.MustCompile("(?i)(\\[y/n\\]|\\(y/n\\)|\\[yes/no\\]|\\(yes/no(?:/\\[fingerprint\\])?\\)\\?|pa" + "ssword:|pa" + "ssphrase|continue\\?|press enter)")
	statusInProgressRe  = regexp.MustCompile(`(?i)^(?:#{1,6}\s*)?status:\s*in\s+progress(?:\s|$|<!--)`)
)

func inspectConversationFiles(home, convID string, now time.Time) (Finding, bool) {
	if home == "" || !validConvID(convID) {
		return Finding{}, false
	}
	brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", convID, ".system_generated")
	trPath := filepath.Join(brain, "logs", "transcript.jsonl")
	if hint, mt, ok := matchFileTailPrompt(trPath, 5, true); ok {
		return interactiveFinding(convID, hint, mt, now), true
	}
	if stepOut := latestStepOutput(filepath.Join(brain, "steps")); stepOut != "" {
		if hint, mt, ok := matchFileTailPrompt(stepOut, 20, false); ok {
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

func matchFileTailPrompt(path string, maxLines int, jsonl bool) (string, time.Time, bool) {
	data, mod, ok := readFileTail(path, tailReadBytes)
	if !ok {
		return "", time.Time{}, false
	}
	lines := tailNonEmptyLines(string(data), maxLines)
	for i := len(lines) - 1; i >= 0; i-- {
		if interactivePromptRe.MatchString(lines[i]) {
			return clipPrintableMode(lines[i], promptHintMaxB, jsonl), mod, true
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

func checkAgentsDirNoSymlink(agentsDir string) (bool, error) {
	info, err := os.Lstat(agentsDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%w: %s is a symlink", trail.ErrStampSymlink, agentsDir)
	}
	return info.IsDir(), nil
}

func inspectArtifactsChecked(dir string, idleSec int, now time.Time) (int, []Finding, error) {
	agentsDir := filepath.Join(dir, ".agents", "scratch", "agents")
	okDir, err := checkAgentsDirNoSymlink(agentsDir)
	if err != nil || !okDir {
		return 0, nil, err
	}
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return 0, nil, nil
	}
	checked := 0
	var findings []Finding
	threshold := time.Duration(idleSec) * time.Second
	cutoff := now.Add(-discoveryWindow)
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".md") {
			continue
		}
		if info, err := ent.Info(); err == nil && info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
			continue
		}
		p := filepath.Join(agentsDir, ent.Name())
		data, mod, ok := readFileHead(p, artifactMaxB)
		if !ok || mod.Before(cutoff) {
			continue
		}
		checked++
		if !hasInProgressStatus(string(data)) {
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
	return checked, findings, nil
}

func hasInProgressStatus(body string) bool {
	var fenceChar byte
	var fenceLen int
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		ch, n := parseFence(trimmed)
		if fenceChar != 0 {
			if ch == fenceChar && n >= fenceLen && strings.TrimSpace(trimmed[n:]) == "" {
				fenceChar, fenceLen = 0, 0
			}
			continue
		}
		if ch != 0 && (ch != '`' || !strings.ContainsRune(trimmed[n:], '`')) {
			fenceChar, fenceLen = ch, n
			continue
		}
		if statusInProgressRe.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func parseFence(trimmed string) (byte, int) {
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return 0, 0
	}
	ch := trimmed[0]
	n := 0
	for n < len(trimmed) && trimmed[n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0
	}
	return ch, n
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

func clipPrintableMode(s string, maxB int, jsonl bool) string {
	redacted := redact.Redact(s)
	if jsonl {
		redacted = redact.RedactJSONL(s)
	}
	clean := search.Printable(strings.Join(strings.Fields(redacted), " "))
	if len(clean) <= maxB {
		return clean
	}
	return trail.TruncateUTF8(clean, maxB) + "..."
}
