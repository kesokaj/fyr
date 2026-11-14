package work

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/kesokaj/fyr/internal/sandbox"
)

type baseCheckSetupError struct{ err error }

func (e *baseCheckSetupError) Error() string { return e.err.Error() }
func (e *baseCheckSetupError) Unwrap() error { return e.err }

// runBaseCheck executes the Kickoff's Base-Check: inside an isolated
// --network none container when the docker runner is active, or on the host
// with an allowlisted environment under --runner host.
func (w *worker) runBaseCheck() ([]byte, error) {
	if cfg := w.p.dockerConfig(w.stamp); cfg != nil {
		label := "fyr.work.basecheck=" + w.stamp + "-" + w.p.k.Name
		cmd, err := w.dockerBaseCheckCmd(label, cfg)
		if err != nil {
			return nil, &baseCheckSetupError{err: err}
		}
		out, err := runBounded(cmd, baseCheckTimeout)
		if err != nil {
			_, _ = removeLabelled([]string{"ps", "-aq", "--filter", "label=" + label}, "rm", "-f")
		}
		return out, err
	}
	return runBounded(w.hostBaseCheckCmd(), baseCheckTimeout)
}

func (w *worker) dockerBaseCheckCmd(label string, cfg *sandbox.Docker) (*exec.Cmd, error) {
	args, err := dockerBaseCheckArgs(w.b.App(), w.b.Home(), w.stamp, w.p.k.Name, label, w.p.k.BaseCheck, cfg)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("docker", args...)
	cmd.Dir = w.b.App()
	cmd.Env = sandbox.DockerEnv()
	return cmd, nil
}

func dockerBaseCheckArgs(app, sb, stamp, name, label, baseCheck string, cfg *sandbox.Docker) ([]string, error) {
	for _, p := range []string{app, sb} {
		if err := sandbox.Mountable(p); err != nil {
			return nil, err
		}
	}
	if cfg.Image == "" || strings.HasPrefix(cfg.Image, "-") || strings.IndexFunc(cfg.Image, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fmt.Errorf("the image %q is not an image ref", cfg.Image)
	}
	mem, cpus, tmpfs := dockerLimitsOrDefault(cfg)
	args := []string{
		"run", "--rm", "--pull", "never",
		"--label", "fyr.work=" + stamp,
		"--label", "fyr.worker=" + name,
		"--label", label,
		"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--init",
		"--memory", mem, "--memory-swap", mem, "--cpus", cpus, "--pids-limit", "1024",
		"--network", "none",
		"--read-only",
		"--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=" + tmpfs + ",mode=1777",
		"--mount", "type=bind,src=" + app + ",dst=" + app,
		"--mount", "type=bind,src=" + sb + ",dst=" + sb,
		"-w", app,
	}
	for _, kv := range dockerBaseCheckEnv(app, sb) {
		args = append(args, "-e", kv)
	}
	return append(args, cfg.Image, "sh", "-c", baseCheck), nil
}

func dockerLimitsOrDefault(cfg *sandbox.Docker) (mem, cpus, tmpfs string) {
	mem, cpus, tmpfs = cfg.Memory, cfg.CPUs, cfg.TmpfsSize
	if mem == "" {
		mem = "8g"
	}
	if cpus == "" {
		cpus = "2"
	}
	if tmpfs == "" {
		tmpfs = "2g"
	}
	return mem, cpus, tmpfs
}

func dockerBaseCheckEnv(app, sb string) []string {
	env := []string{
		"PATH=" + sandbox.ImagePath,
		"HOME=" + sb,
		"PWD=" + app,
		"TERM=dumb",
		"LANG=C.UTF-8",
		"SHELL=/bin/bash",
		"GOPATH=" + filepath.Join(sb, "go"),
		"GOTOOLCHAIN=local",
		"GODEBUG=installgoroot=all",
	}
	if tz := os.Getenv("TZ"); tz != "" {
		env = append(env, "TZ="+tz)
	}
	return env
}

func (w *worker) hostBaseCheckCmd() *exec.Cmd {
	cmd := exec.Command("sh", "-c", w.p.k.BaseCheck)
	cmd.Dir = w.b.App()
	cmd.Env = hostBaseCheckEnv(w.b.App(), w.b.Home())
	return cmd
}

func hostBaseCheckEnv(app, sb string) []string {
	env := []string{
		"PATH=" + sanitizedHostPath(app, sb),
		"HOME=" + sb,
		"PWD=" + app,
		"SHELL=/bin/bash",
		"STYRMAN_SKIP_CRED_PULL=1",
		"TERM=" + envDefault("TERM", "dumb"),
		"LANG=" + envDefault("LANG", "C.UTF-8"),
	}
	for _, k := range []string{"LC_ALL", "USER", "LOGNAME", "TZ", "TMPDIR", "GOPATH", "GOCACHE", "GOMODCACHE", "GOROOT", "GOTOOLCHAIN"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func sanitizedHostPath(app, sb string) string {
	sbRoot := canonicalPath(sb)
	appRoot := canonicalPath(app)
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		c := canonicalPath(dir)
		if isPathOrSubdir(c, sbRoot) || isPathOrSubdir(c, appRoot) {
			continue
		}
		kept = append(kept, dir)
	}
	if len(kept) == 0 {
		return "/usr/local/bin:/usr/bin:/bin"
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

func canonicalPath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	dir := filepath.Dir(p)
	if dir != p {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, filepath.Base(p))
		}
	}
	return p
}

func isPathOrSubdir(path, root string) bool {
	if root == "" || root == "." {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
