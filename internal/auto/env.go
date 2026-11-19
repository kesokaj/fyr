package auto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
)

// toolLimit bounds what a machine tool may print; toolWait and usageWait are
// how long one may take: a gate never waits on a hung tool.
const (
	toolLimit = 4 << 20
	toolWait  = 15 * time.Second
	usageWait = 60 * time.Second
)

// Env is every reading a gate takes of the machine, so a test can give each
// one in both states. The real one is machine.
type Env interface {
	Supplies() ([]Supply, error)
	Pmset(args ...string) (string, error)
	// Usage is `agy -p "/usage"`'s stdout, run in dir.
	Usage(home, dir string) (string, error)
	Targets(home string) ([]dream.Target, error)
}

// Supply is one entry of /sys/class/power_supply.
type Supply struct{ Name, Type, Online, Scope string }

type machine struct{}

const supplyRoot = "/sys/class/power_supply"

func (machine) Supplies() ([]Supply, error) { return readSupplies(supplyRoot) }

func readSupplies(root string) ([]Supply, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Supply
	for _, e := range entries {
		// Past the entry's own link, which sysfs makes: a supply that cannot be
		// typed fails G3 closed.
		kind, err := os.ReadFile(filepath.Join(root, e.Name(), "type"))
		if err != nil {
			return nil, err
		}
		online, _ := os.ReadFile(filepath.Join(root, e.Name(), "online"))
		scope, _ := os.ReadFile(filepath.Join(root, e.Name(), "scope"))
		out = append(out, Supply{e.Name(), strings.TrimSpace(string(kind)), strings.TrimSpace(string(online)), strings.TrimSpace(string(scope))})
	}
	return out, nil
}

// capped refuses output past its limit, which ends the tool.
type capped struct{ b strings.Builder }

func (c *capped) Write(p []byte) (int, error) {
	if c.b.Len()+len(p) > toolLimit {
		return 0, errors.New("output over the limit")
	}
	return c.b.Write(p)
}

// tool runs one machine tool under a deadline, in the C locale, and returns
// its stdout.
func tool(wait time.Duration, dir string, env []string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.WaitDelay = dir, time.Second
	// Its own group, so a deadline ends what the tool started too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.Env = append(append(os.Environ(), "LC_ALL=C"), env...)
	var out capped
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return out.b.String(), nil
}

func (machine) Pmset(args ...string) (string, error) {
	return tool(toolWait, "", nil, "pmset", args...)
}

// Usage runs outside every checkout, in auto/, with HOME the run's home: a
// scratch home has no login, so a development run fails closed and never
// refreshes the real home's token. It starts no turn and leaves no
// conversation (D28).
func (machine) Usage(home, dir string) (string, error) {
	return tool(usageWait, dir, []string{"HOME=" + home}, "agy", "-p", "/usage")
}

func (machine) Targets(home string) ([]dream.Target, error) { return dream.Targets(home) }
