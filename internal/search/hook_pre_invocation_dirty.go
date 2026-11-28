package search

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

func dynamicDirtyFastPathCap(ctx context.Context, existingFiles int) int {
	if !hasRemainingBudget(ctx, 60*time.Millisecond) {
		return 0
	}
	if existingFiles >= 2000 {
		return 3
	}
	return 8
}

func tryDirtyFastReconcile(ctx context.Context, root string, st *store, existingFiles int) (*scope, string, bool, bool, error) {
	dirtyEntries, dirtyLabels, ok := detectDirtyPathsFast(ctx, root, st)
	if !ok {
		return nil, "", false, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, true, err
	}
	if len(dirtyEntries) == 0 {
		return &scope{st: st, projects: []string{root}}, "", false, true, nil
	}
	capLimit := dynamicDirtyFastPathCap(ctx, existingFiles)
	if len(dirtyEntries) > capLimit {
		return &scope{st: st, projects: []string{root}}, formatDirtyFastPath(dirtyLabels, len(dirtyEntries), capLimit), true, true, nil
	}
	if err := st.applyDirtyPaths(ctx, root, dirtyEntries, existingFiles); err != nil {
		return nil, "", false, true, err
	}
	if existingFiles <= maxWarmSigEntries {
		st.saveGitWarmSig(root)
	}
	return &scope{st: st, projects: []string{root}}, "", false, true, nil
}

func detectDirtyPathsFast(ctx context.Context, root string, st *store) ([]entry, []string, bool) {
	if ctx.Err() != nil || !st.matchesGitIdxHeadSig(ctx, root) {
		return nil, nil, false
	}
	gitDir := resolveGitWorktreeDir(root)
	if gitDir == "" {
		return nil, nil, false
	}
	indexPath := filepath.Join(gitDir, "index")
	idxFi, err := os.Lstat(indexPath)
	if err != nil || !idxFi.Mode().IsRegular() {
		return nil, nil, false
	}
	entries, hasIndex, err := readGitIndex(indexPath)
	if !hasIndex || err != nil || len(entries) == 0 {
		return nil, nil, false
	}
	indexedFiles, nonTracked, ok := loadIndexedFilesCtx(ctx, st, root)
	if !ok {
		return nil, nil, false
	}
	fsRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, nil, false
	}
	defer fsRoot.Close()
	dirtyEntries, dirtyLabels, ok := scanDirtyTrackedEntries(ctx, root, fsRoot, entries, idxFi.ModTime().Unix(), indexedFiles, nonTracked)
	if !ok {
		return nil, nil, false
	}
	return scanDirtyUntrackedEntries(ctx, root, fsRoot, nonTracked, dirtyEntries, dirtyLabels)
}

func loadIndexedFilesCtx(ctx context.Context, st *store, root string) (map[string]bool, map[string]entry, bool) {
	rows, err := st.db.QueryContext(ctx, `SELECT path, oid, state FROM files WHERE project = ?`, root)
	if err != nil {
		return nil, nil, false
	}
	defer rows.Close()
	indexedFiles := map[string]bool{}
	nonTracked := map[string]entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.path, &e.oid, &e.state); err != nil {
			return nil, nil, false
		}
		indexedFiles[e.path] = true
		if e.state != "tracked" {
			nonTracked[e.path] = e
		}
	}
	return indexedFiles, nonTracked, rows.Err() == nil
}

func shouldSkipTrackedIndexEntry(e gitIndexEntry) bool {
	if e.Stage != 0 {
		return true
	}
	modeType := e.Mode & 0o170000
	if modeType == 0o160000 || modeType == 0o120000 {
		return true
	}
	return !indexed(e.Path) || harness.SecretShaped(path.Base(e.Path))
}

func scanDirtyTrackedEntries(ctx context.Context, root string, fsRoot *os.Root, entries []gitIndexEntry, idxSec int64, indexedFiles map[string]bool, nonTracked map[string]entry) ([]entry, []string, bool) {
	var dirty []entry
	var labels []string
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, nil, false
		}
		if shouldSkipTrackedIndexEntry(e) {
			continue
		}
		if isNoIndexMarker(e.Path) || !filepath.IsLocal(filepath.FromSlash(e.Path)) {
			return nil, nil, false
		}
		ch, lbl, changed := checkDirtyTrackedEntry(root, fsRoot, e, idxSec, indexedFiles, nonTracked)
		if !changed {
			continue
		}
		dirty = append(dirty, ch)
		if lbl != "" {
			labels = append(labels, lbl)
		}
	}
	return dirty, labels, true
}

func deletedIfIndexed(p, label string, inIndex bool) (entry, string, bool) {
	if !inIndex {
		return entry{}, "", false
	}
	return entry{path: p, state: "deleted"}, label, true
}

func trackedEntryMatchesStat(fi os.FileInfo, e gitIndexEntry, idxSec int64) bool {
	if fi.Size() != int64(e.Size) || e.MTimeSec == 0 {
		return false
	}
	mt := fi.ModTime()
	if mt.Unix() != int64(e.MTimeSec) || uint32(mt.Nanosecond()) != e.MTimeNsec {
		return false
	}
	return e.MTimeNsec != 0 || int64(e.MTimeSec) < idxSec
}

func reconcileCleanTrackedEntry(p, idxOID string, inIndex, wasDirty bool, prev entry) (entry, string, bool) {
	if !inIndex || (wasDirty && prev.oid != idxOID) {
		return entry{path: p, oid: idxOID, state: "tracked"}, "", true
	}
	return entry{}, "", false
}

func checkDirtyTrackedEntry(root string, fsRoot *os.Root, e gitIndexEntry, idxSec int64, indexedFiles map[string]bool, nonTracked map[string]entry) (entry, string, bool) {
	local := filepath.FromSlash(e.Path)
	inIndex := indexedFiles[e.Path]
	prev, wasDirty := nonTracked[e.Path]
	delete(nonTracked, e.Path)
	fi, err := os.Lstat(filepath.Join(root, local))
	if err != nil || !fi.Mode().IsRegular() {
		return deletedIfIndexed(e.Path, "D "+e.Path, inIndex)
	}
	if fi.Size() > maxFileBytes {
		return deletedIfIndexed(e.Path, "", inIndex)
	}
	idxOID := fmt.Sprintf("%x", e.SHA1)
	if trackedEntryMatchesStat(fi, e, idxSec) {
		return reconcileCleanTrackedEntry(e.Path, idxOID, inIndex, wasDirty, prev)
	}
	data, err := readAll(fsRoot, local)
	if err != nil {
		return deletedIfIndexed(e.Path, "", inIndex)
	}
	oid := blobID(data)
	if oid == idxOID {
		return reconcileCleanTrackedEntry(e.Path, idxOID, inIndex, wasDirty, prev)
	}
	if !inIndex || !wasDirty || prev.oid != oid {
		return entry{path: e.Path, oid: oid, state: "modified"}, "M " + e.Path, true
	}
	return entry{}, "", false
}

func readUntrackedEntryOID(fsRoot *os.Root, p string) (string, bool) {
	local := filepath.FromSlash(p)
	fi, err := fsRoot.Lstat(local)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileBytes {
		return "", false
	}
	data, err := readAll(fsRoot, local)
	if err != nil {
		return "", false
	}
	return blobID(data), true
}

func checkDirtyUntrackedPath(fsRoot *os.Root, p string, remaining map[string]entry) (entry, bool) {
	oid, ok := readUntrackedEntryOID(fsRoot, p)
	if !ok {
		return entry{}, false
	}
	if prev, exists := remaining[p]; exists && prev.oid == oid {
		return entry{}, false
	}
	return entry{path: p, oid: oid, state: "untracked"}, true
}

func appendDeletedUntrackedEntries(remaining map[string]entry, seenUntracked map[string]bool, dirty []entry, labels []string) ([]entry, []string) {
	for p, prev := range remaining {
		if prev.state == "untracked" && !seenUntracked[p] {
			dirty = append(dirty, entry{path: p, state: "deleted"})
			labels = append(labels, "D "+p)
		}
	}
	return dirty, labels
}

func scanDirtyUntrackedEntries(ctx context.Context, root string, fsRoot *os.Root, remaining map[string]entry, dirty []entry, labels []string) ([]entry, []string, bool) {
	if ctx.Err() != nil {
		return nil, nil, false
	}
	out, err := gitCtx(ctx, root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, false
	}
	seenUntracked := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if isNoIndexMarker(p) {
			return nil, nil, false
		}
		if !indexed(p) || harness.SecretShaped(path.Base(p)) {
			continue
		}
		seenUntracked[p] = true
		if ch, changed := checkDirtyUntrackedPath(fsRoot, p, remaining); changed {
			dirty = append(dirty, ch)
			labels = append(labels, "?? "+p)
		}
	}
	dirty, labels = appendDeletedUntrackedEntries(remaining, seenUntracked, dirty, labels)
	return dirty, labels, true
}

func reconcileHookScopeView(ctx context.Context, root string, st *store, existingFiles int) (*scope, string, bool, error) {
	view, leftOut, err := gitView(root)
	if err != nil {
		return nil, "", false, err
	}
	dirtyPaths := collectDirtyPaths(view)
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	diffN, _ := st.diffCount(source{project: root, dir: root, view: view})
	capLimit := hookMaxDirtyFastPath
	if existingFiles > 0 {
		capLimit = dynamicDirtyFastPathCap(ctx, existingFiles)
	}
	if existingFiles > 0 && diffN > capLimit {
		return &scope{st: st, projects: []string{root}, leftOut: leftOut}, formatDirtyFastPath(dirtyPaths, diffN, capLimit), true, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	if _, err := st.apply(source{project: root, dir: root, view: view}); err != nil {
		return nil, "", false, err
	}
	if len(leftOut) == 0 {
		st.saveGitWarmSig(root)
	}
	return &scope{st: st, projects: []string{root}, leftOut: leftOut}, "", false, nil
}

func collectDirtyPaths(view []entry) []string {
	var out []string
	for _, e := range view {
		switch e.state {
		case "modified":
			out = append(out, "M "+e.path)
		case "untracked":
			out = append(out, "?? "+e.path)
		}
	}
	return out
}

func formatDirtyFastPath(dirtyPaths []string, diffN, threshold int) string {
	var b strings.Builder
	count := max(len(dirtyPaths), diffN)
	fmt.Fprintf(&b, "# fast-path: %d modified/untracked files (> %d threshold; serving cached AST snapshot)\n", count, threshold)
	limit := min(20, len(dirtyPaths))
	for i := 0; i < limit; i++ {
		b.WriteString("  " + dirtyPaths[i] + "\n")
	}
	if len(dirtyPaths) > limit {
		fmt.Fprintf(&b, "  ... and %d more\n", len(dirtyPaths)-limit)
	}
	return strings.TrimRight(b.String(), "\n")
}
