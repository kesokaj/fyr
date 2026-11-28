//go:build race

package hooks

import "time"

const (
	// DefaultHookDeadline is scaled under -race to avoid scheduler starvation flakes.
	DefaultHookDeadline                = 2 * time.Second
	defaultHookSliceDeadline           = 1200 * time.Millisecond
	preInvocationTimeoutRecorderBudget = 450 * time.Millisecond
)
