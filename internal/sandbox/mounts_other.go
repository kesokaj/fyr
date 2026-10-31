//go:build !linux

package sandbox

// Without /proc no mount is seen; the specs state the gap.
func mountsUnder(roots ...string) ([]string, error) { return nil, nil }

func mountChains(roots ...string) (map[string]string, error) { return nil, nil }
