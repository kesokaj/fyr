package sandbox

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxRefFile bounds HEAD and a loose ref: git reads each whole, on the host.
const maxRefFile = 4096

// foreignNames lead a repository's reads to another one, relative to .git.
var foreignNames = []string{"objects/info/alternates", "commondir", "gitdir"}

// GitIntact reports whether the clone's .git holds only what can stay in it,
// and fails the box sandbox replaced when it does not. Under the docker
// runner a session cannot reach the lead, but the host's upload-pack then
// reads this .git as the operator, and git's options do not reach it: an
// alternates file, or a ref linked to a file of another repository, brings
// that repository's objects into the lead; a FIFO or a device hangs the fetch
// or fills the host's memory (PLAN.md item 45, findings S1 and S3). It runs
// after ExitChecks, so nothing of the session is left to race it.
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
		if rel == "HEAD" || strings.HasPrefix(rel, "refs"+string(filepath.Separator)) {
			info, err := d.Info()
			if err != nil {
				return err
			}
			if info.Size() > maxRefFile {
				why = fmt.Sprintf("%s is %d bytes", rel, info.Size())
				return fs.SkipAll
			}
		}
		return nil
	})
	if why == "" && err != nil {
		return err.Error()
	}
	return why
}
