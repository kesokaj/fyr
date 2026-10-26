//go:build !linux

package eval

import (
	"fmt"
	"io"
)

// ProcsChecked reports whether safety check e runs on this system: not
// without /proc (O1). The final group kill of step 15 still runs, and the
// record names the gap.
const ProcsChecked = false

func leftoverProcs(w io.Writer, home string, roots ...string) []string {
	fmt.Fprintln(w, "eval: no /proc here: the leftover-process check did not run")
	return nil
}
