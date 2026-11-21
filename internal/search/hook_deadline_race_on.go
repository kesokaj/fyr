//go:build race

package search

import "time"

const (
	defaultHookDeadline      = 2 * time.Second
	defaultHookSliceDeadline = 1200 * time.Millisecond
)
