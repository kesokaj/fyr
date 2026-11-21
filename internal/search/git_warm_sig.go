package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	gitWarmSigMetaPrefix = "git_warm_sig:"
	maxWarmSigEntries    = 2500
	maxWarmSigDirs       = 512
	maxWarmSigRacyFileB  = 256 << 10
	maxWarmSigRacyTotalB = 2 << 20
	maxUntrackedFiles    = 256
	maxUntrackedDepth    = 6
)

func gitWarmSigKey(root string) string {
	if canon := CanonicalizePath("", root); canon != "" {
		return gitWarmSigMetaPrefix + canon
	}
	return gitWarmSigMetaPrefix + root
}

func (s *store) matchesGitWarmSig(ctx context.Context, root string) bool {
	if s == nil || s.db == nil || root == "" {
		return false
	}
	var cached string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, gitWarmSigKey(root)).Scan(&cached); err != nil || cached == "" {
		return false
	}
	cur, ok := computeGitWarmSignature(root)
	return ok && cur == cached
}

func (s *store) saveGitWarmSig(root string) {
	if s == nil || s.db == nil || root == "" {
		return
	}
	key := gitWarmSigKey(root)
	sig, ok := computeGitWarmSignature(root)
	if !ok {
		_, _ = s.db.Exec(`DELETE FROM meta WHERE key = ?`, key)
		return
	}
	_, _ = s.db.Exec(
		`INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, sig,
	)
}

func computeGitWarmSignature(root string) (string, bool) {
	gitDir := resolveGitWorktreeDir(root)
	_, commonDir, ok := findGitRootFast(root)
	if !ok || gitDir == "" || commonDir == "" {
		return "", false
	}
	h := sha256.New()
	if !hashGitHeadAndRefs(h, gitDir, commonDir) {
		return "", false
	}
	indexPath := filepath.Join(gitDir, "index")
	idxFi, err := os.Lstat(indexPath)
	if err != nil || !idxFi.Mode().IsRegular() {
		return "", false
	}
	fmt.Fprintf(h, "idx:%d:%d\n", idxFi.Size(), idxFi.ModTime().UnixNano())
	entries, hasIndex, err := readGitIndex(indexPath)
	if !hasIndex || err != nil || len(entries) == 0 || len(entries) > maxWarmSigEntries {
		return "", false
	}
	tracked, dirs, ok := hashTrackedWarmEntries(h, root, entries, idxFi.ModTime().Unix())
	if !ok || !hashTrackedDirsForUntracked(h, root, dirs, tracked) {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

func hashGitHeadAndRefs(h hash.Hash, gitDir, commonDir string) bool {
	headPath := filepath.Join(gitDir, "HEAD")
	headFi, err := os.Lstat(headPath)
	if err != nil || !headFi.Mode().IsRegular() || headFi.Size() > 4096 {
		return false
	}
	headBytes, err := os.ReadFile(headPath)
	if err != nil {
		return false
	}
	head := strings.TrimSpace(string(headBytes))
	if strings.Contains(head, ".invalid") {
		return false
	}
	fmt.Fprintf(h, "head:%d:%s\n", headFi.ModTime().UnixNano(), head)
	if ref, isRef := strings.CutPrefix(head, "ref: "); isRef {
		cleanRef := filepath.FromSlash(strings.TrimSpace(ref))
		if !filepath.IsLocal(cleanRef) {
			return false
		}
		hashOptionalStat(h, "wref", filepath.Join(gitDir, cleanRef))
		hashOptionalStat(h, "cref", filepath.Join(commonDir, cleanRef))
	}
	hashOptionalStat(h, "packed", filepath.Join(commonDir, "packed-refs"))
	return true
}

func hashOptionalStat(h hash.Hash, tag, p string) {
	fi, err := os.Lstat(p)
	if err != nil {
		fmt.Fprintf(h, "%s:none\n", tag)
		return
	}
	fmt.Fprintf(h, "%s:%d:%d\n", tag, fi.Size(), fi.ModTime().UnixNano())
}

func hashTrackedWarmEntries(h hash.Hash, root string, entries []gitIndexEntry, idxSec int64) (map[string]bool, map[string]bool, bool) {
	tracked := make(map[string]bool, len(entries))
	dirs := map[string]bool{".": true}
	var racyTotal int64
	for _, e := range entries {
		if e.Stage != 0 || (e.Mode&0o170000) == 0o160000 || !filepath.IsLocal(filepath.FromSlash(e.Path)) {
			return nil, nil, false
		}
		tracked[e.Path] = true
		addParentDirsToSet(dirs, e.Path)
		abs := filepath.Join(root, filepath.FromSlash(e.Path))
		fi, err := os.Lstat(abs)
		if err != nil {
			fmt.Fprintf(h, "gone:%s\n", e.Path)
			continue
		}
		fmt.Fprintf(h, "f:%s:%d:%d:%d:%x\n", e.Path, fi.Mode(), fi.Size(), fi.ModTime().UnixNano(), e.SHA1[:8])
		if fi.Mode().IsRegular() && fi.ModTime().Unix() >= idxSec {
			racyTotal += fi.Size()
			if fi.Size() > maxWarmSigRacyFileB || racyTotal > maxWarmSigRacyTotalB {
				return nil, nil, false
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				return nil, nil, false
			}
			sum := sha256.Sum256(data)
			fmt.Fprintf(h, "racy:%x\n", sum[:12])
		}
	}
	return tracked, dirs, len(dirs) <= maxWarmSigDirs
}

func addParentDirsToSet(dirs map[string]bool, relPath string) {
	for d := path.Dir(relPath); d != "." && d != "/" && d != ""; d = path.Dir(d) {
		if dirs[d] {
			return
		}
		dirs[d] = true
	}
}

func hashTrackedDirsForUntracked(h hash.Hash, root string, dirs, tracked map[string]bool) bool {
	sortedDirs := make([]string, 0, len(dirs))
	for d := range dirs {
		sortedDirs = append(sortedDirs, d)
	}
	sort.Strings(sortedDirs)
	untrackedCount := 0
	for _, relDir := range sortedDirs {
		absDir := root
		if relDir != "." {
			absDir = filepath.Join(root, filepath.FromSlash(relDir))
		}
		if !scanUntrackedDirEntries(h, absDir, relDir, 0, &untrackedCount, dirs, tracked) {
			return false
		}
	}
	return true
}

func isIgnoredUntrackedDir(name string) bool {
	switch name {
	case ".git", ".agents", "node_modules", "vendor", ".next", "dist", "build", "target", "__pycache__":
		return true
	}
	return false
}

func scanUntrackedDirEntries(h hash.Hash, absDir, relDir string, depth int, count *int, dirs, tracked map[string]bool) bool {
	if depth > maxUntrackedDepth {
		return false
	}
	dents, err := os.ReadDir(absDir)
	if err != nil {
		return false
	}
	for _, d := range dents {
		name := d.Name()
		if isIgnoredUntrackedDir(name) {
			continue
		}
		rel := name
		if relDir != "." {
			rel = relDir + "/" + name
		}
		if tracked[rel] || (depth == 0 && dirs[rel]) {
			continue
		}
		if d.Type()&os.ModeSymlink != 0 {
			return false
		}
		fi, err := d.Info()
		if err != nil {
			return false
		}
		if d.IsDir() {
			if !scanUntrackedDirEntries(h, filepath.Join(absDir, name), rel, depth+1, count, dirs, tracked) {
				return false
			}
			continue
		}
		*count++
		if *count > maxUntrackedFiles {
			return false
		}
		fmt.Fprintf(h, "u:%s:false:%d:%d\n", rel, fi.Size(), fi.ModTime().UnixNano())
	}
	return true
}
