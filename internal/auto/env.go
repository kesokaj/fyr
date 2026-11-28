package auto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/trail"
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

var errToolOutputLimit = errors.New("output over the limit")

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
	out := trail.NewCappedBuffer(toolLimit, errToolOutputLimit)
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), nil
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

// gatePower is G3: why this machine is not on AC, or "". A supply it cannot
// read fails closed; a box with no supplies counts as AC. A supply of scope
// Device is a mouse's or a headset's battery, never the machine's.
func gatePower(env Env, goos string) string {
	switch goos {
	case "linux":
		return gatePowerLinux(env)
	case "darwin":
		return gatePowerDarwin(env)
	}
	return "G3 power: no reading on " + goos
}

func gatePowerLinux(env Env) string {
	supplies, err := env.Supplies()
	if err != nil {
		return "G3 power: unreadable: " + oneLine(err.Error())
	}
	battery, online := false, false
	for _, s := range supplies {
		switch {
		case s.Scope == "Device":
		case s.Type == "Battery":
			battery = true
		case s.Online == "1":
			online = true
		}
	}
	if battery && !online {
		return "G3 power: on battery"
	}
	return ""
}

func gatePowerDarwin(env Env) string {
	batt, err := env.Pmset("-g", "batt")
	if err != nil {
		return "G3 power: unreadable: " + oneLine(err.Error())
	}
	if !strings.Contains(batt, "'AC Power'") {
		return "G3 power: not on AC power"
	}
	all, err := env.Pmset("-g")
	if err != nil {
		return "G3 power: unreadable: " + oneLine(err.Error())
	}
	if lowPower.MatchString(all) {
		return "G3 power: low power mode"
	}
	return ""
}

var lowPower = regexp.MustCompile(`(?m)^\s*lowpowermode\s+1\s*$`)

// machineStop runs G3 (AC power). G4 (operator presence) and G5 (active agy
// session) are retired (ADR-524).
func (r *run) machineStop() string {
	return gatePower(r.env, r.o.goos())
}
