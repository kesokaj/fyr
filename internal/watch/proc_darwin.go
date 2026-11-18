//go:build darwin

package watch

import (
	"os/exec"
	"strconv"
	"strings"
)

var psBin = "/bin/ps"

func listProcs() ([]ProcInfo, error) {
	cmd := exec.Command(psBin, "-axo", "pid,ppid,stat,etime,command")
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseDarwinPs(string(out)), nil
}

func parseDarwinPs(out string) []ProcInfo {
	var procs []ProcInfo
	for _, line := range strings.Split(out, "\n") {
		if info, ok := parseDarwinPsLine(line); ok {
			procs = append(procs, info)
		}
	}
	return procs
}

func parseDarwinPsLine(line string) (ProcInfo, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return ProcInfo{}, false
	}
	pid, err1 := strconv.Atoi(fields[0])
	ppid, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil || pid <= 0 {
		return ProcInfo{}, false
	}
	cmdline := extractFifthFieldTail(line)
	return ProcInfo{
		PID:        pid,
		PPID:       ppid,
		State:      fields[2],
		Cmdline:    cmdline,
		ElapsedSec: parseElapsed(fields[3]),
	}, true
}

func extractFifthFieldTail(line string) string {
	s := strings.TrimLeft(line, " \t")
	for range 4 {
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			return ""
		}
		s = strings.TrimLeft(s[i:], " \t")
	}
	return strings.TrimSpace(s)
}

func parseElapsed(etime string) int64 {
	var days int64
	rest := strings.TrimSpace(etime)
	if dStr, after, ok := strings.Cut(rest, "-"); ok {
		if d, err := strconv.ParseInt(dStr, 10, 64); err == nil {
			days = d
		}
		rest = after
	}
	parts := strings.Split(rest, ":")
	switch len(parts) {
	case 2:
		m, _ := strconv.ParseInt(parts[0], 10, 64)
		s, _ := strconv.ParseInt(parts[1], 10, 64)
		return days*86400 + m*60 + s
	case 3:
		h, _ := strconv.ParseInt(parts[0], 10, 64)
		m, _ := strconv.ParseInt(parts[1], 10, 64)
		s, _ := strconv.ParseInt(parts[2], 10, 64)
		return days*86400 + h*3600 + m*60 + s
	case 1:
		s, _ := strconv.ParseInt(parts[0], 10, 64)
		return days*86400 + s
	default:
		return 0
	}
}
