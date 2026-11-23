package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/redact"
	"github.com/kesokaj/fyr/internal/search"
)

func inspectProcsFrom(o Options, customProcs bool, procs []ProcInfo) (int, []Finding) {
	candidates := filterProcs(procs, customProcs, o.Dir, o.ConvID)
	var findings []Finding
	for _, p := range candidates {
		if f, ok := classifyProc(p, o.IdleSec); ok {
			findings = append(findings, f)
		}
	}
	return len(candidates), findings
}

func procTableLiveness(procs []ProcInfo) (inspectable, hasLiveAgy bool) {
	if procs == nil {
		return false, false
	}
	for _, p := range procs {
		if isAgyProcess(p.Cmdline) && isLiveProcState(p.State) {
			return true, true
		}
	}
	return true, false
}

func isLiveProcState(state string) bool {
	s := strings.ToUpper(strings.TrimSpace(state))
	return !strings.HasPrefix(s, "Z") && !strings.HasPrefix(s, "X")
}

func filterProcs(procs []ProcInfo, customProcs bool, dir, convID string) []ProcInfo {
	self := os.Getpid()
	if customProcs {
		var out []ProcInfo
		for _, p := range procs {
			if p.PID != self {
				out = append(out, p)
			}
		}
		return out
	}
	hasTarget := dir != "" || convID != ""
	ancestors := buildAncestorSet(procs, self)
	ppidByPID := make(map[int]int, len(procs))
	agyPIDs := make(map[int]bool)
	for _, p := range procs {
		ppidByPID[p.PID] = p.PPID
		if isAgyProcess(p.Cmdline) && (!hasTarget || cmdMatchesTarget(p.Cmdline, dir, convID)) {
			agyPIDs[p.PID] = true
		}
	}
	var out []ProcInfo
	for _, p := range procs {
		if p.PID == self || ancestors[p.PID] {
			continue
		}
		if hasAncestorIn(p.PPID, ppidByPID, agyPIDs) || p.PPID == self || cmdMatchesTarget(p.Cmdline, dir, convID) {
			out = append(out, p)
		}
	}
	return out
}

func buildAncestorSet(procs []ProcInfo, self int) map[int]bool {
	ppids := make(map[int]int, len(procs))
	for _, p := range procs {
		ppids[p.PID] = p.PPID
	}
	skip := map[int]bool{self: true}
	for cur := self; cur > 1; {
		parent, ok := ppids[cur]
		if !ok || parent <= 1 || skip[parent] {
			break
		}
		skip[parent] = true
		cur = parent
	}
	return skip
}

func hasAncestorIn(ppid int, ppids map[int]int, roots map[int]bool) bool {
	seen := map[int]bool{}
	for cur := ppid; cur > 1 && !seen[cur]; {
		if roots[cur] {
			return true
		}
		seen[cur] = true
		next, ok := ppids[cur]
		if !ok {
			break
		}
		cur = next
	}
	return false
}

func isAgyProcess(cmdline string) bool {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return false
	}
	base := strings.ToLower(filepath.Base(fields[0]))
	return base == "agy" || base == "antigravity" || strings.HasPrefix(base, "antigravity-")
}

func cmdMatchesTarget(cmdline, dir, convID string) bool {
	if convID != "" && containsBoundedToken(cmdline, convID, isConvIdentChar) {
		return true
	}
	if dir == "" {
		return false
	}
	clean := strings.TrimRight(dir, string(filepath.Separator))
	if clean == "" {
		clean = dir
	}
	if containsBoundedToken(cmdline, clean, isPathContChar) {
		return true
	}
	if abs, err := filepath.Abs(clean); err == nil && abs != clean && containsBoundedToken(cmdline, abs, isPathContChar) {
		return true
	}
	real, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return false
	}
	real = filepath.Clean(real)
	return real != clean && containsBoundedToken(cmdline, real, isPathContChar)
}

func containsBoundedToken(s, tok string, cont func(byte) bool) bool {
	for off := 0; off < len(s); {
		idx := strings.Index(s[off:], tok)
		if idx < 0 {
			return false
		}
		start := off + idx
		end := start + len(tok)
		okLeft := start == 0 || !cont(s[start-1])
		okRight := end == len(s) || !cont(s[end])
		if okLeft && okRight {
			return true
		}
		off = start + 1
	}
	return false
}

func isConvIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_'
}

func isPathContChar(b byte) bool {
	return isConvIdentChar(b) || b == '.'
}

func classifyProc(p ProcInfo, idleSec int) (Finding, bool) {
	wchan := strings.ToLower(strings.TrimSpace(p.Wchan))
	state := strings.ToUpper(strings.TrimSpace(p.State))
	target := compactCmdTarget(p.Cmdline, p.PID)
	running := strings.HasPrefix(state, "R")
	isAgy := isAgyProcess(p.Cmdline)
	switch {
	case strings.Contains(wchan, "tty") && !running:
		return procFinding(kindInteractiveWait, target, p, "wchan="+p.Wchan, "process blocked on terminal I/O; supply stdin or terminate child"), true
	case strings.Contains(wchan, "pipe") && !running && !isAgy && p.ElapsedSec >= int64(idleSec):
		return procFinding(kindInteractiveWait, target, p, "wchan="+p.Wchan, "process blocked on pipe I/O past idle threshold; check stdin or kill"), true
	case isBlockedWaitWchan(wchan) && !running && !isAgy && p.ElapsedSec >= int64(idleSec):
		return procFinding(kindStuckNoProgress, target, p, "wchan="+p.Wchan, "child process stuck waiting past idle threshold; inspect or kill"), true
	case strings.HasPrefix(state, "T") && p.ElapsedSec >= int64(idleSec):
		kind := kindStuckNoProgress
		if strings.Contains(state, "+") {
			kind = kindInteractiveWait
		}
		return procFinding(kind, target, p, "state="+p.State, "stopped child process past idle threshold; resume or kill"), true
	case runtime.GOOS == "darwin" && wchan == "" && !running && !isAgy && p.ElapsedSec >= int64(idleSec):
		return classifyDarwinState(p, target, state)
	}
	return Finding{}, false
}

func isBlockedWaitWchan(wchan string) bool {
	if !strings.Contains(wchan, "wait") {
		return false
	}
	for _, normal := range []string{"futex", "epoll", "poll", "select", "nanosleep", "hrtimer", "woken"} {
		if strings.Contains(wchan, normal) {
			return false
		}
	}
	return true
}

func classifyDarwinState(p ProcInfo, target, state string) (Finding, bool) {
	if strings.Contains(state, "+") {
		return procFinding(kindInteractiveWait, target, p, "state="+p.State, "foreground process waiting past idle threshold; check stdin or kill"), true
	}
	for _, prefix := range []string{"S", "I", "U", "T"} {
		if strings.HasPrefix(state, prefix) {
			return procFinding(kindStuckNoProgress, target, p, "state="+p.State, "child process idle past threshold; inspect or kill"), true
		}
	}
	return Finding{}, false
}

func procFinding(kind, target string, p ProcInfo, hint, action string) Finding {
	hint = redact.Redact(hint)
	return Finding{
		Kind:       kind,
		Target:     target,
		PID:        p.PID,
		IdleSec:    p.ElapsedSec,
		PromptHint: search.Printable(hint),
		Action:     action,
	}
}

func compactCmdTarget(cmdline string, pid int) string {
	s := search.Printable(strings.Join(strings.Fields(cmdline), " "))
	if s == "" {
		return fmt.Sprintf("pid:%d", pid)
	}
	s = redact.Redact(s)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func parseDarwinPsForUID(out string, uid int) []ProcInfo {
	var procs []ProcInfo
	for _, line := range strings.Split(out, "\n") {
		if info, ok := parseDarwinPsLineForUID(line, uid); ok {
			procs = append(procs, info)
		}
	}
	return procs
}

func parseDarwinPsLineForUID(line string, uid int) (ProcInfo, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return ProcInfo{}, false
	}
	parsedUID, err0 := strconv.Atoi(fields[0])
	pid, err1 := strconv.Atoi(fields[1])
	ppid, err2 := strconv.Atoi(fields[2])
	if err0 != nil || parsedUID != uid || err1 != nil || err2 != nil || pid <= 0 {
		return ProcInfo{}, false
	}
	return ProcInfo{
		PID:        pid,
		PPID:       ppid,
		State:      fields[3],
		Cmdline:    extractNthFieldTail(line, 5),
		ElapsedSec: parseElapsed(fields[4]),
	}, true
}

func extractNthFieldTail(line string, skip int) string {
	s := strings.TrimLeft(line, " \t")
	for range skip {
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
