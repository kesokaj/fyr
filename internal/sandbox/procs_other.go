//go:build !linux

package sandbox

import (
	"fmt"
	"io"
)

// ProcsChecked reports whether safety check e runs on this system: not
// without /proc (O1). The final group kill of step 14 still runs, and the
// record names the gap.
const ProcsChecked = false

func leftoverProcs(w io.Writer, verb, home string, roots ...string) ([]string, error) {
	fmt.Fprintln(w, verb+": no /proc here: the leftover-process check did not run")
	return nil, nil
}
