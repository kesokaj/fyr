package harness

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// PathShadows walks PATH the way a shell does and reports two facts about the
// binary at binPath: the executables of the same name that PATH finds BEFORE
// it (in PATH order, without repeats), and whether PATH reaches it at all.
//
// "Reaches it" means one thing only: a PATH entry holds an executable that IS
// the installed file, by os.SameFile — through its own directory, a symlinked
// directory, a symlink or a hard link to it. Never a lexical comparison of
// directory names: a directory spelled differently (`.local/bin//`, a
// symlinked directory) made a lexical "on PATH" disagree with the file
// identity and hid a real shadow. One identity, one walk.
//
// An empty shadows result with onPath true means an unqualified `fyr`
// runs the binary the install just wrote; anything else means a shadow runs
// instead while every command reports success and `status` reports drift
// none, since drift is measured against the source the running binary
// resolved (BUG-078). When binPath does not exist, nothing can reach it:
// onPath is false and every executable of that name is a shadow.
func PathShadows(pathEnv, binPath string) (shadows []string, onPath bool) {
	name := filepath.Base(binPath)
	target, haveTarget := executableAt(binPath)
	for _, cand := range pathCandidates(pathEnv, name) {
		info, ok := executableAt(cand)
		if !ok {
			continue
		}
		if haveTarget && os.SameFile(info, target) {
			// The shell would run the installed file here; nothing later can win.
			return shadows, true
		}
		shadows = append(shadows, cand)
	}
	return shadows, false
}

// OldBinariesOnPath lists every executable `sh-agent` PATH finds, in PATH
// order: the binary from before the rename, which a crossing leaves when its
// removal was refused or the old manifest never listed it (plan fyr F2, F4).
func OldBinariesOnPath(pathEnv string) []string {
	var found []string
	for _, cand := range pathCandidates(pathEnv, "sh-agent") {
		if _, ok := executableAt(cand); ok {
			found = append(found, cand)
		}
	}
	return found
}

// pathCandidates is the one PATH walk: the file a shell would try for name in
// each PATH entry, in PATH order, without repeats. An empty entry is the
// working directory (POSIX), printed as ./name so it reads as a file, not a
// command.
func pathCandidates(pathEnv, name string) []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range filepath.SplitList(pathEnv) {
		cand := filepath.Join(dir, name)
		if dir == "" {
			cand = "." + string(filepath.Separator) + name
		}
		if seen[cand] {
			continue
		}
		seen[cand] = true
		out = append(out, cand)
	}
	return out
}

// executableAt reports the file a shell would execute at p, following
// symlinks. A directory, a missing file, a dangling symlink, or a file with no
// execute bit is not one.
func executableAt(p string) (os.FileInfo, bool) {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return nil, false
	}
	return info, true
}

// runningElsewhere reports where a shell finds the binary at exe by the name
// of binPath, when exe is NOT the file at binPath. `update` replaces the file
// at binPath and nothing else, so a process started from another path has
// just updated a file it is not running, and a shell that resolved the name
// to that path keeps running it whatever PATH says, because the shell caches
// the lookup (BUG-079). A process no PATH entry reaches by name — a test
// binary, a `go run` build — is nobody's `fyr` and is not reported.
func runningElsewhere(pathEnv, binPath, exe string) (string, bool) {
	self, err := os.Stat(exe)
	if err != nil {
		return "", false
	}
	// No installed file, nothing to be behind: an `update` that installed
	// nothing must not call the running binary stale (security review 2026-09-18).
	inst, err := os.Stat(binPath)
	if err != nil || os.SameFile(self, inst) {
		return "", false
	}
	for _, cand := range pathCandidates(pathEnv, filepath.Base(binPath)) {
		if info, err := os.Stat(cand); err == nil && os.SameFile(info, self) {
			return cand, true
		}
	}
	return "", false
}

// NoteRunningElsewhere says, after an update wrote the installed binary, when
// the process that ran the update is not that binary and a shell can reach the
// process's file by name: that file stays as it is, and a shell that already
// resolved `fyr` to it keeps running it (BUG-079). Called only after a
// binary was actually written: an up-to-date or older-release run installs
// nothing, and on the older-release branch the running file is the NEWER one
// (security review 2026-09-18).
func NoteRunningElsewhere(home string, out io.Writer) {
	// The shell hazard is about the home a shell actually uses: for a --home
	// elsewhere (a scratch or test place) the note would call ITS binary the
	// installed one (security review 2026-09-18).
	real, err := os.UserHomeDir()
	if err != nil {
		return
	}
	realInfo, err1 := os.Stat(real)
	homeInfo, err2 := os.Stat(home)
	if err1 != nil || err2 != nil || !os.SameFile(realInfo, homeInfo) {
		// Identity, not spelling: a symlinked $HOME is the same directory.
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	installed := filepath.Join(home, BinPath)
	from, ok := runningElsewhere(os.Getenv("PATH"), installed, exe)
	if !ok {
		return
	}
	remedy := "point it at the installed binary (`ln -sfn`) or remove it"
	// Replacing a directory entry — relinking or removing — is governed by
	// the DIRECTORY's owner, not the file's: /usr/local/bin is root's whoever
	// owns the file in it.
	if info, err := os.Stat(filepath.Dir(from)); err == nil && !ownedByCaller(info) {
		remedy = "it is not yours to relink — remove it, with the rights that own its directory, or leave it and run the installed one by its path"
	}
	fmt.Fprintf(out, "note: this update ran from %s, not from the installed binary %s; update replaces only the installed one, so the file it ran from stays as it is. A shell that has already resolved `fyr` to that path keeps running it until `hash -r` (bash) or `rehash` (zsh); %s\n", from, installed, remedy)
}
