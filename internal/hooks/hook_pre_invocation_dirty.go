package hooks

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/hooks/guard"
)

// DynamicDirtyFastPathCap returns the dirty-file fast-path cap given remaining deadline and repo size.
func DynamicDirtyFastPathCap(ctx context.Context, existingFiles int) int {
	if !hasRemainingBudget(ctx, 60*time.Millisecond) {
		return 0
	}
	if existingFiles >= 2000 {
		return 3
	}
	return 8
}

// TryDirtyFastReconcile attempts incremental dirty-path reconciliation without a full git view.
func TryDirtyFastReconcile(ctx context.Context, d Deps, root string, st *Store, existingFiles int) (*Scope, string, bool, bool, error) {
	dirtyEntries, dirtyLabels, ok := DetectDirtyPathsFast(ctx, d, root, st)
	if !ok {
		return nil, "", false, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, true, err
	}
	if len(dirtyEntries) == 0 {
		return &Scope{St: st, Projects: []string{root}}, "", false, true, nil
	}
	capLimit := DynamicDirtyFastPathCap(ctx, existingFiles)
	if len(dirtyEntries) > capLimit {
		return &Scope{St: st, Projects: []string{root}}, FormatDirtyFastPath(dirtyLabels, len(dirtyEntries), capLimit), true, true, nil
	}
	if err := d.ApplyDirtyPaths(ctx, st, root, dirtyEntries, existingFiles); err != nil {
		return nil, "", false, true, err
	}
	if existingFiles <= d.maxWarmSigEntries() && d.SaveGitWarmSig != nil {
		d.SaveGitWarmSig(st, root)
	}
	return &Scope{St: st, Projects: []string{root}}, "", false, true, nil
}

// DetectDirtyPathsFast scans the git index directly for modified, deleted, or untracked indexable files.
func DetectDirtyPathsFast(ctx context.Context, d Deps, root string, st *Store) ([]Entry, []string, bool) {
	if ctx.Err() != nil || d.MatchesGitIdxHeadSig == nil || !d.MatchesGitIdxHeadSig(ctx, st, root) {
		return nil, nil, false
	}
	gitDir := guard.ResolveGitWorktreeDir(root)
	if gitDir == "" {
		return nil, nil, false
	}
	indexPath := filepath.Join(gitDir, "index")
	idxFi, err := os.Lstat(indexPath)
	if err != nil || !idxFi.Mode().IsRegular() {
		return nil, nil, false
	}
	entries, hasIndex, err := guard.ReadGitIndex(indexPath)
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
	dirtyEntries, dirtyLabels, ok := scanDirtyTrackedEntries(ctx, d, root, fsRoot, entries, idxFi.ModTime().Unix(), indexedFiles, nonTracked)
	if !ok {
		return nil, nil, false
	}
	return scanDirtyUntrackedEntries(ctx, d, root, fsRoot, nonTracked, dirtyEntries, dirtyLabels)
}

func loadIndexedFilesCtx(ctx context.Context, st *Store, root string) (map[string]bool, map[string]Entry, bool) {
	rows, err := st.SQLDB.QueryContext(ctx, `SELECT path, oid, state FROM files WHERE project = ?`, root)
	if err != nil {
		return nil, nil, false
	}
	defer rows.Close()
	indexedFiles := map[string]bool{}
	nonTracked := map[string]Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Path, &e.OID, &e.State); err != nil {
			return nil, nil, false
		}
		indexedFiles[e.Path] = true
		if e.State != "tracked" {
			nonTracked[e.Path] = e
		}
	}
	return indexedFiles, nonTracked, rows.Err() == nil
}

func shouldSkipTrackedIndexEntry(d Deps, e guard.GitIndexEntry) bool {
	if e.Stage != 0 {
		return true
	}
	modeType := e.Mode & 0o170000
	if modeType == 0o160000 || modeType == 0o120000 {
		return true
	}
	return !d.Indexed(e.Path) || harness.SecretShaped(path.Base(e.Path))
}

func scanDirtyTrackedEntries(ctx context.Context, d Deps, root string, fsRoot *os.Root, entries []guard.GitIndexEntry, idxSec int64, indexedFiles map[string]bool, nonTracked map[string]Entry) ([]Entry, []string, bool) {
	var dirty []Entry
	var labels []string
	for _, e := range entries {
		if ctx.Err() != nil {
			return nil, nil, false
		}
		if shouldSkipTrackedIndexEntry(d, e) {
			continue
		}
		if d.IsNoIndexMarker(e.Path) || !filepath.IsLocal(filepath.FromSlash(e.Path)) {
			return nil, nil, false
		}
		ch, lbl, changed := checkDirtyTrackedEntry(d, root, fsRoot, e, idxSec, indexedFiles, nonTracked)
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

func deletedIfIndexed(p, lbl string, inIndex bool) (Entry, string, bool) {
	if !inIndex {
		return Entry{}, "", false
	}
	return Entry{Path: p, State: "deleted"}, lbl, true
}

func trackedEntryMatchesStat(fi os.FileInfo, e guard.GitIndexEntry, idxSec int64) bool {
	if fi.Size() != int64(e.Size) || e.MTimeSec == 0 {
		return false
	}
	mt := fi.ModTime()
	if mt.Unix() != int64(e.MTimeSec) || uint32(mt.Nanosecond()) != e.MTimeNsec {
		return false
	}
	return e.MTimeNsec != 0 || int64(e.MTimeSec) < idxSec
}

func reconcileCleanTrackedEntry(p, idxOID string, inIndex, wasDirty bool, prev Entry) (Entry, string, bool) {
	if !inIndex || (wasDirty && prev.OID != idxOID) {
		return Entry{Path: p, OID: idxOID, State: "tracked"}, "", true
	}
	return Entry{}, "", false
}

func checkDirtyTrackedEntry(d Deps, root string, fsRoot *os.Root, e guard.GitIndexEntry, idxSec int64, indexedFiles map[string]bool, nonTracked map[string]Entry) (Entry, string, bool) {
	local := filepath.FromSlash(e.Path)
	inIndex := indexedFiles[e.Path]
	prev, wasDirty := nonTracked[e.Path]
	delete(nonTracked, e.Path)
	fi, err := os.Lstat(filepath.Join(root, local))
	if err != nil || !fi.Mode().IsRegular() {
		return deletedIfIndexed(e.Path, "D "+e.Path, inIndex)
	}
	if fi.Size() > d.maxFileBytes() {
		return deletedIfIndexed(e.Path, "", inIndex)
	}
	idxOID := fmt.Sprintf("%x", e.SHA1)
	if trackedEntryMatchesStat(fi, e, idxSec) {
		return reconcileCleanTrackedEntry(e.Path, idxOID, inIndex, wasDirty, prev)
	}
	data, err := d.ReadAll(fsRoot, local)
	if err != nil {
		return deletedIfIndexed(e.Path, "", inIndex)
	}
	oid := d.BlobID(data)
	if oid == idxOID {
		return reconcileCleanTrackedEntry(e.Path, idxOID, inIndex, wasDirty, prev)
	}
	if !inIndex || !wasDirty || prev.OID != oid {
		return Entry{Path: e.Path, OID: oid, State: "modified"}, "M " + e.Path, true
	}
	return Entry{}, "", false
}

func readUntrackedEntryOID(d Deps, fsRoot *os.Root, p string) (string, bool) {
	local := filepath.FromSlash(p)
	fi, err := fsRoot.Lstat(local)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > d.maxFileBytes() {
		return "", false
	}
	data, err := d.ReadAll(fsRoot, local)
	if err != nil {
		return "", false
	}
	return d.BlobID(data), true
}

func checkDirtyUntrackedPath(d Deps, fsRoot *os.Root, p string, remaining map[string]Entry) (Entry, bool) {
	oid, ok := readUntrackedEntryOID(d, fsRoot, p)
	if !ok {
		return Entry{}, false
	}
	if prev, okPrev := remaining[p]; okPrev && prev.OID == oid {
		return Entry{}, false
	}
	return Entry{Path: p, OID: oid, State: "untracked"}, true
}

func appendDeletedUntrackedEntries(remaining map[string]Entry, seenUntracked map[string]bool, dirty []Entry, labels []string) ([]Entry, []string) {
	for p, prev := range remaining {
		if prev.State == "untracked" && !seenUntracked[p] {
			dirty = append(dirty, Entry{Path: p, State: "deleted"})
			labels = append(labels, "D "+p)
		}
	}
	return dirty, labels
}

func scanDirtyUntrackedEntries(ctx context.Context, d Deps, root string, fsRoot *os.Root, remaining map[string]Entry, dirty []Entry, labels []string) ([]Entry, []string, bool) {
	if ctx.Err() != nil {
		return nil, nil, false
	}
	out, err := d.GitCtx(ctx, root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, nil, false
	}
	seenUntracked := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if d.IsNoIndexMarker(p) {
			return nil, nil, false
		}
		if !d.Indexed(p) || harness.SecretShaped(path.Base(p)) {
			continue
		}
		seenUntracked[p] = true
		if ch, changed := checkDirtyUntrackedPath(d, fsRoot, p, remaining); changed {
			dirty = append(dirty, ch)
			labels = append(labels, "?? "+p)
		}
	}
	dirty, labels = appendDeletedUntrackedEntries(remaining, seenUntracked, dirty, labels)
	return dirty, labels, true
}

func reconcileHookScopeView(ctx context.Context, d Deps, root string, st *Store, existingFiles int) (*Scope, string, bool, error) {
	view, leftOut, err := d.GitView(root)
	if err != nil {
		return nil, "", false, err
	}
	dirtyPaths := collectDirtyPaths(view)
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	diffN, _ := d.DiffCount(st, root, view)
	capLimit := hookMaxDirtyFastPath
	if existingFiles > 0 {
		capLimit = DynamicDirtyFastPathCap(ctx, existingFiles)
	}
	if existingFiles > 0 && diffN > capLimit {
		return &Scope{St: st, Projects: []string{root}, LeftOut: leftOut}, FormatDirtyFastPath(dirtyPaths, diffN, capLimit), true, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	if _, err := d.ApplyView(st, root, view); err != nil {
		return nil, "", false, err
	}
	if len(leftOut) == 0 && d.SaveGitWarmSig != nil {
		d.SaveGitWarmSig(st, root)
	}
	return &Scope{St: st, Projects: []string{root}, LeftOut: leftOut}, "", false, nil
}

func collectDirtyPaths(view []Entry) []string {
	var out []string
	for _, e := range view {
		switch e.State {
		case "modified":
			out = append(out, "M "+e.Path)
		case "untracked":
			out = append(out, "?? "+e.Path)
		}
	}
	return out
}

// FormatDirtyFastPath formats the fast-path summary note when dirty count exceeds cap.
func FormatDirtyFastPath(dirtyPaths []string, diffN, threshold int) string {
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
