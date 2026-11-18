package isolate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
)

// CommandOpts configures a confined (or test-unconfined) child command.
type CommandOpts struct {
	Dir, ScratchHome, TmpDir, RealHome string
	Argv, ExtraEnv, ROPaths, RWPaths   []string
	Unconfined                         bool
}

// Command returns an *exec.Cmd running opts.Argv in opts.Dir with a strict
// allowlist environment and OS sandbox confinement (Network: false) unless
// opts.Unconfined is true.
func Command(opts CommandOpts) (*exec.Cmd, func(), error) {
	if len(opts.Argv) == 0 || opts.Argv[0] == "" {
		return nil, nil, errors.New("isolate command: empty argv")
	}
	env, gomodcache := buildCommandEnv(opts)
	if opts.Unconfined {
		cmd := exec.Command(opts.Argv[0], opts.Argv[1:]...)
		cmd.Dir = opts.Dir
		cmd.Env = env
		return cmd, func() {}, nil
	}
	bin, err := resolveCommandBin(opts.Argv[0])
	if err != nil {
		return nil, nil, err
	}
	return prepareIsolatedCmd(buildCommandPolicy(opts, bin, env, gomodcache))
}

func resolveCommandBin(bin string) (string, error) {
	if filepath.IsAbs(bin) {
		return bin, nil
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("isolate command %s: %w", bin, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("isolate command %s: %w", bin, err)
	}
	return abs, nil
}

func buildCommandEnv(opts CommandOpts) ([]string, string) {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"TERM=" + envOr("TERM", "dumb"),
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"USER=" + envOr("USER", name),
		"LOGNAME=" + envOr("LOGNAME", name),
		"SHELL=/bin/bash",
		"HOME=" + opts.ScratchHome,
		"TMPDIR=" + opts.TmpDir,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	}
	if opts.TmpDir != "" {
		env = append(env, "GOCACHE="+filepath.Join(opts.TmpDir, "gocache"))
	}
	gopath, gomodcache := hostGoDirs(opts.RealHome)
	if gomodcache != "" {
		env = append(env, "GOMODCACHE="+gomodcache)
	}
	if gopath != "" {
		env = append(env, "GOPATH="+gopath)
	}
	env = append(env, opts.ExtraEnv...)
	return env, gomodcache
}

func hostGoDirs(realHome string) (gopath, gomodcache string) {
	home := realHome
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		if u, err := user.Current(); err == nil {
			home = u.HomeDir
		}
	}
	rawGP := os.Getenv("GOPATH")
	if rawGP == "" && home != "" {
		rawGP = filepath.Join(home, "go")
	}
	gopath = existingDir(rawGP)
	rawMC := os.Getenv("GOMODCACHE")
	if rawMC == "" && rawGP != "" {
		rawMC = filepath.Join(rawGP, "pkg", "mod")
	}
	gomodcache = existingDir(rawMC)
	return gopath, gomodcache
}

func existingDir(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	res, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return ""
	}
	st, err := os.Stat(res)
	if err != nil || !st.IsDir() {
		return ""
	}
	return res
}

func buildCommandPolicy(opts CommandOpts, bin string, env []string, gomodcache string) Policy {
	rw := appendUniqueClean(nil, opts.Dir, opts.ScratchHome, opts.TmpDir)
	for _, dev := range safeCharDevs {
		if _, err := os.Stat(dev); err == nil {
			rw = appendUniqueClean(rw, dev)
		}
	}
	rw = appendUniqueClean(rw, opts.RWPaths...)

	var ro []string
	ro = addPathPair(ro, bin)
	ro = appendExistingPaths(ro, systemROCandidates)
	if gr := runtime.GOROOT(); gr != "" {
		ro = appendExistingPaths(ro, []string{gr})
	}
	ro = appendSafePATHDirs(ro, opts.RealHome)
	if gomodcache != "" {
		ro = appendExistingPaths(ro, []string{gomodcache})
	}
	ro = appendUniqueClean(ro, opts.ROPaths...)

	return Policy{
		Argv:    append([]string{bin}, opts.Argv[1:]...),
		Dir:     opts.Dir,
		Env:     env,
		RWPaths: rw,
		ROPaths: ro,
		Network: false,
	}
}
