package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// BoxHandle is the minimal sandbox lifecycle contract a worker drives on
// isolate.Box without importing package isolate.
type BoxHandle interface {
	Dir() string
	App() string
	Home() string
	Make() bool
	ScratchApp() bool
	Clone(ctx context.Context) bool
	Fingerprint()
	Install(ctx context.Context) bool
	Settings() bool
	Baseline()
	Session(ctx context.Context, agy, tok, prompt, outName string) int
	Started() bool
	After() bool
	ExitChecks()
	TimedOut() bool
	Intact() bool
	GitIntact() bool
	Reasons() []string
	Fail(reason string)
	Note(format string, a ...any)
	Has(reason string) bool
	Remove() bool
}

// BoxConfig carries worker sandbox parameters passed to Deps.NewBox.
type BoxConfig struct {
	Dir            string
	Source         string
	RealHome       string
	Repo           string
	Self           string
	Grants         []string
	GitName        string
	GitEmail       string
	Model          string
	Zone           string
	Timeout        time.Duration
	SkipStores     bool
	HostOS         string
	PrintTimeout   string
	StopGrace      time.Duration
	Prefix         string
	Out            io.Writer
	ACP            bool
	TestRO         []string
	TestRW         []string
	TestUnconfined bool
}

// JudgeConfinedRequest holds parameters for constructing an OS-confined judge command.
type JudgeConfinedRequest struct {
	RealHome    string
	Bin         string
	Args        []string
	Dir         string
	AbsRepo     string
	ScratchHome string
	TmpDir      string
	Tok         string
	Env         []string
}

// JudgeDeps injects OS-confinement and auth-staging helpers from package isolate
// into RunJudgePrint without creating an import cycle.
type JudgeDeps struct {
	OAuthRel           string
	WorkAuthRel        string
	EffectiveHostOS    func(override string) string
	SyncAuth           func(home, realHome, goos string) (string, error)
	StageModelsCatalog func(realHome, scratchHome string) error
	BuildConfinedCmd   func(req JudgeConfinedRequest) (*exec.Cmd, func(), error)
	WithoutGit         func(env []string) []string
	Printable          func(s string) string
}

// Deps injects isolate sandbox primitives into worker orchestration.
type Deps struct {
	Judge           JudgeDeps
	JudgeExec       func(context.Context, JudgeExecOptions) (JudgeExecOutput, error)
	SafetyReasons   []string
	Root            func(home, verb string) string
	SandboxRoot     func(home, root string) string
	CheckRoot       func(realHome, repo, root string) error
	CheckWorkDir    func(home string) error
	OutsideCheckout func(dir string) error
	ChooseRunner    func(requested string, isolateAvail func() error) (runner, why string, err error)
	CheckAvailable  func() error
	CheckWorkLogin  func(home, realHome, goos string) (string, error)
	PrepareSelf     func(ctx context.Context, repo, self string) (string, func())
	OpenRegular     func(path string, flag int) (*os.File, error)
	FyrEnv          func() []string
	NewBox          func(cfg BoxConfig) BoxHandle
}

func (d Deps) printable(s string) string {
	if d.Judge.Printable != nil {
		return d.Judge.Printable(s)
	}
	return s
}

func (d Deps) fyrEnv() []string {
	if d.FyrEnv != nil {
		return d.FyrEnv()
	}
	return os.Environ()
}

func (d Deps) openRegular(path string, flag int) (*os.File, error) {
	if d.OpenRegular != nil {
		return d.OpenRegular(path, flag)
	}
	return os.OpenFile(path, flag, 0o600)
}

func (d JudgeDeps) withoutGit(env []string) []string {
	if d.WithoutGit != nil {
		return d.WithoutGit(env)
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.HasPrefix(k, "GIT_") {
			out = append(out, kv)
		}
	}
	return out
}

func coalesceStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func setWorkerCmdSetsid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

func cmdExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
