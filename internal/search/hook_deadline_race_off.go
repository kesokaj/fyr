//go:build !race

package search

import "time"

const (
	defaultHookDeadline      = 200 * time.Millisecond
	defaultHookSliceDeadline = 120 * time.Millisecond
)
