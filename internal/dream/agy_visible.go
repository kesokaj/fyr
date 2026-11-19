package dream

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// visibleHome runs the oracle in home from work and reports whether every
// tool needs review there, and why not when one does not. env is fyr's
// environment; the oracle gets childEnv's allow-list with HOME set to home.
func visibleHome(argv0, home, work string, env []string) (ok bool, why string) {
	if _, _, err := loginIn(runtime.GOOS, home); err != nil {
		return false, "no Antigravity login in " + home
	}
	if why := workAlone(home, work); why != "" {
		return false, why
	}
	env = childEnv(env, home)
	perms, err := oracle(argv0, "permissions", work, env)
	if err != nil {
		return false, err.Error()
	}
	if why := permissionsGrant(perms); why != "" {
		return false, why
	}
	conf, err := oracle(argv0, "config", work, env)
	if err != nil {
		return false, err.Error()
	}
	if why := configGrants(conf, work); why != "" {
		return false, why
	}
	hooks, err := oracle(argv0, "hooks", work, env)
	if err != nil {
		return false, err.Error()
	}
	if why := hooksAny(hooks); why != "" {
		return false, why
	}
	return true, ""
}

// workAlone says why work would bring more than the operator's own setup
// into the session, or "": it must be empty, and no directory above it may
// hold a .git (a checkout, whose .agents/ agy loads) or a .agents other than
// home's, which is the operator's own (plan D8). agy walks the directory
// its working directory resolves to, so the walk runs over both forms.
func workAlone(home, work string) string {
	entries, err := os.ReadDir(work)
	if err != nil {
		return "dream-work unreadable: " + err.Error()
	}
	if len(entries) > 0 {
		return "dream-work is not empty (" + entries[0].Name() + ")"
	}
	homes := map[string]bool{filepath.Clean(home): true}
	if r, err := filepath.EvalSymlinks(home); err == nil {
		homes[r] = true
	}
	for _, w := range workForms(work) {
		for dir := filepath.Dir(w); ; dir = filepath.Dir(dir) {
			if exists(filepath.Join(dir, ".git")) {
				return "dream-work is inside a git checkout (" + dir + ")"
			}
			if exists(filepath.Join(dir, ".agents")) && !homes[dir] {
				return "a .agents directory above dream-work (" + dir + ")"
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	return ""
}

// workForms is work as written and, when it differs, as it resolves.
func workForms(work string) []string {
	forms := []string{filepath.Clean(work)}
	if r, err := filepath.EvalSymlinks(work); err == nil && r != forms[0] {
		forms = append(forms, r)
	}
	return forms
}

// exists reports whether path names anything, a dangling link included.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// trustCovers names the first trusted workspace at or above work, as written
// or as it resolves, or "".
func trustCovers(trusted []string, work string) string {
	for _, t := range trusted {
		for _, w := range workForms(work) {
			if covers(t, w) {
				return "a trusted workspace covers dream-work (" + t + ")"
			}
		}
	}
	return ""
}

// covers reports whether dir is path or above it; a dir that is not an
// absolute path is taken to cover it, toward the private branch.
func covers(dir, path string) bool {
	if !filepath.IsAbs(dir) {
		return true
	}
	dir, path = filepath.Clean(dir), filepath.Clean(path)
	return dir == path || dir == string(filepath.Separator) || strings.HasPrefix(path, dir+string(filepath.Separator))
}
