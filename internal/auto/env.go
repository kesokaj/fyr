package auto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/dream"
	"github.com/kesokaj/fyr/internal/search"
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
	// Sessions is logind's sessions of uid, each its show-session keys.
	Sessions(uid int) ([]map[string]string, error)
	PTYs(uid int) ([]time.Time, error)
	Ioreg(args ...string) (string, error)
	AgySession(goos string, uid int) (bool, error)
	// Usage is `agy -p "/usage"`'s stdout, run in dir.
	Usage(home, dir string) (string, error)
	Targets(home string) ([]dream.Target, error)
	// LastConversation is the newest Antigravity conversation of root
	// modified after since; zero when there is none.
	LastConversation(home, root string, since time.Time) (time.Time, error)
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

func (machine) Ioreg(args ...string) (string, error) {
	return tool(toolWait, "", nil, "ioreg", args...)
}

var sessionID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (machine) Sessions(uid int) ([]map[string]string, error) {
	list, err := tool(toolWait, "", nil, "loginctl", "list-sessions", "--no-legend", "--no-pager")
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !sessionID.MatchString(f[0]) {
			continue
		}
		show, err := tool(toolWait, "", nil, "loginctl", "show-session", f[0], "--no-pager",
			"-p", "User", "-p", "Type", "-p", "Active", "-p", "LockedHint", "-p", "IdleHint", "-p", "IdleSinceHint")
		if err != nil {
			continue
		}
		if s := parseSession(show); s["User"] == strconv.Itoa(uid) {
			out = append(out, s)
		}
	}
	return out, nil
}

func parseSession(text string) map[string]string {
	s := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			s[k] = v
		}
	}
	return s
}

func (machine) PTYs(uid int) ([]time.Time, error) { return readPTYs("/dev/pts", uid) }

// readPTYs is the access time of every numbered terminal uid owns.
func readPTYs(root string, uid int) ([]time.Time, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []time.Time
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		if at, owner, ok := accessed(info); ok && owner == uid {
			out = append(out, at)
		}
	}
	return out, nil
}

func (machine) AgySession(goos string, uid int) (bool, error) { return dream.AgySession(goos, uid) }

// Usage runs outside every checkout, in auto/, with HOME the run's home: a
// scratch home has no login, so a development run fails closed and never
// refreshes the real home's token. It starts no turn and leaves no
// conversation (D28).
func (machine) Usage(home, dir string) (string, error) {
	return tool(usageWait, dir, []string{"HOME=" + home}, "agy", "-p", "/usage")
}

func (machine) Targets(home string) ([]dream.Target, error) { return dream.Targets(home) }

func (machine) LastConversation(home, root string, since time.Time) (time.Time, error) {
	convs, _, err := search.AgyConversationText(home, root, since)
	if err != nil {
		return time.Time{}, err
	}
	var last time.Time
	for _, c := range convs {
		if c.Modified.After(last) {
			last = c.Modified
		}
	}
	return last, nil
}
