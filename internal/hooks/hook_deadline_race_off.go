//go:build !race

package hooks

import "time"

const (
	// DefaultHookDeadline is the production wall-clock hook deadline.
	DefaultHookDeadline                = 200 * time.Millisecond
	defaultHookSliceDeadline           = 120 * time.Millisecond
	preInvocationTimeoutRecorderBudget = 45 * time.Millisecond
)
