package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcsChecked reports whether safety check e runs on this system (O1).
const ProcsChecked = true

// procDir is /proc; a test points it at a fixture.
var procDir = "/proc"

// leftoverProcs is safety check e (preamble :179-215, BUG-163): a process of
// this uid whose cwd or executable lies under one of roots, or whose
// environment holds exactly HOME=<home>, outlived the CLI. The verb itself and
// its ancestors are never matched. Each match gets SIGTERM, is reported as
// "pid (kind) name" with the executable's name sanitised (argv can carry a
// token), and fails the row. Every /proc read tolerates a vanished process
// or one it may not read (a non-dumpable process of this uid, as ssh-agent);
// any other failure comes back as the error, beside what was found, since an
// agent can exhaust descriptors or memory to hide from the check (PLAN.md
// item 49).
func leftoverProcs(w io.Writer, verb, home string, roots ...string) ([]string, error) {
	skip := map[int]bool{}
	for p := os.Getpid(); p > 1 && !skip[p]; p = ppid(p) {
		skip[p] = true
	}
	uid := strconv.Itoa(os.Getuid())
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}
	var found []string
	var unread error
	note := func(err error) {
		if err != nil && !procGone(err) {
			unread = err
		}
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || skip[pid] {
			continue
		}
		dir := filepath.Join(procDir, e.Name())
		owner, err := procField(dir, "Uid:")
		note(err)
		if owner != uid {
			continue
		}
		hit, exe := procHit(dir, home, note, roots)
		if hit == "" {
			continue
		}
		found = append(found, fmt.Sprintf("%d (%s) %s", pid, hit, cleanName(filepath.Base(exe))))
		syscall.Kill(pid, syscall.SIGTERM)
	}
	if len(found) > 0 {
		time.Sleep(time.Second)
	}
	return found, unread
}

// procHit says how the process at dir matches, "" for not at all, and its
// executable; note takes every read's error.
func procHit(dir, home string, note func(error), roots []string) (hit, exe string) {
	cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
	note(err)
	exe, err = os.Readlink(filepath.Join(dir, "exe"))
	note(err)
	for _, r := range roots {
		if cwd != "" && within(cwd, r) {
			hit = "cwd"
		}
		if exe != "" && strings.HasPrefix(exe, r+string(filepath.Separator)) {
			hit = "exe"
		}
	}
	if hit != "" {
		return hit, exe
	}
	// An exact NUL-separated entry: a HOME that only starts with the
	// sandbox home is another run's.
	env, err := os.ReadFile(filepath.Join(dir, "environ"))
	note(err)
	for _, kv := range bytes.Split(env, []byte{0}) {
		if string(kv) == "HOME="+home {
			return "home", exe
		}
	}
	return "", exe
}

// procGone reports a read that says nothing against the process: it exited,
// or it is not readable by design.
func procGone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) ||
		errors.Is(err, fs.ErrPermission)
}

// procField is the first value of a /proc/<pid>/status line, "" when it has
// none, or the read's error.
func procField(dir, key string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, key); ok {
			if f := strings.Fields(v); len(f) > 0 {
				return f[0], nil
			}
		}
	}
	return "", nil
}

func ppid(pid int) int {
	v, _ := procField(filepath.Join(procDir, strconv.Itoa(pid)), "PPid:")
	n, _ := strconv.Atoi(v)
	return n
}

// cleanName keeps the characters the preamble kept, at most 40 of them.
func cleanName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._+-", r)) && b.Len() < 40 {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "?"
	}
	return b.String()
}
