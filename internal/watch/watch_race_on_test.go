//go:build race

package watch

import "time"

// Under -race, pure-Go SQLite executes significantly slower than normal builds;
// keep strict 25ms budget on non-race builds while still catching full-file
// 40 MB copy regressions under -race.
const watch40MBReadBudget = 250 * time.Millisecond
