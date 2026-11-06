// Package sandbox is one unattended CLI session in a throwaway directory:
// the clone, a home of its own, the harness installed into it, the token
// linked for the session, and the safety checks after it. The steps are
// SPEC-fyr-eval § One row's 1, 2, 4, 6-12 and 14; the verb that calls them
// owns the rest (PLAN-2026-10-29-workers § Interfaces).
package sandbox

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// Config is one sandbox: where it is made, what it clones and installs, and
// how its session is bounded.
type Config struct {
	Dir          string                             // the sandbox directory: <Dir>/app the clone, <Dir>/home the HOME
	Source       string                             // the repository cloned into <Dir>/app
	RealHome     string                             // $HOME: check c's fingerprint, the token
	Repo         string                             // install source; "" is the harness embedded in Self
	Self         string                             // the fyr that installs (os.Executable)
	Grants       []string                           // settings.json permissions.allow
	GitName      string                             // agy's GIT_AUTHOR_*/GIT_COMMITTER_*
	GitEmail     string                             //
	Model        string                             // agy --model; "" passes none
	Zone         string                             // agy's TZ, a zone name; "" passes none (work only, workers D144)
	Timeout      time.Duration                      // agy alone (in docker mode its container) is killed after this
	SkipStores   bool                               // check c leaves out every storeEntry (work and eval, D45, D64, D70, ADR-506)
	BrainWatch   func(dbPath string) (string, bool) // with SkipStores, watches brain.db by mode + scope digest (eval, ADR-506)
	PrintTimeout string                             // agy --print-timeout, as "<n>m"
	StopGrace    time.Duration                      // SIGTERM to SIGKILL on ctx's end; 0 is 5s
	Prefix       string                             // every note starts "<Prefix>: "
	Out          io.Writer
	Docker       *Docker // nil: the session runs on the host; else in a container (work only)
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

// CheckLogin checks the operator's Antigravity login under realHome without
// writing to disk, for the host runner and fyr eval: a regular file at
// TokenRel, or on darwin a directory at KeychainsRel (BUG-166). A TokenRel
// that exists and is not a regular file is refused on every OS. It returns
// tok, the path to the token file when one exists, or "" when darwin uses
// Keychains alone. The docker runner's login is CheckWorkLogin (BUG-510).
func CheckLogin(realHome, goos string) (tok string, err error) {
	p := filepath.Join(realHome, filepath.FromSlash(TokenRel))
	st, err := os.Lstat(p)
	if err == nil {
		if !st.Mode().IsRegular() {
			return "", fmt.Errorf("no Antigravity login at %s", p)
		}
		return p, nil
	}
	if goos == "darwin" && errors.Is(err, fs.ErrNotExist) {
		kc := filepath.Join(realHome, filepath.FromSlash(KeychainsRel))
		if info, kerr := os.Lstat(kc); kerr == nil && info.IsDir() {
			return "", nil
		}
		return "", fmt.Errorf("no Antigravity login: neither %s nor a Keychain folder at %s", p, kc)
	}
	return "", fmt.Errorf("no Antigravity login at %s", p)
}

// Root is a verb's directory under fyr's home: "eval" holds eval's
// sandboxes, "work" the work runs' locks and pid files and the docker
// runner's files, and "sandbox" work's sandboxes, three of the sandboxDirs
// check c leaves out (D10, D9 of PLAN-2026-11-02-1806-fyr-speed).
func Root(home, verb string) string { return filepath.Join(home, harness.StateDir, verb) }

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
	c               Config
	dir, app, sb    string
	reasons         []string
	pgids           []int
	realBefore      map[string]string
	credsBefore     map[string]credFinding
	linked          bool
	tokLinked       bool
	keychainsLinked bool
	tok             string   // the real token, as Session was last given it
	keychains       string   // the real Keychains directory on darwin, when present
	keychainFiles   []string // regular files in keychains before Session
	sessions        int
	started         bool // agy was started by a session
	timedOut        bool
	// resolved holds dir, app and sb as guard a approved them. The agent can
	// write in dir, so every later step that follows those paths first checks
	// they still resolve here (intact), and check e matches these strings.
	resolved map[string]string
	// mounts is the chain of mounts above each resolved path at that moment.
	mounts map[string]string
	// root is SB, opened once guard a approved it. A held handle follows the
	// directory through a rename, so the token link is removed from the home
	// it was made in, whatever the path leads to by then.
	root *os.Root
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
func (b *Box) Started() bool { return b.started }

// TimedOut reports that agy was killed at Config.Timeout: a note, not a
// reason, so each verb decides what it means.
func (b *Box) TimedOut() bool { return b.timedOut }

func (b *Box) Fail(reason string) { b.reasons = append(b.reasons, reason) }

func (b *Box) Reasons() []string { return b.reasons }

// Note writes one line, "<Prefix>: " and the message, made printable.
func (b *Box) Note(format string, a ...any) {
	fmt.Fprintln(b.c.Out, Printable(b.c.Prefix+": "+fmt.Sprintf(format, a...)))
}

func (b *Box) Has(reason string) bool {
	for _, r := range b.reasons {
		if r == reason {
			return true
		}
	}
	return false
}

// verb is the prefix's first word, for a line that names no row.
func (b *Box) verb() string {
	v, _, _ := strings.Cut(b.c.Prefix, ":")
	return v
}

// Intact reports whether the sandbox directory, the clone and the sandbox
// home still resolve to what guard a approved, and fails the box once when
// they do not, or when a file system is mounted at, under or above them: the agent
// can replace any of them with a link to the real home, or mount over them,
// and the steps remove, scan and ingest through them.
func (b *Box) Intact() bool {
	for p, want := range b.resolved {
		st, err := os.Lstat(p)
		got, rerr := filepath.EvalSymlinks(p)
		if err != nil || rerr != nil || !st.IsDir() || got != want {
			b.replaced("%s no longer resolves to %s; nothing is removed, run or scanned through it", p, want)
			return false
		}
	}
	if len(b.resolved) == 0 {
		return true
	}
	roots := make([]string, 0, len(b.resolved))
	for _, want := range b.resolved {
		roots = append(roots, want)
	}
	found, err := mountsUnder(roots...)
	if err != nil {
		b.replaced("the mount table could not be read: %v; nothing is removed, run or scanned through the sandbox", err)
		return false
	}
	if len(found) > 0 {
		for i, m := range found {
			found[i] = Printable(m)
		}
		b.replaced("a file system is mounted under the sandbox: %s; nothing is removed, run or scanned through it", strings.Join(found, ", "))
		return false
	}
	chains, err := mountChains(roots...)
	if err != nil {
		b.replaced("the mount table could not be read: %v; nothing is removed, run or scanned through the sandbox", err)
		return false
	}
	for r, chain := range b.mounts {
		if chains[r] != chain {
			b.replaced("a file system is mounted above %s; nothing is removed, run or scanned through it", Printable(r))
			return false
		}
	}
	return true
}

// replaced notes what the agent changed and fails the box sandbox replaced,
// once.
func (b *Box) replaced(format string, a ...any) {
	b.Note(format, a...)
	if !b.Has(ReasonReplaced) {
		b.Fail(ReasonReplaced)
	}
}

// fileFailed fails the box for a file of its own it could not open or
// remove: sandbox write failed when the machine is answerable (machineErr)
// and no session has started, so no agent ran that could have caused it,
// and eval does not grade it; else sandbox replaced, the graded direction:
// a planted link, FIFO, directory or hard link is what a run leaves, and
// after a session the agent, as the operator's uid, can exhaust descriptors
// or memory or mount a file system of its own (PLAN.md items 1 and 48; P6's
// security review, finding 1). Once each.
func (b *Box) fileFailed(err error, format string, a ...any) {
	if b.started || !machineErr(err) {
		b.replaced(format, a...)
		return
	}
	b.Note(format, a...)
	if !b.Has(ReasonWrite) {
		b.Fail(ReasonWrite)
	}
}

// machineErrs are the errors of the machine, not of what is at the path.
var machineErrs = []error{syscall.ENOSPC, syscall.EDQUOT, syscall.EIO, syscall.EROFS, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM}

func machineErr(err error) bool {
	for _, e := range machineErrs {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// sandboxRoot is the held os.Root on SB, or one opened now when Clone did
// not get that far. An os.Root refuses a path that a link leads out of the
// home (the real token sits at the other end of one). done closes only a
// root opened here.
func (b *Box) sandboxRoot() (root *os.Root, done func(), err error) {
	if b.root != nil {
		return b.root, func() {}, nil
	}
	root, err = os.OpenRoot(b.sb)
	if err != nil {
		return nil, nil, err
	}
	return root, func() { root.Close() }, nil
}

// removeInSandbox removes rel inside the sandbox home, never through a link
// that leads out of it; one that does fails the box sandbox replaced.
func (b *Box) removeInSandbox(rel string) {
	root, done, err := b.sandboxRoot()
	if err == nil {
		err = root.Remove(filepath.FromSlash(rel))
		done()
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		b.fileFailed(err, "not removed: %s: %v", rel, err)
	}
}
