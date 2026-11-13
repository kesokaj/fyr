package work

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

var (
	ansiCSIRe  = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]`)
	twoSpaceRe = regexp.MustCompile(`  +`)
)

// modelsTimeout bounds the agy models call (D125), a variable so a test
// can shorten it.
var modelsTimeout = time.Minute

// maxModelsOut caps each of the call's stdout and stderr (D125).
const maxModelsOut = 64 << 10

var errModelsOver = fmt.Errorf("its output is over %d KiB", maxModelsOut>>10)

// checkModels is refusal 21: when the run names a model, in a
// Kickoff's Model: line or --model, every one must be an ID the host's agy
// models lists. A failed call or an empty list refuses too: fail closed
// (D103 item 3). A run naming none makes no call.
func checkModels(ctx context.Context, o *Options, ks []Kickoff) error {
	named := o.Model != ""
	for _, k := range ks {
		named = named || k.Model != "" || k.JudgeModel != ""
	}
	if !named {
		return nil
	}
	// With --runner docker refusal 10 was not made: the check is the host's.
	agy, err := exec.LookPath("agy")
	if err != nil {
		return errors.New("the model check runs the host's agy: agy not found on PATH")
	}
	ids, err := listModels(ctx, agy)
	if err != nil {
		return fmt.Errorf("the model check: agy models: %v", err)
	}
	if len(ids) == 0 {
		return errors.New("the model check: agy models listed no model")
	}
	listed := map[string]bool{}
	for _, id := range ids {
		listed[id] = true
	}
	if err := resolveAutoModels(ctx, o, ks, listed, ids); err != nil {
		return err
	}
	all := strings.Join(ids, ", ")
	for _, k := range ks {
		if k.Model != "" && !listed[k.Model] {
			return fmt.Errorf("the Kickoff %s names the model %s, which agy models does not list: %s", k.Path, k.Model, all)
		}
		if k.JudgeModel != "" && !listed[k.JudgeModel] {
			return fmt.Errorf("the Kickoff %s names the judge model %s, which agy models does not list: %s", k.Path, k.JudgeModel, all)
		}
	}
	if o.Model != "" && !listed[o.Model] {
		return fmt.Errorf("--model %s: agy models does not list it: %s", o.Model, all)
	}
	return nil
}

// listModels runs `agy models` in / with the verb's environment, stdin from
// the null device, in its own session and process group (Setsid: true, detaching
// /dev/tty so SSH terminal probes never stop on SIGTTIN/SIGTTOU), which the
// deadline or the run's end kills, and which is killed after the call too, so
// no helper it left runs on (D125); the IDs are parsed by ParseModelsOutput.
func listModels(run context.Context, agy string) ([]string, error) {
	raw, err := runAgyModels(run, agy)
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

func runAgyModels(run context.Context, agy string) (string, error) {
	ctx, cancel := context.WithTimeout(run, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, agy, "models")
	cmd.Dir = "/"
	cmd.Env = rootEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &capped{over: errModelsOver}, &capped{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	switch {
	case stdout.full:
		err = errModelsOver
	case run.Err() != nil:
		err = errors.New("the run ended before it answered")
	case ctx.Err() == context.DeadlineExceeded:
		err = fmt.Errorf("no answer in %s", modelsTimeout)
	}
	if err != nil {
		if l := lastLine(stderr.String()); l != "" {
			err = fmt.Errorf("%v: %s", err, l)
		}
		return "", err
	}
	return stdout.String(), nil
}

// rootEnv is the verb's environment with PWD saying / and ANTIGRAVITY_*
// stripped: an explicit Env keeps the inherited PWD (the Phase 8a security
// review), and stripping ANTIGRAVITY_* prevents nested daemon/TTY interference.
func rootEnv() []string {
	var out []string
	for _, kv := range sandbox.FyrEnv() {
		k, _, _ := strings.Cut(kv, "=")
		if k != "PWD" && k != "OLDPWD" && !strings.HasPrefix(k, "ANTIGRAVITY_") {
			out = append(out, kv)
		}
	}
	return append(out, "PWD=/")
}

// capped keeps up to maxModelsOut bytes. Past that it returns over, which
// ends the copy and so the child's pipe, or with over nil drops the rest.
// The buffer is a field, not embedded: its ReadFrom would let io.Copy pass
// Write by.
type capped struct {
	buf  bytes.Buffer
	over error
	full bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := maxModelsOut - c.buf.Len(); len(p) > room {
		c.buf.Write(p[:max(room, 0)])
		c.full = true
		if c.over != nil {
			return 0, c.over
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *capped) String() string { return c.buf.String() }

// lastLine is s's last non-blank line, past any carriage return a spinner
// left in it.
func lastLine(s string) string {
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
		applyModelProfile(&entry)
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
	if !modelRe.MatchString(id) || isHeaderRow(id, disp) {
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
