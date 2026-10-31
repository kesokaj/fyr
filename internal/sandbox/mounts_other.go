//go:build !linux

package sandbox

// MountsChecked reports whether a mount under the sandbox is seen on this
// system: not without /proc, as ProcsChecked.
const MountsChecked = false

func mountsUnder(roots ...string) ([]string, error) { return nil, nil }
