//go:build !race

package brain

import "time"

const turnRecordsWarmBudget = 120 * time.Millisecond
