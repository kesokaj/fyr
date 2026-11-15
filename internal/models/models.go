package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ModelIDRe matches syntactically valid model identifiers returned by agy models.
var ModelIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var (
	ansiCSIRe  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]`)
	twoSpaceRe = regexp.MustCompile(`  +`)
)

// ModelsTimeout bounds the agy models call (D125), a variable so a test can shorten it.
var ModelsTimeout = time.Minute

// MaxModelsOut caps stdout and stderr of subprocesses spawned by models helpers (D125).
const MaxModelsOut = 64 << 10

// ErrModelsOver is returned when stdout exceeds MaxModelsOut.
var ErrModelsOver = fmt.Errorf("its output is over %d KiB", MaxModelsOut>>10)

// ListModels runs `agy models` and returns the parsed model IDs in listed order.
func ListModels(run context.Context, agy string) ([]string, error) {
	raw, err := RunAgyModels(run, agy)
	if err != nil {
		return nil, err
	}
	entries := ParseModelsOutput(raw)
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids, nil
}

// RunAgyModels executes `agy models` in / with process-group isolation and
// output capping, returning raw stdout.
func RunAgyModels(run context.Context, agy string) (string, error) {
	ctx, cancel := context.WithTimeout(run, ModelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, agy, "models")
	cmd.Dir = "/"
	cmd.Env = RootEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &Capped{Over: ErrModelsOver}, &Capped{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	switch {
	case stdout.full:
		err = ErrModelsOver
	case run.Err() != nil:
		err = errors.New("the run ended before it answered")
	case ctx.Err() == context.DeadlineExceeded:
		err = fmt.Errorf("no answer in %s", ModelsTimeout)
	}
	if err != nil {
		if l := LastLine(stderr.String()); l != "" {
			err = fmt.Errorf("%v: %s", err, l)
		}
		return "", err
	}
	return stdout.String(), nil
}

// RootEnv returns the process environment stripped of git overrides, fyr
// recursion markers, PWD/OLDPWD, and ANTIGRAVITY_* vars, with PWD=/.
func RootEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_") || k == "FYR_REPO" || k == "FYR_CHILD" ||
			k == "PWD" || k == "OLDPWD" || strings.HasPrefix(k, "ANTIGRAVITY_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PWD=/")
}

// Capped keeps up to MaxModelsOut bytes. Past that it returns Over, which
// ends the copy and closes the child's pipe, or with Over nil drops the rest.
type Capped struct {
	buf  bytes.Buffer
	Over error
	full bool
}

// Write appends p up to MaxModelsOut, returning Over when truncated.
func (c *Capped) Write(p []byte) (int, error) {
	if room := MaxModelsOut - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:max(room, 0)])
		c.full = true
		if c.Over != nil {
			return 0, c.Over
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

// String returns the buffered bytes accumulated before any cap.
func (c *Capped) String() string { return c.buf.String() }

// Full reports whether writes hit MaxModelsOut.
func (c *Capped) Full() bool { return c.full }

// LastLine returns s's last non-blank line, past any carriage return a spinner left.
func LastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := lines[len(lines)-1]
	if i := strings.LastIndex(strings.TrimRight(l, "\r"), "\r"); i >= 0 {
		l = l[i+1:]
	}
	return strings.TrimSpace(l)
}

// ParseModelsOutput strips ANSI CSI/OSC sequences and \r spinner frames from
// `agy models` stdout and parses `<id>\t<display>` or `<id>  +<display>` rows.
func ParseModelsOutput(raw string) []CatalogEntry {
	clean := ansiCSIRe.ReplaceAllString(strings.ReplaceAll(raw, "\r\n", "\n"), "")
	seen := map[string]bool{}
	var out []CatalogEntry
	for _, line := range strings.Split(clean, "\n") {
		if i := strings.LastIndexByte(line, '\r'); i >= 0 {
			line = line[i+1:]
		}
		line = strings.TrimSpace(line)
		id, disp, ok := splitModelLine(line)
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		fam, tier, eff := ClassifyModel(id, disp)
		entry := CatalogEntry{
			ID:          id,
			DisplayName: disp,
			Family:      fam,
			Tier:        tier,
			Effort:      eff,
		}
		ApplyModelProfile(&entry)
		out = append(out, entry)
	}
	return out
}

func splitModelLine(line string) (string, string, bool) {
	var id, disp string
	if left, right, ok := strings.Cut(line, "\t"); ok {
		id, disp = strings.TrimSpace(left), strings.TrimSpace(right)
	} else if loc := twoSpaceRe.FindStringIndex(line); loc != nil {
		id, disp = strings.TrimSpace(line[:loc[0]]), strings.TrimSpace(line[loc[1]:])
	} else {
		return "", "", false
	}
	if !ModelIDRe.MatchString(id) || isHeaderRow(id, disp) {
		return "", "", false
	}
	return id, disp, true
}

func isHeaderRow(id, disp string) bool {
	lowID := strings.ToLower(id)
	lowDisp := strings.ToLower(disp)
	if (lowID == "id" || lowID == "model" || lowID == "name") &&
		(strings.Contains(lowDisp, "name") || strings.Contains(lowDisp, "display") || strings.HasPrefix(lowDisp, "---")) {
		return true
	}
	return strings.Trim(id, "-") == ""
}

// ClassifyModel derives family ("gemini"|"anthropic"|"other"), tier ("pro"|"flash"),
// and effort ("max"|"xhigh"|"high"|"medium"|"low"|"orchestrator"|"") from a model ID and display name.
func ClassifyModel(id, displayName string) (family, tier, effort string) {
	low := strings.ToLower(strings.TrimSpace(id))
	switch {
	case strings.HasPrefix(low, "gemini-"):
		family = "gemini"
	case strings.HasPrefix(low, "claude-"):
		family = "anthropic"
	default:
		family = "other"
	}
	lowDisp := strings.ToLower(displayName)
	switch {
	case strings.Contains(low, "orchestrator") || strings.Contains(lowDisp, "orchestrator"):
		effort = "orchestrator"
	case strings.HasSuffix(low, "-xhigh") || strings.Contains(lowDisp, "(xhigh)"):
		effort = "xhigh"
	case strings.HasSuffix(low, "-max") || strings.Contains(lowDisp, "(max)"):
		effort = "max"
	case strings.HasSuffix(low, "-high"):
		effort = "high"
	case strings.HasSuffix(low, "-medium"):
		effort = "medium"
	case strings.HasSuffix(low, "-low"):
		effort = "low"
	}
	lowAll := low + " " + lowDisp
	isFastHydrogen := strings.Contains(lowAll, "hydrogen") && (effort == "low" || effort == "medium" || effort == "")
	if strings.Contains(lowAll, "flash") || strings.Contains(lowAll, "sonnet") ||
		strings.Contains(lowAll, "haiku") || strings.Contains(lowAll, "-lite") || strings.Contains(lowAll, "-mini") ||
		strings.Contains(lowAll, "gpt-oss") || isFastHydrogen {
		tier = "flash"
	} else {
		tier = "pro"
	}
	return family, tier, effort
}
