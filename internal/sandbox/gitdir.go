package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxRefFile and maxConfigFile bound what git reads whole, on the host:
// HEAD, a loose ref, and config.
const (
	maxRefFile    = 4096
	maxConfigFile = 1 << 20
)

// foreignNames lead a repository's reads to another one, relative to .git.
var foreignNames = []string{"objects/info/alternates", "commondir", "gitdir"}

// GitIntact reports whether the clone's .git holds only what can stay in it,
// and fails the box sandbox replaced when it does not. Under the docker
// runner a session cannot reach the lead, but the host's upload-pack then
// reads this .git as the operator, and git's options do not reach all
// clone-local config keys: an alternates file, or a ref linked to a file of
// another repository, brings that repository's objects into the lead; a FIFO
// or a device hangs the fetch or fills the host's memory (PLAN.md item 45,
// findings S1 and S3); and hook/filter/pack/uploadpack/include config keys
// expand attack surface if parsed. It runs after ExitChecks, so a docker
// session's own processes are gone; a process that passed check e on the
// host, or a concurrent run of this uid, can still change the clone after it,
// and what is then in .git is honoured. The fetch's arguments also disable
// hooks/fsmonitor on upload-pack and close upload-pack's fallback lookups.
func (b *Box) GitIntact() bool {
	why := gitDirProblem(filepath.Join(b.app, ".git"))
	if why == "" {
		return true
	}
	b.replaced(".git: %s; nothing is fetched from the clone", Printable(why))
	return false
}

// gitDirProblem says what in git must not be there, or "".
func gitDirProblem(git string) string {
	st, err := os.Lstat(git)
	if err != nil || !st.IsDir() {
		return git + " is not a directory"
	}
	for _, name := range foreignNames {
		if _, err := os.Lstat(filepath.Join(git, filepath.FromSlash(name))); !errors.Is(err, fs.ErrNotExist) {
			return name + " leads out of the clone"
		}
	}
	why := ""
	err = filepath.WalkDir(git, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(git, p)
		if !d.Type().IsRegular() {
			why = rel + " is not a regular file"
			return fs.SkipAll
		}
		limit := int64(0)
		switch {
		case rel == "HEAD" || strings.HasPrefix(rel, "refs"+string(filepath.Separator)):
			limit = maxRefFile
		case rel == "config" || strings.HasPrefix(rel, "reftable"+string(filepath.Separator)):
			limit = maxConfigFile
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > limit {
			why = fmt.Sprintf("%s is %d bytes", rel, info.Size())
			return fs.SkipAll
		}
		return nil
	})
	if why == "" && err != nil {
		return err.Error()
	}
	if why == "" {
		why = configProblem(filepath.Join(git, "config"))
	}
	return why
}

// configProblem says which key of config leads git to read more files, each
// whole, on the host (40 includes of one file are 40 copies in memory), or
// configures hooks/filters/pack/uploadpack/ssh commands on the clone. A config
// git cannot list is a problem too; --file --no-includes reads this file
// alone.
func configProblem(config string) string {
	cmd := exec.Command("git", "config", "--file", config, "--no-includes", "--name-only", "--list")
	cmd.Env = append(WithoutGit(os.Environ()), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return "config cannot be read: " + err.Error()
	}
	for _, name := range strings.Split(string(out), "\n") {
		if disallowedGitConfigKey(strings.ToLower(strings.TrimSpace(name))) {
			return "config sets " + name
		}
	}
	return ""
}

func disallowedGitConfigKey(low string) bool {
	if low == "" {
		return false
	}
	for _, pfx := range []string{"include.", "includeif.", "uploadpack.", "pack.", "filter."} {
		if strings.HasPrefix(low, pfx) {
			return true
		}
	}
	switch low {
	case "extensions.worktreeconfig",
		"core.hookspath",
		"core.fsmonitor",
		"core.sshcommand",
		"core.alternaterefscommand":
		return true
	}
	return false
}
