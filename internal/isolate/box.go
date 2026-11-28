// Package isolate is one unattended CLI session in a throwaway directory:
// the clone, a home of its own, the harness installed into it, OS-level
// isolation (Landlock / sandbox-exec),
// the token linked for the session, and the safety checks after it.
package isolate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kesokaj/fyr/internal/harness"
)

// Config is one sandbox: where it is made, what it clones and installs, and
// how its session is bounded.
type Config struct {
	Dir            string                             // the sandbox directory: <Dir>/app the clone, <Dir>/home the HOME
	Source         string                             // the repository cloned into <Dir>/app
	RealHome       string                             // $HOME: check c's fingerprint, the token
	Repo           string                             // install source; "" is the harness embedded in Self
	Self           string                             // the fyr that installs (os.Executable)
	Grants         []string                           // settings.json permissions.allow
	GitName        string                             // agy's GIT_AUTHOR_*/GIT_COMMITTER_*
	GitEmail       string                             //
	Model          string                             // agy --model; "" passes none
	Zone           string                             // agy's TZ, a zone name; "" passes none (workers D144)
	Timeout        time.Duration                      // agy is killed after this
	IdleTimeout    time.Duration                      // agy is aborted early if transcript/artifacts stall past this
	SkipStores     bool                               // check c leaves out every storeEntry (work and eval, D45, D64, D70, ADR-506)
	BrainWatch     func(dbPath string) (string, bool) // with SkipStores, watches brain.db by mode + scope digest (eval, ADR-506)
	PrintTimeout   string                             // agy --print-timeout, as "<n>m"
	StopGrace      time.Duration                      // SIGTERM to SIGKILL on ctx's end; 0 is 5s
	Prefix         string                             // every note starts "<Prefix>: "
	Out            io.Writer
	ACP            bool           // true: drive agy via `agy acp --stdio --process-per-session`
	OnEvent        func(ACPEvent) // optional live step callback when ACP is true
	HostOS         string         // optional GOOS override for tests; "" uses runtime.GOOS (hostOS)
	TestRO         []string       // Go-only read-only test rig paths for OS isolation
	TestRW         []string       // Go-only read-write test rig paths for OS isolation
	TestUnconfined bool           // Go-only escape hatch for real-home tripwire unit tests
}

// The fixed reason strings of the steps this package runs (SPEC-fyr-eval
// § One row, step 15).
const (
	ReasonExists      = "sandbox exists"
	ReasonClone       = "clone"
	ReasonSetup       = "setup failed"
	ReasonInstall     = "install"
	ReasonSettings    = "settings"
	ReasonToken       = "token link"
	ReasonDenied      = "auto-denied"
	ReasonInvalid     = "invalid allow entry"
	ReasonProcs       = "processes left"
	ReasonRealConfig  = "real config changed"
	ReasonCredentials = "credentials left in the sandbox"
	ReasonReplaced    = "sandbox replaced"
	ReasonWrite       = "sandbox write failed"
)

// IntegrityReasons say a sandbox cannot be read at all, for the one list
// eval's ingest and work share (PLAN.md item 51): it is another invocation's,
// something outlived the run and can still swap it, or it was replaced or
// never fully written. Work adds its own reasons to fetch nothing.
var IntegrityReasons = []string{ReasonExists, ReasonProcs, ReasonReplaced, ReasonWrite}

// Compromised reports whether reasons hold one of IntegrityReasons.
func Compromised(reasons []string) bool {
	for _, r := range reasons {
		if slices.Contains(IntegrityReasons, r) {
			return true
		}
	}
	return false
}

// TokenRel is the CLI's login token under a home.
const TokenRel = ".gemini/antigravity-cli/antigravity-oauth-token"

// KeychainsRel is the macOS Keychain directory under a home, where agy keeps
// its login on darwin (BUG-166).
const KeychainsRel = "Library/Keychains"

// hostOS is runtime.GOOS, a variable so a test can run the darwin branch.
var hostOS = runtime.GOOS

// SetHostOS overrides the host OS seen by Session in tests and returns a
// function that restores the previous value.
func SetHostOS(goos string) func() {
	old := hostOS
	hostOS = goos
	return func() { hostOS = old }
}

func effectiveHostOS(override string) string {
	if override != "" {
		return override
	}
	return hostOS
}

// Root is a verb's directory under fyr's home: "eval" holds eval's
// sandboxes, "work" the work runs' locks and pid files and the docker
// runner's files, and "sandbox" work's sandboxes, three of the sandboxDirs
// check c leaves out (D10, D9 of PLAN-2026-11-02-1806-fyr-speed).
func Root(home, verb string) string { return filepath.Join(home, harness.StateDir, verb) }

// ProjectName returns a safe single-segment directory name for projectRoot.
func ProjectName(projectRoot string) string {
	if projectRoot == "" {
		return "default"
	}
	b := filepath.Base(filepath.Clean(projectRoot))
	if b == "" || b == "." || b == ".." || b == string(filepath.Separator) || strings.HasPrefix(b, "-") || strings.IndexFunc(b, unicode.IsControl) >= 0 {
		return "default"
	}
	return b
}

// ProjectRoot is where a verb makes its per-project sandboxes under fyr's
// home: <home>/.config/fyr/<kind>/<project> (D9 of PLAN-2026-11-02-1806-fyr-speed).
func ProjectRoot(home, kind, projectRoot string) string {
	return filepath.Join(Root(home, kind), ProjectName(projectRoot))
}

// CheckRoot is the refusal of a sandbox root before anything is made: guards
// a and b on its nearest existing ancestor (BUG-106).
func CheckRoot(realHome, repo, root string) error {
	if err := checkRoots(realHome, repo, root); err != nil {
		return fmt.Errorf("the sandbox root: %v", err)
	}
	if err := outsideCheckout(nearestExisting(root)); err != nil {
		return fmt.Errorf("the sandbox root %s is inside a git checkout (BUG-106); use a --home outside every checkout", root)
	}
	return nil
}

// OutsideCheckout is safety check b: nil only when git run in dir finds no
// repository; it fails closed.
func OutsideCheckout(dir string) error { return outsideCheckout(dir) }

// Box is one sandbox's state as the steps fill it.
type Box struct {
	c            Config
	dir, app, sb string
	reasons      []string
	pgids        []int
	realBefore   map[string]string
	credsBefore  map[string]credFinding
	linked       bool
	tokLinked    bool
	tok          string // the real token, as Session was last given it
	sessions     int
	started      bool // agy was started by a session
	timedOut     bool
	// resolved holds dir, app and sb as guard a approved them. The agent can
	// write in dir, so every later step that follows those paths first checks
	// they still resolve here (intact), and check e matches these strings.
	resolved map[string]string
	// mounts is the chain of mounts above each resolved path at that moment.
	mounts map[string]string
	// root is SB, opened once guard a approved it. A held handle follows the
	// directory through a rename, so the token link is removed from the home
	// it was made in, whatever the path leads to by then.
	root    *os.Root
	acpProc *acpProcess
	mu      sync.Mutex
	outMu   sync.Mutex
}

// New is the Box of c; nothing is made until Make.
func New(c Config) *Box {
	if c.StopGrace == 0 {
		c.StopGrace = 5 * time.Second
	}
	return &Box{c: c, dir: c.Dir, app: filepath.Join(c.Dir, "app"), sb: filepath.Join(c.Dir, "home")}
}

func (b *Box) Dir() string  { return b.dir }
func (b *Box) App() string  { return b.app }
func (b *Box) Home() string { return b.sb }

// Started reports that a session started agy: a token link or an exec that
// failed leaves it false.
func (b *Box) Started() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started
}

func (b *Box) setStarted() {
	b.mu.Lock()
	b.started = true
	b.mu.Unlock()
}

// TimedOut reports that agy was killed at Config.Timeout: a note, not a
// reason, so each verb decides what it means.
func (b *Box) TimedOut() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.timedOut
}

func (b *Box) setTimedOut() {
	b.mu.Lock()
	b.timedOut = true
	b.mu.Unlock()
}

func (b *Box) Fail(reason string) {
	b.mu.Lock()
	b.reasons = append(b.reasons, reason)
	b.mu.Unlock()
}

func (b *Box) failOnce(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !slices.Contains(b.reasons, reason) {
		b.reasons = append(b.reasons, reason)
	}
}

func (b *Box) Reasons() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.reasons)
}

func (b *Box) currentACP() *acpProcess {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acpProc
}

func (b *Box) setACP(p *acpProcess) {
	b.mu.Lock()
	b.acpProc = p
	b.mu.Unlock()
}

func (b *Box) takeACP() *acpProcess {
	b.mu.Lock()
	p := b.acpProc
	b.acpProc = nil
	b.mu.Unlock()
	return p
}

// Note writes one line, "<Prefix>: " and the message, made printable.
func (b *Box) Note(format string, a ...any) {
	if b.c.Out == nil {
		return
	}
	b.outMu.Lock()
	defer b.outMu.Unlock()
	fmt.Fprintln(b.c.Out, Printable(b.c.Prefix+": "+fmt.Sprintf(format, a...)))
}

func (b *Box) Has(reason string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Contains(b.reasons, reason)
}

// verb is the prefix's first word, for a line that names no row.
func (b *Box) verb() string {
	v, _, _ := strings.Cut(b.c.Prefix, ":")
	return v
}
