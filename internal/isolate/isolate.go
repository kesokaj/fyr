package isolate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	RunnerIsolate = "isolate"
	RunnerAuto    = "auto"

	// DefaultRunner is OS-native isolation (Landlock on Linux, Seatbelt on macOS).
	DefaultRunner = RunnerIsolate

	// IsolateInitEnv enables MaybeRunInit inside the child process before exec.
	IsolateInitEnv = "FYR_ISOLATE_INIT"
)

// Policy describes filesystem paths and process launch parameters enforced
// by the platform sandbox before executing an unattended CLI child.
type Policy struct {
	Argv    []string `json:"argv"`
	Dir     string   `json:"dir"`
	Env     []string `json:"env"`
	RWPaths []string `json:"rw_paths,omitempty"`
	ROPaths []string `json:"ro_paths,omitempty"`
	Network bool     `json:"network,omitempty"`
}

// CheckEvalRunner validates --runner for `fyr eval` (`isolate` or empty).
func CheckEvalRunner(r string) error {
	switch r {
	case "", RunnerIsolate:
		return nil
	default:
		return fmt.Errorf("--runner %q: want isolate", r)
	}
}

// ResolveEvalRunner resolves an eval runner flag and fails closed if OS isolation
// is unavailable when `isolate` is selected.
func ResolveEvalRunner(r string) (string, error) {
	if err := CheckEvalRunner(r); err != nil {
		return "", err
	}
	if err := CheckAvailable(); err != nil {
		return "", fmt.Errorf("--runner isolate: %w", err)
	}
	return RunnerIsolate, nil
}

// CheckAvailable reports whether the current OS supports fail-closed sandbox confinement.
func CheckAvailable() error {
	return checkOSAvailable()
}

// ChooseRunner resolves a worker runner choice fail-closed without falling back
// to an unconfined host process (PRD R8, R10).
func ChooseRunner(requested string, isolateAvail func() error) (runner, why string, err error) {
	if isolateAvail == nil {
		isolateAvail = CheckAvailable
	}
	if requested == "" {
		requested = DefaultRunner
	}
	switch requested {
	case RunnerIsolate, RunnerAuto:
		return chooseIsolateRunner(isolateAvail)
	default:
		return "", "", fmt.Errorf("--runner %q: want auto or isolate", requested)
	}
}

func chooseIsolateRunner(isolateAvail func() error) (string, string, error) {
	if err := isolateAvail(); err != nil {
		s := strings.TrimSpace(err.Error())
		if s == "" || strings.HasPrefix(s, "--runner isolate:") {
			return "", "", err
		}
		return "", "", fmt.Errorf("--runner isolate: %w", err)
	}
	return RunnerIsolate, RunnerIsolate, nil
}

var systemROCandidates = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt",
	"/proc", "/sys", "/run/systemd/resolve",
	"/var/lib/ca-certificates", "/var/cache/ldconfig",
	"/System", "/Library", "/Applications", "/private/etc", "/private/var/db", "/private/var/select",
}

var safeCharDevs = []string{
	"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty", "/dev/ptmx",
}

func (b *Box) isolated() bool {
	return !b.c.TestUnconfined
}

func policyPaths(realHome, bin string, baseRW, extraRW, baseRO, extraRO []string) (rw, ro []string) {
	rw = appendUniqueClean(nil, baseRW...)
	for _, dev := range safeCharDevs {
		if _, err := os.Stat(dev); err == nil {
			rw = appendUniqueClean(rw, dev)
		}
	}
	rw = appendUniqueClean(rw, extraRW...)

	ro = appendAgyROPaths(nil, realHome, bin)
	ro = appendExistingPaths(ro, baseRO)
	ro = appendExistingPaths(ro, systemROCandidates)
	if gr := resolveGoRoot(); gr != "" {
		ro = appendExistingPaths(ro, []string{gr})
	}
	ro = appendSafePATHDirs(ro, realHome)
	ro = appendUniqueClean(ro, extraRO...)
	return rw, ro
}

func (b *Box) policyFor(bin string, args []string) Policy {
	rw, ro := policyPaths(
		b.c.RealHome, bin,
		[]string{b.app, b.sb, filepath.Join(b.dir, "tmp")}, b.c.TestRW,
		[]string{b.tok}, b.c.TestRO,
	)
	return Policy{
		Argv:    append([]string{bin}, args...),
		Dir:     b.app,
		Env:     b.agyEnv(),
		RWPaths: rw,
		ROPaths: ro,
		Network: true,
	}
}

func addPathPair(dst []string, p string) []string {
	if p == "" {
		return dst
	}
	dst = appendUniqueClean(dst, p)
	if r, err := filepath.EvalSymlinks(p); err == nil && r != p {
		dst = appendUniqueClean(dst, r)
	}
	return dst
}

func appendExistingPaths(dst, candidates []string) []string {
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			dst = addPathPair(dst, c)
		}
	}
	return dst
}

func appendSafePATHDirs(dst []string, realHome string) []string {
	realHome = effectiveRealHome(realHome)
	tmpRoot := filepath.Clean(os.TempDir())
	resTmp, _ := resolve(tmpRoot)
	var resHome string
	if realHome != "" {
		resHome, _ = resolve(realHome)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		abs := filepath.Clean(dir)
		resAbs, err := resolve(abs)
		if err != nil || isExcludedPATHDir(abs, resAbs, tmpRoot, resTmp, realHome, resHome) {
			continue
		}
		if st, err := os.Stat(resAbs); err == nil && st.IsDir() {
			dst = addPathPair(dst, abs)
		}
	}
	return dst
}

func overlapsPath(p, root string) bool {
	return p != "" && root != "" && (within(p, root) || within(root, p))
}

func isExcludedPATHDir(abs, resAbs, tmpRoot, resTmp, realHome, resHome string) bool {
	if filepath.Clean(abs) == string(filepath.Separator) || filepath.Clean(resAbs) == string(filepath.Separator) {
		return true
	}
	if overlapsPath(abs, tmpRoot) || overlapsPath(resAbs, resTmp) {
		return true
	}
	if realHome != "" && overlapsPath(abs, filepath.Clean(realHome)) {
		return true
	}
	return overlapsPath(resAbs, resHome)
}

func appendUniqueClean(dst []string, paths ...string) []string {
	seen := make(map[string]bool, len(dst)+len(paths))
	for _, p := range dst {
		seen[p] = true
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		clean := filepath.Clean(p)
		if !seen[clean] {
			seen[clean] = true
			dst = append(dst, clean)
		}
	}
	return dst
}

func (b *Box) buildAgentCmd(bin string, args []string) (*exec.Cmd, func(), error) {
	if !b.isolated() {
		cmd := exec.Command(bin, args...)
		cmd.Dir = b.app
		cmd.Env = b.agyEnv()
		return cmd, func() {}, nil
	}
	if !filepath.IsAbs(bin) {
		resolved, err := exec.LookPath(bin)
		if err != nil {
			return nil, nil, fmt.Errorf("isolate binary %s: %w", bin, err)
		}
		abs, err := filepath.Abs(resolved)
		if err != nil {
			return nil, nil, fmt.Errorf("isolate binary %s: %w", bin, err)
		}
		bin = abs
	}
	if err := b.ensureAgyWrapper(bin); err != nil {
		return nil, nil, err
	}
	return prepareIsolatedCmd(b.policyFor(bin, args))
}

func (b *Box) ensureAgyWrapper(bin string) error {
	if !isAgyExecutableName(bin) {
		return nil
	}
	binDir := filepath.Join(b.sb, "bin")
	if st, err := os.Lstat(binDir); err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	dst := filepath.Join(binDir, "agy")
	if _, err := os.Lstat(dst); err == nil {
		return nil
	}
	script := "#!/usr/bin/env bash\nexec " + ShellQuote(bin) + " \"$@\"\n"
	if err := os.WriteFile(dst, []byte(script), 0o755); err != nil {
		return fmt.Errorf("isolate write agy wrapper %s: %w", dst, err)
	}
	return nil
}
