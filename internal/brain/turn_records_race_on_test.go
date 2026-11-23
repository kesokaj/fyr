//go:build race

package brain

import "time"

// Under -race with 20-way parallel package tests, pure-Go SQLite runs ~25-30x
// slower than normal builds; scale the warm-load ceiling proportionally while
// keeping the strict 120ms budget on non-race builds.
const turnRecordsWarmBudget = 600 * time.Millisecond
