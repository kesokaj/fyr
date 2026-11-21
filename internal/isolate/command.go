package isolate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	env, pathVal, gomodcache := buildCommandEnv(opts)
	if opts.Unconfined {
		cmd := exec.Command(opts.Argv[0], opts.Argv[1:]...)
		cmd.Dir = opts.Dir
		cmd.Env = env
		return cmd, func() {}, nil
	}
	bin, err := resolveCommandBin(opts.Argv[0], opts.Dir, pathVal)
	if err != nil {
		return nil, nil, err
	}
	return prepareIsolatedCmd(buildCommandPolicy(opts, bin, env, gomodcache))
}

func resolveCommandBin(bin, workDir, pathVal string) (string, error) {
	if filepath.IsAbs(bin) {
		return bin, nil
	}
	if strings.ContainsRune(bin, filepath.Separator) {
		cand := bin
		if workDir != "" {
			cand = filepath.Join(workDir, bin)
		}
		abs, err := filepath.Abs(cand)
		if err != nil {
			return "", fmt.Errorf("isolate command %s: %w", bin, err)
		}
		st, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("isolate command %s: %w", bin, err)
		}
		if st.IsDir() || st.Mode()&0o111 == 0 {
			return "", fmt.Errorf("isolate command %s: %w", bin, os.ErrPermission)
		}
		return abs, nil
	}
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		cand := filepath.Join(dir, bin)
		st, err := os.Stat(cand)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		abs, err := filepath.Abs(cand)
		if err != nil {
			return "", fmt.Errorf("isolate command %s: %w", bin, err)
		}
		return abs, nil
	}
	return "", fmt.Errorf("isolate command %s: %w", bin, exec.ErrNotFound)
}

func buildCommandEnv(opts CommandOpts) ([]string, string, string) {
	pathVal := os.Getenv("PATH")
	if !opts.Unconfined {
		allow := make([]string, 0, 3+len(opts.ROPaths)+len(opts.RWPaths))
		allow = append(allow, opts.Dir, opts.ScratchHome, opts.TmpDir)
		allow = append(allow, opts.ROPaths...)
		allow = append(allow, opts.RWPaths...)
		pathVal = confinedPATH("", opts.RealHome, allow)
	}
	env := baseSandboxEnv(pathVal, opts.ScratchHome, opts.TmpDir)
	env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	if opts.TmpDir != "" {
		env = append(env,
			"GOCACHE="+filepath.Join(opts.TmpDir, "gocache"),
			"GOPATH="+filepath.Join(opts.TmpDir, "gopath"),
		)
	}
	gomodcache := hostGoDirs(opts.RealHome)
	if gomodcache != "" {
		env = append(env, "GOMODCACHE="+gomodcache)
	}
	env = append(env, opts.ExtraEnv...)
	return env, pathVal, gomodcache
}

func hostGoDirs(realHome string) string {
	home := effectiveRealHome(realHome)
	rawMC := os.Getenv("GOMODCACHE")
	if rawMC == "" && home != "" {
		rawMC = filepath.Join(home, "go", "pkg", "mod")
	}
	mcRes := existingDir(rawMC)
	if mcRes == "" {
		return ""
	}
	mcAbs, err := filepath.Abs(rawMC)
	if err != nil {
		return ""
	}
	tmpRoot := filepath.Clean(os.TempDir())
	resTmp, _ := resolve(tmpRoot)
	var resHome string
	if home != "" {
		resHome, _ = resolve(home)
	}
	if !isExcludedPATHDir(mcAbs, mcRes, tmpRoot, resTmp, home, resHome) {
		return mcRes
	}
	if resHome != "" && within(mcRes, filepath.Join(resHome, "go", "pkg", "mod")) {
		return mcRes
	}
	return ""
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
	var baseRO []string
	if gomodcache != "" {
		baseRO = []string{gomodcache}
	}
	rw, ro := policyPaths(
		opts.RealHome, bin,
		[]string{opts.Dir, opts.ScratchHome, opts.TmpDir}, opts.RWPaths,
		baseRO, opts.ROPaths,
	)
	return Policy{
		Argv:    append([]string{bin}, opts.Argv[1:]...),
		Dir:     opts.Dir,
		Env:     env,
		RWPaths: rw,
		ROPaths: ro,
		Network: false,
	}
}
