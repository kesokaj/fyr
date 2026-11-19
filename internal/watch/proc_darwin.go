//go:build darwin

package watch

import (
	"os"
	"os/exec"
)

var psBin = "/bin/ps"

func listProcs() ([]ProcInfo, error) {
	cmd := exec.Command(psBin, "-axo", "uid,pid,ppid,stat,etime,command")
	cmd.Dir = "/"
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseDarwinPsForUID(string(out), os.Getuid()), nil
}
