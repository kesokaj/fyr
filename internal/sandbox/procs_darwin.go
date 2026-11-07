//go:build darwin

package sandbox

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcsChecked reports whether safety check e runs on this system.
const ProcsChecked = true

// procDir is /usr/sbin/lsof on darwin; a test points it at a missing path to
// verify check e fails closed when the process table cannot be inspected.
var (
	procDir = "/usr/sbin/lsof"
	psBin   = "/bin/ps"
)

type darwinProc struct {
	ppid int
	cwd  string
	exe  string
	env  string
}

// leftoverProcs is safety check e on darwin (BUG-163): it inspects every
// process of this uid via lsof (-d cwd,txt) and ps (-Eww) to find processes
// whose cwd or executable lies under roots, or whose environment holds
// HOME=<home>, excluding this process and its ancestors. Each match gets
// SIGTERM and is reported as "pid (kind) name". If lsof or ps cannot produce
// the process table, it returns an error so check e fails closed.
func leftoverProcs(w io.Writer, verb, home string, roots ...string) ([]string, error) {
	procs, err := readDarwinProcs(roots)
	if err != nil {
		return nil, err
	}
	skip := map[int]bool{}
	for p := os.Getpid(); p > 1 && !skip[p]; {
		skip[p] = true
		if dp := procs[p]; dp != nil {
			p = dp.ppid
		} else {
			break
		}
	}
	pids := make([]int, 0, len(procs))
	for pid := range procs {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	var found []string
	for _, pid := range pids {
		if skip[pid] {
			continue
		}
		dp := procs[pid]
		hit := darwinHit(dp, home, roots)
		if hit == "" {
			continue
		}
		found = append(found, fmt.Sprintf("%d (%s) %s", pid, hit, cleanName(filepath.Base(dp.exe))))
		syscall.Kill(pid, syscall.SIGTERM)
	}
	if len(found) > 0 {
		time.Sleep(time.Second)
	}
	return found, nil
}

func readDarwinProcs(roots []string) (map[int]*darwinProc, error) {
	uid := strconv.Itoa(os.Getuid())
	lsofCmd := exec.Command(procDir, "-n", "-P", "-w", "-a", "-u", uid, "-d", "cwd,txt", "-F", "pRfn")
	lsofCmd.Dir = "/"
	lsofCmd.Env = WithoutGit(os.Environ())
	lsofOut, lsofErr := lsofCmd.Output()
	if len(lsofOut) == 0 {
		return nil, fmt.Errorf("lsof: %v", lsofErr)
	}
	psCmd := exec.Command(psBin, "-Eww", "-U", uid, "-o", "pid=,ppid=,comm=,command=")
	psCmd.Dir = "/"
	psCmd.Env = WithoutGit(os.Environ())
	psOut, psErr := psCmd.Output()
	if psErr != nil || len(psOut) == 0 {
		return nil, fmt.Errorf("ps: %v", psErr)
	}
	procs := map[int]*darwinProc{}
	parseLsof(string(lsofOut), roots, procs)
	parsePs(string(psOut), procs)
	return procs, nil
}

func parseLsof(out string, roots []string, procs map[int]*darwinProc) {
	var cur *darwinProc
	var fd string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 2 {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			pid, err := strconv.Atoi(val)
			if err != nil {
				cur = nil
				continue
			}
			cur = procs[pid]
			if cur == nil {
				cur = &darwinProc{}
				procs[pid] = cur
			}
		case 'R':
			if cur != nil {
				cur.ppid, _ = strconv.Atoi(val)
			}
		case 'f':
			fd = val
		case 'n':
			if cur == nil {
				continue
			}
			if fd == "cwd" {
				cur.cwd = val
			} else if fd == "txt" && (cur.exe == "" || hasRootPrefix(val, roots)) {
				cur.exe = val
			}
		}
	}
}

func parsePs(out string, procs map[int]*darwinProc) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		dp := procs[pid]
		if dp == nil {
			dp = &darwinProc{}
			procs[pid] = dp
		}
		if dp.ppid == 0 {
			dp.ppid = ppid
		}
		if dp.exe == "" {
			dp.exe = fields[2]
		}
		dp.env = line
	}
}

func hasRootPrefix(p string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(p, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func darwinHit(dp *darwinProc, home string, roots []string) string {
	hit := ""
	for _, r := range roots {
		if dp.cwd != "" && within(dp.cwd, r) {
			hit = "cwd"
		}
		if dp.exe != "" && strings.HasPrefix(dp.exe, r+string(filepath.Separator)) {
			hit = "exe"
		}
	}
	if hit != "" {
		return hit
	}
	want := "HOME=" + home
	for _, tok := range strings.Fields(dp.env) {
		if tok == want {
			return "home"
		}
	}
	return ""
}
