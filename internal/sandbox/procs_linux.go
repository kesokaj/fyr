package sandbox

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcsChecked reports whether safety check e runs on this system (O1).
const ProcsChecked = true

// leftoverProcs is safety check e (preamble :179-215, BUG-163): a process of
// this uid whose cwd or executable lies under one of roots, or whose
// environment holds exactly HOME=<home>, outlived the CLI. The verb itself and
// its ancestors are never matched. Each match gets SIGTERM, is reported as
// "pid (kind) name" with the executable's name sanitised (argv can carry a
// token), and fails the row. Every /proc read tolerates a vanished process.
func leftoverProcs(w io.Writer, verb, home string, roots ...string) []string {
	skip := map[int]bool{}
	for p := os.Getpid(); p > 1 && !skip[p]; p = ppid(p) {
		skip[p] = true
	}
	uid := strconv.Itoa(os.Getuid())
	entries, _ := os.ReadDir("/proc")
	var found []string
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || skip[pid] {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		if procField(dir, "Uid:") != uid {
			continue
		}
		cwd, _ := os.Readlink(filepath.Join(dir, "cwd"))
		exe, _ := os.Readlink(filepath.Join(dir, "exe"))
		hit := ""
		for _, r := range roots {
			if cwd != "" && within(cwd, r) {
				hit = "cwd"
			}
			if exe != "" && strings.HasPrefix(exe, r+string(filepath.Separator)) {
				hit = "exe"
			}
		}
		// An exact NUL-separated entry: a HOME that only starts with the
		// sandbox home is another run's.
		if env, err := os.ReadFile(filepath.Join(dir, "environ")); hit == "" && err == nil {
			for _, kv := range bytes.Split(env, []byte{0}) {
				if string(kv) == "HOME="+home {
					hit = "home"
					break
				}
			}
		}
		if hit == "" {
			continue
		}
		found = append(found, fmt.Sprintf("%d (%s) %s", pid, hit, cleanName(filepath.Base(exe))))
		syscall.Kill(pid, syscall.SIGTERM)
	}
	if len(found) > 0 {
		time.Sleep(time.Second)
	}
	return found
}

// procField is the first value of a /proc/<pid>/status line, or "".
func procField(dir, key string) string {
	b, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, key); ok {
			if f := strings.Fields(v); len(f) > 0 {
				return f[0]
			}
		}
	}
	return ""
}

func ppid(pid int) int {
	n, _ := strconv.Atoi(procField(filepath.Join("/proc", strconv.Itoa(pid)), "PPid:"))
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
