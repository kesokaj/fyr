//go:build !linux && !darwin

package main

import "time"

func waitPipeReady(uintptr, time.Duration) bool {
	return false
}
