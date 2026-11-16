//go:build !linux && !darwin

package isolate

import (
	"fmt"
	"os/exec"
)

// MaybeRunInit is a no-op on unsupported operating systems.
func MaybeRunInit() {}

func checkOSAvailable() error {
	return fmt.Errorf("OS isolation unavailable on %s", hostOS)
}

func prepareIsolatedCmd(Policy) (*exec.Cmd, func(), error) {
	return nil, nil, checkOSAvailable()
}
