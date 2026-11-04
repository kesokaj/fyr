package work

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
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
func checkModels(ctx context.Context, o Options, ks []Kickoff) error {
	named := o.Model != ""
	for _, k := range ks {
		named = named || k.Model != ""
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
	all := strings.Join(ids, ", ")
	for _, k := range ks {
		if k.Model != "" && !listed[k.Model] {
			return fmt.Errorf("the Kickoff %s names the model %s, which agy models does not list: %s", k.Path, k.Model, all)
		}
	}
	if o.Model != "" && !listed[o.Model] {
		return fmt.Errorf("--model %s: agy models does not list it: %s", o.Model, all)
	}
	return nil
}

// listModels runs `agy models` in / with the verb's environment, stdin from
// the null device, in its own process group, which the deadline or the
// run's end kills, and which is killed after the call too, so no helper it
// left runs on (D125); the IDs are each stdout line's text before its
// first tab.
func listModels(run context.Context, agy string) ([]string, error) {
	ctx, cancel := context.WithTimeout(run, modelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, agy, "models")
	cmd.Dir = "/"
	cmd.Env = rootEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
		return nil, err
	}
	var ids []string
	for _, l := range strings.Split(stdout.String(), "\n") {
		if id, _, ok := strings.Cut(l, "\t"); ok && strings.TrimSpace(id) != "" {
			ids = append(ids, strings.TrimSpace(id))
		}
	}
	return ids, nil
}

// rootEnv is the verb's environment with PWD saying /: an explicit Env
// keeps the inherited PWD, which names the lead's checkout (the Phase 8a
// security review).
func rootEnv() []string {
	var out []string
	for _, kv := range sandbox.FyrEnv() {
		if k, _, _ := strings.Cut(kv, "="); k != "PWD" && k != "OLDPWD" {
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
