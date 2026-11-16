//go:build darwin

package isolate

import (
	"fmt"
	"os/exec"
)

var darwinLookPath = exec.LookPath

// MaybeRunInit is a no-op on macOS because sandbox-exec wraps exec directly.
func MaybeRunInit() {}

func checkOSAvailable() error {
	if _, err := darwinLookPath("sandbox-exec"); err != nil {
		return fmt.Errorf("sandbox-exec unavailable: %w", err)
	}
	return nil
}

func prepareIsolatedCmd(pol Policy) (*exec.Cmd, func(), error) {
	if err := checkOSAvailable(); err != nil {
		return nil, nil, err
	}
	if len(pol.Argv) == 0 || pol.Argv[0] == "" {
		return nil, nil, fmt.Errorf("isolate: empty argv")
	}
	prof, err := renderSeatbeltProfile(pol)
	if err != nil {
		return nil, nil, err
	}
	args := append([]string{"-p", prof}, pol.Argv...)
	cmd := exec.Command("sandbox-exec", args...)
	cmd.Dir = pol.Dir
	cmd.Env = pol.Env
	return cmd, func() {}, nil
}
