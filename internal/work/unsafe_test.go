package work

import (
	"io"
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// TestUnsafeReasons: a worker failed by a safety check, a replaced sandbox,
// a moved lead or a sandbox the machine could not write is unsafe, so
// nothing of it is fetched and its sandbox is kept, as a failed write was
// before it had its own reason (PLAN.md item 48); a failed check is not.
func TestUnsafeReasons(t *testing.T) {
	for reason, want := range map[string]bool{
		sandbox.ReasonCredentials: true, sandbox.ReasonRealConfig: true, sandbox.ReasonProcs: true,
		sandbox.ReasonReplaced: true, sandbox.ReasonWrite: true, ReasonLeadChanged: true,
		sandbox.ReasonSetup: false,
	} {
		w := &worker{b: sandbox.New(sandbox.Config{Out: io.Discard})}
		w.b.Fail(reason)
		if got := w.unsafe(); got != want {
			t.Errorf("%q: unsafe %v, want %v", reason, got, want)
		}
	}
}
