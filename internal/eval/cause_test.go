package eval

import (
	"testing"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// TestClassify: the status and cause of a trial from its reasons, and the
// order one cause is chosen when several reasons are given.
func TestClassify(t *testing.T) {
	for name, c := range map[string]struct {
		reasons       []string
		timedOut      bool
		status, cause string
	}{
		"no reason":                    {nil, false, StatusPass, ""},
		"timed out, the check held":    {nil, true, StatusPass, ""},
		"the check":                    {[]string{ReasonCheck}, false, StatusFail, "check"},
		"the check after a timeout":    {[]string{ReasonCheck}, true, StatusFail, "timeout"},
		"the agent":                    {[]string{ReasonAgent("reviewer")}, false, StatusFail, "agent"},
		"check and agent":              {[]string{ReasonCheck, ReasonAgent("reviewer")}, false, StatusFail, "agent"},
		"a leak behind a failed check": {[]string{ReasonCheck, ReasonCredentials}, false, StatusFail, "creds"},
		"a leak after a timeout":       {[]string{ReasonCheck, ReasonCredentials}, true, StatusFail, "creds"},
		"config before procs":          {[]string{ReasonProcs, ReasonRealConfig}, false, StatusFail, "config"},
		"grant before denied":          {[]string{ReasonDenied, ReasonInvalid}, false, StatusFail, "grant"},
		"a leak wins over an error":    {[]string{ReasonReplaced, ReasonProcs}, false, StatusFail, "procs"},
		"an error wins over a grant":   {[]string{ReasonDenied, ReasonInterrupted}, false, StatusError, "interrupted"},
		"the first error is named":     {[]string{ReasonSetup, ReasonInterrupted}, false, StatusError, "setup"},
		"interrupted":                  {[]string{ReasonInterrupted}, false, StatusError, "interrupted"},
		"an unnamed reason is graded":  {[]string{"something new"}, false, StatusFail, "check"},
	} {
		if s, cause := classify(c.reasons, c.timedOut); s != c.status || cause != c.cause {
			t.Errorf("%s: %s (%s), want %s (%s)", name, s, cause, c.status, c.cause)
		}
	}
}

// TestClassifyEveryReason: every reason eval and internal/sandbox give has
// its own cause, so none falls through to the graded default, and each
// cause is an identifier the trials table accepts.
func TestClassifyEveryReason(t *testing.T) {
	want := map[string][2]string{
		ReasonExists: {StatusError, "exists"}, ReasonClone: {StatusError, "clone"}, ReasonRecords: {StatusError, "records"},
		ReasonSetup: {StatusError, "setup"}, ReasonInstall: {StatusError, "install"}, ReasonSettings: {StatusError, "settings"},
		ReasonToken: {StatusError, "token"}, ReasonReplaced: {StatusError, "replaced"}, ReasonInterrupted: {StatusError, "interrupted"},
		ReasonCheck: {StatusFail, "check"}, ReasonDenied: {StatusFail, "denied"}, ReasonInvalid: {StatusFail, "grant"},
		ReasonProcs: {StatusFail, "procs"}, ReasonRealConfig: {StatusFail, "config"}, ReasonCredentials: {StatusFail, "creds"},
		ReasonAgent("x"): {StatusFail, "agent"},
	}
	// The sandbox's own constants, named here so a new one shows up as a
	// missing key rather than a silent default.
	for _, r := range []string{sandbox.ReasonExists, sandbox.ReasonClone, sandbox.ReasonSetup, sandbox.ReasonInstall, sandbox.ReasonSettings,
		sandbox.ReasonToken, sandbox.ReasonDenied, sandbox.ReasonInvalid, sandbox.ReasonProcs, sandbox.ReasonRealConfig,
		sandbox.ReasonCredentials, sandbox.ReasonReplaced} {
		if _, ok := want[r]; !ok {
			t.Errorf("sandbox reason %q has no cause here", r)
		}
	}
	for r, w := range want {
		s, cause := classify([]string{r}, false)
		if s != w[0] || cause != w[1] {
			t.Errorf("%q: %s (%s), want %s (%s)", r, s, cause, w[0], w[1])
		}
	}
}
