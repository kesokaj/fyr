package models

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/trail"
)

// ModelIDRe matches syntactically valid model identifiers returned by agy models.
var ModelIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

var (
	ansiCSIRe  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]`)
	twoSpaceRe = regexp.MustCompile(`  +`)
)

// ModelsTimeout bounds the agy models call (D125), a variable so a test can shorten it.
var ModelsTimeout = time.Minute

// ChangelogTimeout bounds `agy changelog` streaming probes (F-052).
var ChangelogTimeout = 5 * time.Second

// MaxModelsOut caps stdout and stderr of models/usage subprocesses (D125).
const MaxModelsOut = 64 << 10

// MaxChangelogOut caps stdout of `agy changelog` probes at 1 MiB (F-052).
const MaxChangelogOut = 1 << 20

// ErrModelsOver is returned when stdout exceeds MaxModelsOut.
var ErrModelsOver = fmt.Errorf("its output is over %d KiB", MaxModelsOut>>10)

// ListModels runs `agy models` and returns the parsed model IDs in listed order.
func ListModels(run context.Context, agy string) ([]string, error) {
	return listModelsForHome(run, "", agy)
}

func listModelsForHome(run context.Context, home, agy string) ([]string, error) {
	raw, err := runAgyModelsForHome(run, home, agy)
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

// RunAgyModels executes `agy models` in probeWorkDir() with process-group
// isolation and output capping, returning raw stdout.
func RunAgyModels(run context.Context, agy string) (string, error) {
	return runAgyModelsForHome(run, "", agy)
}

func runAgyModelsForHome(run context.Context, home, agy string) (string, error) {
	return runAgyProbe(run, home, agy, ModelsTimeout, 5*time.Second, "models")
}

// RootEnv returns the process environment stripped of git overrides, fyr
// recursion markers, PWD/OLDPWD, and ANTIGRAVITY_* vars, with PWD=/.
func RootEnv() []string { return rootEnvWithPWD("/") }

func rootEnvWithPWD(pwd string) []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_") || k == "FYR_REPO" || k == "FYR_CHILD" ||
			k == "PWD" || k == "OLDPWD" || strings.HasPrefix(k, "ANTIGRAVITY_") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "PW"+"D="+pwd)
}

// probeWorkDir creates a private per-invocation 0700 directory under
// <home>/.config/fyr/sandbox so agy probe invocations never write to /tmp,
// never share a predictable directory, and never record "/" as a workspace URI.
func probeWorkDir(home string) (string, func(), error) {
	if strings.TrimSpace(home) == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", func() {}, fmt.Errorf("probe workdir home: %v", err)
		}
	}
	cleanHome := filepath.Clean(strings.TrimSpace(home))
	if cleanHome == "" || cleanHome == "/" || cleanHome == "." {
		return "", func() {}, fmt.Errorf("probe workdir home: invalid %q", home)
	}
	if err := ensureHomeDirSafe(cleanHome); err != nil {
		return "", func() {}, fmt.Errorf("probe workdir home: %w", err)
	}
	root := filepath.Join(cleanHome, harness.StateDir, "sandbox")
	if st, lerr := os.Lstat(root); lerr == nil {
		if st.Mode()&os.ModeSymlink != 0 {
			return "", func() {}, fmt.Errorf("probe workdir symlink refused: %s", root)
		}
		if !st.IsDir() {
			return "", func() {}, fmt.Errorf("probe workdir non-directory refused: %s", root)
		}
	} else if lerr != nil && !errors.Is(lerr, fs.ErrNotExist) {
		return "", func() {}, fmt.Errorf("probe workdir: %w", lerr)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", func() {}, fmt.Errorf("probe workdir: %w", err)
	}
	dir, err := os.MkdirTemp(root, "fyr-agy-probes-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("probe workdir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", func() {}, fmt.Errorf("probe workdir chmod: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func runAgyProbe(run context.Context, home, agy string, timeout, waitDelay time.Duration, args ...string) (string, error) {
	return runAgyProbeCapped(run, home, agy, timeout, waitDelay, MaxModelsOut, args...)
}

func runAgyProbeCapped(run context.Context, home, agy string, timeout, waitDelay time.Duration, maxOut int, args ...string) (string, error) {
	if maxOut <= 0 {
		maxOut = MaxModelsOut
	}
	overErr := ErrModelsOver
	if maxOut != MaxModelsOut {
		overErr = fmt.Errorf("its output is over %d KiB", maxOut>>10)
	}
	ctx, cancel := context.WithTimeout(run, timeout)
	defer cancel()
	dir, cleanup, err := probeWorkDir(home)
	if err != nil {
		return "", err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, agy, args...)
	cmd.Dir, cmd.Env = dir, rootEnvWithPWD(dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	stdout := trail.NewCappedBuffer(maxOut, overErr)
	stderr := trail.NewCappedBuffer(MaxModelsOut, nil)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	switch {
	case stdout.Truncated():
		err = overErr
	case run.Err() != nil:
		err = errors.New("the run ended before it answered")
	case ctx.Err() == context.DeadlineExceeded:
		err = fmt.Errorf("no answer in %s", timeout)
	}
	if err != nil {
		if l := LastLine(stderr.String()); l != "" {
			err = fmt.Errorf("%v: %s", err, l)
		}
		return "", err
	}
	return stdout.String(), nil
}

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

// ClassifyModel derives family ("gemini"|"anthropic"|"other"),
// tier ("pro"|"flash"|"light"), and effort ("max"|"xhigh"|"high"|"medium"|"low"|"orchestrator"|"")
// from a model ID and display name without hardcoded codename tables.
func ClassifyModel(id, displayName string) (family, tier, effort string) {
	low := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "models/"))
	lowDisp := strings.ToLower(displayName)
	switch {
	case strings.HasPrefix(low, "gemini") || strings.Contains(lowDisp, "gemini"):
		family = "gemini"
	case strings.HasPrefix(low, "claude") || strings.Contains(lowDisp, "claude") || strings.Contains(lowDisp, "anthropic"):
		family = "anthropic"
	default:
		family = "other"
	}
	effort = parseModelEffort(low, lowDisp)
	tier = parseModelTier(low + " " + lowDisp)
	return family, tier, effort
}

func parseModelEffort(low, lowDisp string) string {
	switch {
	case strings.Contains(low, "orchestrator") || strings.Contains(lowDisp, "orchestrator"):
		return "orchestrator"
	case strings.HasSuffix(low, "-xhigh") || strings.Contains(lowDisp, "(xhigh)"):
		return "xhigh"
	case strings.HasSuffix(low, "-max") || strings.Contains(lowDisp, "(max)"):
		return "max"
	case strings.HasSuffix(low, "-high") || strings.Contains(lowDisp, "(high)"):
		return "high"
	case strings.HasSuffix(low, "-medium") || strings.Contains(lowDisp, "(medium)"):
		return "medium"
	case strings.HasSuffix(low, "-low") || strings.Contains(lowDisp, "(low)") ||
		strings.HasSuffix(low, "-minimal") || strings.Contains(lowDisp, "(minimal)"):
		return "low"
	default:
		return ""
	}
}

func parseModelTier(lowAll string) string {
	for _, kw := range []string{"-lite", " lite", "(lite)", "haiku", "-mini", " mini", "-nano", " nano", "-micro", "-small"} {
		if strings.Contains(lowAll, kw) {
			return "light"
		}
	}
	for _, kw := range []string{"flash", "sonnet", "-fast", " fast", "-turbo", "-oss"} {
		if strings.Contains(lowAll, kw) {
			return "flash"
		}
	}
	return "pro"
}
