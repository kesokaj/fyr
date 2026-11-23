//go:build darwin

package main

import (
	"errors"
	"syscall"
	"time"
	"unsafe"
)

const maxSelectFD = 1024

func waitPipeReady(fd uintptr, wait time.Duration) bool {
	if fd >= maxSelectFD {
		return false
	}
	var rfds syscall.FdSet
	bits := uintptr(unsafe.Sizeof(rfds.Bits[0]) * 8)
	idx, shift := fd/bits, fd%bits
	if int(idx) >= len(rfds.Bits) {
		return false
	}
	deadline := time.Now().Add(wait)
	for {
		rem := time.Until(deadline)
		if rem < 0 {
			rem = 0
		}
		rfds = syscall.FdSet{}
		rfds.Bits[idx] |= 1 << shift
		tv := syscall.NsecToTimeval(rem.Nanoseconds())
		err := syscall.Select(int(fd)+1, &rfds, nil, nil, &tv)
		if err == nil {
			return (rfds.Bits[idx] & (1 << shift)) != 0
		}
		if errors.Is(err, syscall.EINTR) && time.Now().Before(deadline) {
			continue
		}
		return false
	}
}
