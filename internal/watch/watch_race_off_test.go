//go:build !race

package watch

import "time"

const watch40MBReadBudget = 25 * time.Millisecond
