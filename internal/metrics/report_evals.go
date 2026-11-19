package metrics

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/search"
)

const healthFileLimit = 1 << 20

// evalsSection is the per-row view as the report shows it, under the
// pass^k chart: the table of `fyr metrics evals` and its suite lines.
type evalsSection struct {
	Table table
	Lines []string
}

// evalsSectionOf is e's section, or nil when e holds no row: a report with
// no suite trial in its window has no such section.
func evalsSectionOf(e *EvalRows) *evalsSection {
	if e == nil || len(e.Rows) == 0 {
		return nil
	}
	return &evalsSection{Table: table{Head: evalHead, Rows: e.Table()}, Lines: e.Suite.lines()}
}

// ReportEvalsWithDevFallback returns eval rows from s, falling back read-only to
// <home>/<StateDir>/dev/fyr-eval/<StateDir>/metrics.db when s holds no eval rows
// in the window and o.Project and o.Label are empty.
func ReportEvalsWithDevFallback(s *Store, home string, o ShowOptions) (*EvalRows, error) {
	if o.Project != "" || o.Label != "" {
		return nil, nil
	}
	ev, err := s.Evals(EvalOptions{Since: o.Since, Now: o.Now})
	if err != nil || len(ev.Rows) > 0 {
		return ev, err
	}
	h := resolveMetricsHome(ShowOptions{Home: home}, s.path)
	if h == "" {
		return ev, nil
	}
	devStore, cleanup, ok := openDevEvalStoreRO(h)
	if !ok {
		return ev, nil
	}
	defer cleanup()
	devEv, derr := devStore.Evals(EvalOptions{Since: o.Since, Now: o.Now})
	if derr != nil || len(devEv.Rows) == 0 {
		return ev, nil
	}
	return devEv, nil
}

func mergeDevEvalPassK(sum *Summary, home string, since, now time.Time) {
	devStore, cleanup, ok := openDevEvalStoreRO(home)
	if !ok {
		return
	}
	defer cleanup()
	devOpt := ShowOptions{Since: since, Now: now, AllLabels: true}
	devAll, err := devStore.sessionsIn(showQuery(devOpt, since, now))
	if err != nil {
		return
	}
	devTrials, err := devStore.trialsIn(devOpt, since, now)
	if err != nil {
		return
	}
	devClaimed, err := devStore.trialRuns()
	if err != nil {
		return
	}
	m := map[string]PassK{}
	tmp := &Summary{A: Outcome{PassK: m}}
	tmp.passK(devAll, devTrials, devClaimed)
	mergeUnseenPassK(sum, tmp.A.PassK)
}

func mergeUnseenPassK(sum *Summary, extra map[string]PassK) {
	if sum.A.PassK == nil {
		m := map[string]PassK{}
		sum.A.PassK = m
	}
	merged := false
	for k, pk := range extra {
		if len(sum.A.PassK) != 0 && !strings.HasPrefix(k, evalPrefix) {
			continue
		}
		if _, exists := sum.A.PassK[k]; !exists {
			sum.A.PassK[k] = pk
			merged = true
		}
	}
	if merged {
		sum.Notes = append(sum.Notes, "evals: merged pass^k from dev/fyr-eval store")
	}
}

func openDevEvalStoreRO(home string) (*Store, func(), bool) {
	db, cleanup, ok := openCopySQLiteFile(home, home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	if !ok {
		return nil, nil, false
	}
	var verStr string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&verStr); err != nil {
		cleanup()
		return nil, nil, false
	}
	ver, err := strconv.Atoi(verStr)
	if err != nil || ver < 5 || ver > len(migrations) {
		cleanup()
		return nil, nil, false
	}
	devPath := filepath.Join(home, harness.StateDir, "dev", "fyr-eval", harness.StateDir, "metrics.db")
	return &Store{db: db, path: devPath}, cleanup, true
}

func openCopySQLiteFile(scratchHome, base string, relParts ...string) (*sql.DB, func(), bool) {
	dbPath, ok := regularNoSymlinkFile(base, relParts...)
	if !ok || !regularSQLiteCompanions(dbPath) {
		return nil, nil, false
	}
	tmpParent, createdDirs, ok := copySQLiteTempParent(scratchHome, dbPath)
	if !ok {
		return nil, nil, false
	}
	tmp, err := os.MkdirTemp(tmpParent, "fyr-metrics-ro-")
	if err != nil {
		cleanupCreatedDirs(createdDirs)
		return nil, nil, false
	}
	db, err := search.OpenCopy(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		cleanupCreatedDirs(createdDirs)
		return nil, nil, false
	}
	cleanup := func() {
		_ = db.Close()
		_ = os.RemoveAll(tmp)
		cleanupCreatedDirs(createdDirs)
	}
	return db, cleanup, true
}

func copySQLiteTempParent(scratchHome, dbPath string) (string, []string, bool) {
	h := strings.TrimSpace(scratchHome)
	if h == "" {
		dbDir := filepath.Dir(filepath.Clean(dbPath))
		agentsDir := filepath.Dir(dbDir)
		if filepath.Base(dbDir) != "scratch" || filepath.Base(agentsDir) != ".agents" {
			return "", nil, false
		}
		if exists, ok := verifyNoSymlinkDir(agentsDir); !ok || !exists {
			return "", nil, false
		}
		if exists, ok := verifyNoSymlinkDir(dbDir); !ok || !exists {
			return "", nil, false
		}
		return dbDir, nil, true
	}
	cur := filepath.Clean(h)
	var dirs []string
	exists, ok := verifyNoSymlinkDir(cur)
	if !ok {
		return "", nil, false
	}
	if !exists {
		dirs = append(dirs, cur)
	}
	for _, seg := range strings.Split(filepath.ToSlash(harness.StateDir), "/") {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		exists, ok = verifyNoSymlinkDir(cur)
		if !ok {
			return "", nil, false
		}
		if !exists {
			dirs = append(dirs, cur)
		}
	}
	if err := os.MkdirAll(cur, 0o700); err != nil {
		cleanupCreatedDirs(dirs)
		return "", nil, false
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

func cleanupCreatedDirs(dirs []string) {
	for i := len(dirs) - 1; i >= 0; i-- {
		if entries, err := os.ReadDir(dirs[i]); err == nil && len(entries) == 0 {
			_ = os.Remove(dirs[i])
		}
	}
}

func readBoundedRegularFile(base string, relParts ...string) ([]byte, bool) {
	p, ok := regularNoSymlinkFile(base, relParts...)
	if !ok {
		return nil, false
	}
	fi, err := os.Lstat(p)
	if err != nil || fi.Size() > healthFileLimit {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return data, true
}

func regularNoSymlinkFile(base string, relParts ...string) (string, bool) {
	if strings.TrimSpace(base) == "" || len(relParts) == 0 {
		return "", false
	}
	var segs []string
	for _, p := range relParts {
		for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
			if seg != "" && seg != "." {
				segs = append(segs, seg)
			}
		}
	}
	if len(segs) == 0 {
		return "", false
	}
	cur := filepath.Clean(base)
	for i, seg := range segs {
		cur = filepath.Join(cur, seg)
		fi, err := os.Lstat(cur)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			return "", false
		}
		if (i < len(segs)-1 && !fi.IsDir()) || (i == len(segs)-1 && !fi.Mode().IsRegular()) {
			return "", false
		}
	}
	return cur, true
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

func tableCols(db *sql.DB, table string) map[string]bool {
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
