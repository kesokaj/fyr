//go:build unix

package check

import "syscall"

// nonBlock opens without waiting for a writer: a FIFO swapped in after a
// check returns at once and is refused by the opened file's Stat (security
// review 2026-10-25, finding 5; internal/brain's openNonBlocking is the same
// guard for trail.jsonl).
const nonBlock = syscall.O_NONBLOCK
