package report

import (
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
)

// HealthFileLimit caps bounded regular-file reads for health telemetry.
const HealthFileLimit = 1 << 20

type evalsSection struct {
	Table Table
	Lines []string
}

func evalsSectionOf(e *EvalView) *evalsSection {
	if e == nil || len(e.Rows) == 0 {
		return nil
	}
	return &evalsSection{Table: Table{Head: e.Head, Rows: e.Rows}, Lines: e.Lines}
}

// MergeUnseenPassK merges unseen pass^k labels from extra into current.
func MergeUnseenPassK(current, extra map[string]PassK, evalPrefix string) (map[string]PassK, bool) {
	if current == nil {
		current = map[string]PassK{}
	}
	merged := false
	for k, pk := range extra {
		if len(current) != 0 && !strings.HasPrefix(k, evalPrefix) {
			continue
		}
		if _, exists := current[k]; !exists {
			current[k] = pk
			merged = true
		}
	}
	return current, merged
}

// OpenDevEvalDB opens <home>/<StateDir>/dev/fyr-eval/<StateDir>/metrics.db
// via a symlink-safe read-only copy when present and schema in [5, maxSchema].
func OpenDevEvalDB(home string, maxSchema int) (*sql.DB, string, func(), bool) {
	db, cleanup, ok := OpenCopySQLiteFile(home, home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	if !ok {
		return nil, "", nil, false
	}
	var verStr string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&verStr); err != nil {
		cleanup()
		return nil, "", nil, false
	}
	ver, err := strconv.Atoi(verStr)
	if err != nil || ver < 5 || ver > maxSchema {
		cleanup()
		return nil, "", nil, false
	}
	devPath := filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	return db, devPath, cleanup, true
}

// OpenCopySQLiteFile opens a symlink-safe read-only copy of base/relParts.
func OpenCopySQLiteFile(scratchHome, base string, relParts ...string) (*sql.DB, func(), bool) {
	db, _, cleanup, ok := OpenCopySQLiteFileWithPath(scratchHome, base, relParts...)
	return db, cleanup, ok
}

// OpenCopySQLiteFileWithPath opens a symlink-safe read-only copy of
// base/relParts and returns the copied file path along with cleanup.
func OpenCopySQLiteFileWithPath(scratchHome, base string, relParts ...string) (*sql.DB, string, func(), bool) {
	dbPath, ok := RegularNoSymlinkFile(base, relParts...)
	if !ok || !regularSQLiteCompanions(dbPath) {
		return nil, "", nil, false
	}
	tmpParent, createdDirs, ok := CopySQLiteTempParent(scratchHome, dbPath)
	if !ok {
		return nil, "", nil, false
	}
	tmp, err := os.MkdirTemp(tmpParent, "fyr-metrics-ro-")
	if err != nil {
		CleanupCreatedDirs(createdDirs)
		return nil, "", nil, false
	}
	db, err := search.OpenCopy(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		CleanupCreatedDirs(createdDirs)
		return nil, "", nil, false
	}
	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(tmp)
		CleanupCreatedDirs(createdDirs)
	}
	return db, filepath.Join(tmp, filepath.Base(dbPath)), cleanup, true
}

func copySQLiteScratchParent(dbPath string) (string, bool) {
	dbDir := filepath.Dir(filepath.Clean(dbPath))
	agentsDir := filepath.Dir(dbDir)
	if filepath.Base(dbDir) != "scratch" || filepath.Base(agentsDir) != ".agents" {
		return "", false
	}
	for _, d := range []string{agentsDir, dbDir} {
		if exists, ok := verifyNoSymlinkDir(d); !ok || !exists {
			return "", false
		}
	}
	return dbDir, true
}

func CopySQLiteTempParent(scratchHome, dbPath string) (string, []string, bool) {
	h := strings.TrimSpace(scratchHome)
	if h == "" {
		parent, ok := copySQLiteScratchParent(dbPath)
		return parent, nil, ok
	}
	cur, dirs, ok := verifyStateDirPath(filepath.Clean(h))
	if !ok {
		return "", nil, false
	}
	if err := os.MkdirAll(cur, 0o700); err != nil {
		CleanupCreatedDirs(dirs)
		return "", nil, false
	}
	return cur, dirs, true
}

func verifyStateDirPath(base string) (string, []string, bool) {
	cur := base
	var dirs []string
	exists, ok := verifyNoSymlinkDir(cur)
	if !ok {
		return "", nil, false
	}
	if !exists {
		dirs = append(dirs, cur)
	}
	for _, seg := range collectRelSegments([]string{harness.StateDir}) {
		cur = filepath.Join(cur, seg)
		exists, ok = verifyNoSymlinkDir(cur)
		if !ok {
			return "", nil, false
		}
		if !exists {
			dirs = append(dirs, cur)
		}
	}
	return cur, dirs, true
}

func verifyNoSymlinkDir(dir string) (bool, bool) {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, true
	}
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return false, false
	}
	return true, true
}

func CleanupCreatedDirs(dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		if entries, err := os.ReadDir(dirs[i]); err == nil && len(entries) == 0 {
			_ = os.Remove(dirs[i])
		}
	}
}

// ReadBoundedRegularFile reads up to HealthFileLimit bytes from a symlink-free
// regular file under base/relParts.
func ReadBoundedRegularFile(base string, relParts ...string) ([]byte, bool) {
	p, ok := RegularNoSymlinkFile(base, relParts...)
	if !ok {
		return nil, false
	}
	f, err := trail.OpenRegular(p)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, HealthFileLimit+1))
	if err != nil || len(data) > HealthFileLimit {
		return nil, false
	}
	return data, true
}

// RegularNoSymlinkFile verifies every component under base/relParts is free of
// symlinks and ends in a regular file.
func RegularNoSymlinkFile(base string, relParts ...string) (string, bool) {
	if strings.TrimSpace(base) == "" || len(relParts) == 0 {
		return "", false
	}
	segs := collectRelSegments(relParts)
	if len(segs) == 0 {
		return "", false
	}
	cur := filepath.Clean(base)
	for i, seg := range segs {
		cur = filepath.Join(cur, seg)
		if !isValidPathComponent(cur, i == len(segs)-1) {
			return "", false
		}
	}
	return cur, true
}

func collectRelSegments(relParts []string) []string {
	var segs []string
	for _, p := range relParts {
		for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
			if seg != "" && seg != "." {
				segs = append(segs, seg)
			}
		}
	}
	return segs
}

func isValidPathComponent(p string, isLast bool) bool {
	fi, err := os.Lstat(p)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if isLast {
		return fi.Mode().IsRegular()
	}
	return fi.IsDir()
}

func regularSQLiteCompanions(dbPath string) bool {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		fi, err := os.Lstat(dbPath + suffix)
		if errors.Is(err, os.ErrNotExist) {
			if suffix == "" {
				return false
			}
			continue
		}
		if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			return false
		}
	}
	return true
}

// TableCols returns lowercase column names present in SQLite table.
func TableCols(db *sql.DB, table string) map[string]bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil {
			cols[strings.ToLower(name)] = true
		}
	}
	return cols
}
