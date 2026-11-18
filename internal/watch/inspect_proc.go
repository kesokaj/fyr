package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/search"
)

func inspectProcs(o Options, customProcs bool) (int, []Finding, error) {
	procs, err := o.ProcLister()
	if err != nil {
		return 0, nil, err
	}
	candidates := filterProcs(procs, customProcs, o.Dir, o.ConvID)
	var findings []Finding
	for _, p := range candidates {
		if f, ok := classifyProc(p, o.IdleSec); ok {
			findings = append(findings, f)
		}
	}
	return len(candidates), findings, nil
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
	ancestors := buildAncestorSet(procs, self)
	ppidByPID := make(map[int]int, len(procs))
	agyPIDs := make(map[int]bool)
	for _, p := range procs {
		ppidByPID[p.PID] = p.PPID
		if isAgyProcess(p.Cmdline) {
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
	if convID != "" && strings.Contains(cmdline, convID) {
		return true
	}
	return dir != "" && strings.Contains(cmdline, dir)
}

func classifyProc(p ProcInfo, idleSec int) (Finding, bool) {
	wchan := strings.ToLower(strings.TrimSpace(p.Wchan))
	state := strings.ToUpper(strings.TrimSpace(p.State))
	target := compactCmdTarget(p.Cmdline, p.PID)
	switch {
	case strings.Contains(wchan, "tty"):
		return procFinding(kindInteractiveWait, target, p, "wchan="+p.Wchan, "process blocked on terminal I/O; supply stdin or terminate child"), true
	case strings.Contains(wchan, "pipe") && p.ElapsedSec >= int64(idleSec):
		return procFinding(kindInteractiveWait, target, p, "wchan="+p.Wchan, "process blocked on pipe I/O past idle threshold; check stdin or kill"), true
	case strings.Contains(wchan, "wait") && p.ElapsedSec >= int64(idleSec):
		return procFinding(kindStuckNoProgress, target, p, "wchan="+p.Wchan, "child process stuck waiting past idle threshold; inspect or kill"), true
	case strings.HasPrefix(state, "T") && p.ElapsedSec >= int64(idleSec):
		kind := kindStuckNoProgress
		if strings.Contains(state, "+") {
			kind = kindInteractiveWait
		}
		return procFinding(kind, target, p, "state="+p.State, "stopped child process past idle threshold; resume or kill"), true
	case wchan == "" && p.ElapsedSec >= int64(idleSec):
		return classifyDarwinState(p, target, state)
	}
	return Finding{}, false
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
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
