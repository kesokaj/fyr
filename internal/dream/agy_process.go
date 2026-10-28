package dream

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// procRoot is where the Linux process check reads; a test points it at a
// fake tree.
var procRoot = "/proc"

// psArgs is the macOS process check's read: every process of uid, one
// command line per line; a test replaces it. -ww keeps a long command line
// whole.
var psArgs = func(uid int) ([]byte, error) {
	return exec.Command("ps", "-ww", "-U", strconv.Itoa(uid), "-o", "args=").Output()
}

// agySubcommands are the first arguments `agy --help` lists as subcommands
// (agy 1.4.9, plan D21): a process running one is not a person at a session,
// and `remote-control` is a daemon that would hold the check true forever.
var agySubcommands = map[string]bool{
	"agent": true, "agents": true, "changelog": true, "help": true, "install": true,
	"mcp": true, "mic-serve": true, "models": true, "plugin": true, "plugins": true,
	"remote-control": true, "update": true,
}

// agySession reports whether an interactive agy session runs under uid
// (R3's exception, plan D9). The real uid is read from each process's
// status, never from its directory's owner, which is root for a
// non-dumpable process; an entry that cannot be read is skipped.
func agySession(goos string, uid int) (bool, error) {
	switch goos {
	case "linux":
		return procSession(uid)
	case "darwin":
		out, err := psArgs(uid)
		if err != nil {
			return false, fmt.Errorf("the agy session check: ps: %w", err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			// ps joins the arguments with spaces: a path holding one splits,
			// and that process is not counted, toward not dreaming.
			if f := strings.Fields(line); len(f) > 0 && filepath.Base(f[0]) == "agy" && interactive(f[1:]) {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("the agy session check: no process check on %s", goos)
}

// procSession is agySession over procRoot.
func procSession(uid int) (bool, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return false, fmt.Errorf("the agy session check: %w", err)
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		if owner, ok := realUID(dir); !ok || owner != uid {
			continue
		}
		args := cmdline(dir)
		if isAgy(dir, args) && len(args) > 0 && interactive(args[1:]) {
			return true, nil
		}
	}
	return false, nil
}

// realUID is the first field of the Uid: line of dir/status.
func realUID(dir string) (int, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			if f := strings.Fields(rest); len(f) > 0 {
				n, err := strconv.Atoi(f[0])
				return n, err == nil
			}
		}
	}
	return 0, false
}

// cmdline is dir/cmdline split at its NUL bytes; nil when it cannot be read.
func cmdline(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(data) == 0 {
		return nil
	}
	return strings.Split(string(bytes.TrimSuffix(data, []byte{0})), "\x00")
}

// isAgy reports whether the process is agy by its first argument or by its
// exe link: `agy update` replaces the binary under a live session, and the
// link then ends in " (deleted)".
func isAgy(dir string, args []string) bool {
	if len(args) > 0 && filepath.Base(args[0]) == "agy" {
		return true
	}
	exe, err := os.Readlink(filepath.Join(dir, "exe"))
	return err == nil && filepath.Base(strings.TrimSuffix(exe, " (deleted)")) == "agy"
}

// interactive reports whether agy's arguments make a session a person sits
// at: no -p, --print or --prompt anywhere (a headless run: the dream's own
// child, fyr eval, a script), and no subcommand as the first non-flag
// argument. Go's flag package takes one dash or two and a value after =.
func interactive(args []string) bool {
	first := ""
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			if first == "" {
				first = a
			}
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name == "p" || name == "print" || name == "prompt" {
			return false
		}
	}
	return !agySubcommands[first]
}
