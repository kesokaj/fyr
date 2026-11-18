//go:build !linux && !darwin

package watch

func listProcs() ([]ProcInfo, error) {
	return nil, nil
}
