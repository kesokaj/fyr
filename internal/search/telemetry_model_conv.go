package search

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxHookConvDBBytes     = 8 << 20
	modelDisplayMetaPrefix = "model_display:"
	modelDisplayCacheTTL   = 6 * time.Hour
)

func effectiveHookSessionModel(home string, in HookInput) string {
	if m := resolveHookSessionModelForHome(home, in); m != "" {
		return m
	}
	return strings.TrimSpace(in.ModelName)
}

func resolveHookSessionModelForHome(home string, in HookInput) string {
	if m := cleanRawModelID(in.ModelName); m != "" {
		return m
	}
	id, _ := resolveHookConvModelInfo(context.Background(), home, in, "")
	return cleanRawModelID(id)
}

func resolveHookConvModelInfo(ctx context.Context, home string, in HookInput, wantID string) (string, string) {
	if ctx == nil {
		ctx = context.Background()
	} else if ctx.Err() != nil {
		return "", ""
	}
	dbPath, brainSubdir := resolveHookConvDBPathForHome(home, in)
	if dbPath == "" || brainSubdir == "" {
		return "", ""
	}
	return queryConvGenMetadataModelInfo(ctx, dbPath, brainSubdir, wantID)
}

func resolvePreInvocationModelInfo(ctx context.Context, home string, cat modelsCatalogFile, hasCat bool, sc *scope, in HookInput) resolvedModelInfo {
	if raw := cleanRawModelID(in.ModelName); raw != "" {
		id, disp, think := resolveModelDetailsWithCatalog(cat, hasCat, raw)
		if disp == "" {
			disp = resolveNonCatalogDisplay(ctx, home, sc, in, id)
		}
		if strings.EqualFold(disp, id) {
			disp = ""
		}
		return resolvedModelInfo{ID: id, Display: disp, Thinking: think}
	}
	convID, convDisp := resolveHookConvModelInfo(ctx, home, in, "")
	id, disp, think := resolveModelDetailsWithCatalog(cat, hasCat, convID)
	if disp == "" {
		disp = fillEmptyAutoDisplay(ctx, sc, id, convDisp)
	}
	if strings.EqualFold(disp, id) {
		disp = ""
	}
	return resolvedModelInfo{ID: id, Display: disp, Thinking: think}
}

func resolveNonCatalogDisplay(ctx context.Context, home string, sc *scope, in HookInput, id string) string {
	cached, fresh := readCachedModelDisplay(ctx, sc, id)
	if fresh {
		return cached
	}
	if _, convDisp := resolveHookConvModelInfo(ctx, home, in, id); convDisp != "" {
		writeCachedModelDisplay(ctx, sc, id, convDisp)
		return convDisp
	}
	return cached
}

func fillEmptyAutoDisplay(ctx context.Context, sc *scope, id, convDisp string) string {
	if convDisp != "" {
		writeCachedModelDisplay(ctx, sc, id, convDisp)
		return convDisp
	}
	if id != "" {
		cached, _ := readCachedModelDisplay(ctx, sc, id)
		return cached
	}
	return ""
}

func modelDisplayMetaKey(id string) string {
	clean := strings.ToLower(cleanRawModelID(id))
	if clean == "" {
		return ""
	}
	return modelDisplayMetaPrefix + clean
}

func readCachedModelDisplay(ctx context.Context, sc *scope, id string) (string, bool) {
	if sc == nil || sc.st == nil || sc.st.db == nil {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	} else if ctx.Err() != nil {
		return "", false
	}
	key := modelDisplayMetaKey(id)
	if key == "" {
		return "", false
	}
	var val string
	if err := sc.st.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&val); err != nil {
		return "", false
	}
	tsStr, disp, ok := strings.Cut(val, "\t")
	if !ok {
		return "", false
	}
	disp = collapseOneLine(printable(disp), 120)
	if disp == "" {
		return "", false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil {
		return disp, false
	}
	return disp, time.Since(time.Unix(ts, 0)) < modelDisplayCacheTTL
}

func writeCachedModelDisplay(ctx context.Context, sc *scope, id, disp string) {
	if sc == nil || sc.st == nil || sc.st.db == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else if ctx.Err() != nil {
		return
	}
	key := modelDisplayMetaKey(id)
	cleanDisp := collapseOneLine(printable(disp), 120)
	if key == "" || cleanDisp == "" {
		return
	}
	val := strconv.FormatInt(time.Now().Unix(), 10) + "\t" + cleanDisp
	_, _ = sc.st.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, val)
}

func resolveHookConvDBPathForHome(home string, in HookInput) (string, string) {
	target := resolveProcessArtifactPathForHome(home, in)
	if target == "" {
		return "", ""
	}
	brainSubdir := filepath.Dir(target)
	convID := strings.TrimSpace(in.ConversationID)
	if convID == "" {
		convID = filepath.Base(brainSubdir)
	}
	if convID == "" || strings.ContainsAny(convID, `/\`) || !filepath.IsLocal(convID+".db") {
		return "", ""
	}
	appDataDir := filepath.Dir(filepath.Dir(brainSubdir))
	convDir := filepath.Join(appDataDir, "conversations")
	if st, err := os.Lstat(convDir); err != nil || st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return "", ""
	}
	dbPath := filepath.Join(convDir, convID+".db")
	if !regularBoundedSQLiteFile(dbPath, maxHookConvDBBytes) {
		return "", ""
	}
	return dbPath, brainSubdir
}

func regularBoundedSQLiteFile(dbPath string, maxBytes int64) bool {
	st, err := os.Lstat(dbPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxBytes {
		return false
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if cst, err := os.Lstat(dbPath + suffix); err == nil && (!cst.Mode().IsRegular() || cst.Size() > maxBytes) {
			return false
		}
	}
	return true
}

func queryConvGenMetadataModelInfo(ctx context.Context, dbPath, brainSubdir, wantID string) (string, string) {
	cleanWant := cleanRawModelID(wantID)
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return "", ""
		}
		id, disp, err := readConvGenMetadataModelInfoOnce(ctx, dbPath, brainSubdir, cleanWant)
		if err == nil || !isUnreadable(err) {
			return id, disp
		}
	}
	return "", ""
}

func convCopyTempRoot(brainSubdir string) string {
	if st, err := os.Lstat(brainSubdir); err == nil {
		if st.Mode()&os.ModeSymlink == 0 && st.IsDir() {
			return brainSubdir
		}
		return ""
	}
	parent := filepath.Dir(brainSubdir)
	if st, err := os.Lstat(parent); err == nil && st.Mode()&os.ModeSymlink == 0 && st.IsDir() {
		return parent
	}
	return ""
}

func readConvGenMetadataModelInfoOnce(ctx context.Context, dbPath, brainSubdir, wantID string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	tempRoot := convCopyTempRoot(brainSubdir)
	if tempRoot == "" {
		return "", "", os.ErrNotExist
	}
	tmp, err := os.MkdirTemp(tempRoot, ".fyr-conv-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	cp, err := copyDB(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	db, err := sql.Open("sqlite", roDSN(cp))
	if err != nil {
		return "", "", err
	}
	defer db.Close()
	limit := 4
	if wantID != "" {
		limit = 16
	}
	rows, err := db.QueryContext(ctx, `SELECT data FROM gen_metadata ORDER BY idx DESC LIMIT ?`, limit)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	return scanGenMetadataModelRows(rows, wantID)
}

func scanGenMetadataModelRows(rows *sql.Rows, wantID string) (string, string, error) {
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return "", "", err
		}
		id, disp := extractGenMetadataModelInfo(blob)
		if wantID == "" {
			if id != "" {
				return id, disp, nil
			}
			continue
		}
		if disp != "" && strings.EqualFold(cleanRawModelID(id), wantID) {
			return id, disp, nil
		}
	}
	return "", "", rows.Err()
}

func extractGenMetadataModelInfo(blob []byte) (string, string) {
	var concID, fbID, disp string
	for _, f := range ProtoFields(blob) {
		if f.Wire != 2 || len(f.Path) != 2 {
			continue
		}
		val := strings.TrimSpace(printable(string(f.Bytes)))
		if val == "" || strings.EqualFold(val, "auto") {
			continue
		}
		switch {
		case f.Path[0] == 3 && f.Path[1] == 28 && concID == "":
			concID = val
		case f.Path[0] == 1 && f.Path[1] == 19 && fbID == "":
			fbID = val
		case f.Path[0] == 1 && f.Path[1] == 21 && disp == "":
			disp = collapseOneLine(val, 120)
		}
	}
	if concID != "" {
		return concID, disp
	}
	return fbID, disp
}
