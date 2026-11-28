//go:build unix

package harness

import (
	"os"
	"syscall"
	"testing"
)

type fakeInfo struct {
	os.FileInfo
	sys any
}

func (f fakeInfo) Sys() any { return f.sys }

func TestOwnedByCaller(t *testing.T) {
	t.Parallel()
	mine := &syscall.Stat_t{Uid: uint32(os.Geteuid())}
	other := &syscall.Stat_t{Uid: uint32(os.Geteuid() + 1)}
	if !ownedByCaller(fakeInfo{sys: mine}) {
		t.Error("own uid reads as not owned")
	}
	if ownedByCaller(fakeInfo{sys: other}) {
		t.Error("another uid reads as owned")
	}
	if !ownedByCaller(fakeInfo{sys: nil}) {
		t.Error("unknown ownership must read as owned (the full remedy, as before)")
	}
}
