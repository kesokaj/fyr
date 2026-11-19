//go:build linux

package watch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	linuxClkTck       = 100
	unknownLinuxWchan = "unknown"
)

var procDir = "/proc"

func listProcs() ([]ProcInfo, error) {
	return listProcsFrom(procDir, os.Getuid())
}

func listProcsFrom(root string, uid int) ([]ProcInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	uptime := readProcUptime(filepath.Join(root, "uptime"))
	var out []ProcInfo
	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if !matchProcUID(ent, uid) {
			continue
		}
		info, ok, err := readLinuxProc(filepath.Join(root, ent.Name()), pid, uptime)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, info)
		}
	}
	return out, nil
}

func matchProcUID(ent os.DirEntry, uid int) bool {
	if uid < 0 {
		return true
	}
	fi, err := ent.Info()
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return !ok || int(st.Uid) == uid
}

func readLinuxProc(dir string, pid int, uptime float64) (ProcInfo, bool, error) {
	statRaw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		if isProcSkipErr(err) {
			return ProcInfo{}, false, nil
		}
		return ProcInfo{}, false, err
	}
	state, ppid, startTicks, ok := parseProcStat(statRaw)
	if !ok {
		return ProcInfo{}, false, nil
	}
	cmdRaw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		if isProcSkipErr(err) {
			return ProcInfo{}, false, nil
		}
		return ProcInfo{}, false, err
	}
	wchan, ok, err := readProcWchan(filepath.Join(dir, "wchan"))
	if err != nil || !ok {
		return ProcInfo{}, false, err
	}
	return ProcInfo{
		PID:        pid,
		PPID:       ppid,
		State:      state,
		Wchan:      wchan,
		Cmdline:    strings.TrimSpace(string(bytes.ReplaceAll(cmdRaw, []byte{0}, []byte{' '}))),
		ElapsedSec: calcProcElapsed(uptime, startTicks),
	}, true, nil
}

func isProcSkipErr(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.ESRCH)
}

func readProcWchan(path string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return "", false, nil
		}
		if errors.Is(err, os.ErrPermission) {
			return unknownLinuxWchan, true, nil
		}
		return "", false, err
	}
	wchan := strings.TrimSpace(string(raw))
	if wchan == "" {
		wchan = unknownLinuxWchan
	}
	return wchan, true, nil
}

func calcProcElapsed(uptime float64, startTicks int64) int64 {
	if uptime <= 0 || startTicks < 0 {
		return 0
	}
	elapsed := int64(uptime) - startTicks/linuxClkTck
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func parseProcStat(raw []byte) (state string, ppid int, startTicks int64, ok bool) {
	idx := bytes.LastIndexByte(raw, ')')
	if idx < 0 {
		return "", 0, 0, false
	}
	fields := strings.Fields(string(raw[idx+1:]))
	if len(fields) < 20 {
		return "", 0, 0, false
	}
	ppid, err1 := strconv.Atoi(fields[1])
	startTicks, err2 := strconv.ParseInt(fields[19], 10, 64)
	if err1 != nil || err2 != nil {
		return "", 0, 0, false
	}
	return fields[0], ppid, startTicks, true
}

func readProcUptime(path string) float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}
