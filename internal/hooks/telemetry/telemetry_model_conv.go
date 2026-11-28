package telemetry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/sqliteutil"
	"github.com/kesokaj/fyr/internal/vet"
)

const (
	maxHookConvDBBytes     = 8 << 20
	modelDisplayMetaPrefix = "model_display:"
	modelDisplayCacheTTL   = 6 * time.Hour
)

// ConvInput carries the conversation metadata fields needed for telemetry and model resolution.
type ConvInput struct {
	ConversationID        string
	ArtifactDirectoryPath string
	TranscriptPath        string
	ModelName             string
}

// ProtoField represents one decoded protobuf wire field from conversation gen_metadata.
type ProtoField struct {
	Path   []int
	Wire   int
	Varint uint64
	Bytes  []byte
}

// ConvDeps supplies database and protobuf callbacks for conversation metadata queries.
type ConvDeps struct {
	MetaDB      *sql.DB
	CopyDB      func(dir, name, tmp string) (string, error)
	RODSN       func(path string) string
	ProtoFields func(msg []byte) []ProtoField
}

// EffectiveHookSessionModel resolves the concrete session model ID or falls back to ModelName.
func EffectiveHookSessionModel(home string, in ConvInput, deps ConvDeps) string {
	if m := ResolveHookSessionModelForHome(home, in, deps); m != "" {
		return m
	}
	return strings.TrimSpace(in.ModelName)
}

// ResolveHookSessionModelForHome resolves a non-auto concrete model ID from hook input or conversation DB.
func ResolveHookSessionModelForHome(home string, in ConvInput, deps ConvDeps) string {
	if m := CleanRawModelID(in.ModelName); m != "" {
		return m
	}
	id, _ := resolveHookConvModelInfo(context.Background(), home, in, "", deps)
	return CleanRawModelID(id)
}

func resolveHookConvModelInfo(ctx context.Context, home string, in ConvInput, wantID string, deps ConvDeps) (string, string) {
	if ctx == nil {
		ctx = context.Background()
	} else if ctx.Err() != nil {
		return "", ""
	}
	dbPath, brainSubdir := ResolveHookConvDBPathForHome(home, in)
	if dbPath == "" || brainSubdir == "" {
		return "", ""
	}
	return queryConvGenMetadataModelInfo(ctx, dbPath, brainSubdir, wantID, deps)
}

// ResolvePreInvocationModelInfo resolves model ID, display name, and thinking effort for a pre-invocation turn.
func ResolvePreInvocationModelInfo(ctx context.Context, home string, cat ModelsCatalogFile, hasCat bool, in ConvInput, deps ConvDeps) ResolvedModelInfo {
	if raw := CleanRawModelID(in.ModelName); raw != "" {
		id, disp, think := ResolveModelDetailsWithCatalog(cat, hasCat, raw)
		if disp == "" {
			disp = resolveNonCatalogDisplay(ctx, home, in, id, deps)
		}
		if strings.EqualFold(disp, id) {
			disp = ""
		}
		return ResolvedModelInfo{ID: id, Display: disp, Thinking: think}
	}
	convID, convDisp := resolveHookConvModelInfo(ctx, home, in, "", deps)
	id, disp, think := ResolveModelDetailsWithCatalog(cat, hasCat, convID)
	if disp == "" {
		disp = fillEmptyAutoDisplay(ctx, deps.MetaDB, id, convDisp)
	}
	if strings.EqualFold(disp, id) {
		disp = ""
	}
	return ResolvedModelInfo{ID: id, Display: disp, Thinking: think}
}

func resolveNonCatalogDisplay(ctx context.Context, home string, in ConvInput, id string, deps ConvDeps) string {
	cached, fresh := readCachedModelDisplay(ctx, deps.MetaDB, id)
	if fresh {
		return cached
	}
	if _, convDisp := resolveHookConvModelInfo(ctx, home, in, id, deps); convDisp != "" {
		writeCachedModelDisplay(ctx, deps.MetaDB, id, convDisp)
		return convDisp
	}
	return cached
}

func fillEmptyAutoDisplay(ctx context.Context, metaDB *sql.DB, id, convDisp string) string {
	if convDisp != "" {
		writeCachedModelDisplay(ctx, metaDB, id, convDisp)
		return convDisp
	}
	if id != "" {
		cached, _ := readCachedModelDisplay(ctx, metaDB, id)
		return cached
	}
	return ""
}

func modelDisplayMetaKey(id string) string {
	clean := strings.ToLower(CleanRawModelID(id))
	if clean == "" {
		return ""
	}
	return modelDisplayMetaPrefix + clean
}

func readCachedModelDisplay(ctx context.Context, metaDB *sql.DB, id string) (string, bool) {
	if metaDB == nil {
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
	if err := metaDB.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&val); err != nil {
		return "", false
	}
	tsStr, disp, ok := strings.Cut(val, "\t")
	if !ok {
		return "", false
	}
	disp = vet.CollapseOneLine(Printable(disp), 120)
	if disp == "" {
		return "", false
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(tsStr), 10, 64)
	if err != nil {
		return disp, false
	}
	return disp, time.Since(time.Unix(ts, 0)) < modelDisplayCacheTTL
}

func writeCachedModelDisplay(ctx context.Context, metaDB *sql.DB, id, disp string) {
	if metaDB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else if ctx.Err() != nil {
		return
	}
	key := modelDisplayMetaKey(id)
	cleanDisp := vet.CollapseOneLine(Printable(disp), 120)
	if key == "" || cleanDisp == "" {
		return
	}
	val := strconv.FormatInt(time.Now().Unix(), 10) + "\t" + cleanDisp
	_, _ = metaDB.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, val)
}

// ResolveHookConvDBPathForHome locates the bounded conversation SQLite DB and its anchored brain subdir.
func ResolveHookConvDBPathForHome(home string, in ConvInput) (string, string) {
	if strings.TrimSpace(home) == "" {
		return "", ""
	}
	target := ResolveProcessArtifactPathForHome(home, in)
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

func queryConvGenMetadataModelInfo(ctx context.Context, dbPath, brainSubdir, wantID string, deps ConvDeps) (string, string) {
	cleanWant := CleanRawModelID(wantID)
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return "", ""
		}
		id, disp, err := readConvGenMetadataModelInfoOnce(ctx, dbPath, brainSubdir, cleanWant, deps)
		if err == nil || !sqliteutil.IsUnreadable(err) {
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

func readConvGenMetadataModelInfoOnce(ctx context.Context, dbPath, brainSubdir, wantID string, deps ConvDeps) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if deps.CopyDB == nil || deps.RODSN == nil {
		return "", "", os.ErrNotExist
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
	cp, err := deps.CopyDB(filepath.Dir(dbPath), filepath.Base(dbPath), tmp)
	if err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	db, err := sql.Open("sqlite", deps.RODSN(cp))
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
	return scanGenMetadataModelRows(rows, wantID, deps.ProtoFields)
}

func scanGenMetadataModelRows(rows *sql.Rows, wantID string, parseProto func([]byte) []ProtoField) (string, string, error) {
	for rows.Next() {
		var blob []byte
		if err := rows.Scan(&blob); err != nil {
			return "", "", err
		}
		id, disp := ExtractGenMetadataModelInfo(blob, parseProto)
		if wantID == "" {
			if id != "" {
				return id, disp, nil
			}
			continue
		}
		if disp != "" && strings.EqualFold(CleanRawModelID(id), wantID) {
			return id, disp, nil
		}
	}
	return "", "", rows.Err()
}

// ExtractGenMetadataModelInfo decodes concrete model ID and display name from a gen_metadata protobuf blob.
func ExtractGenMetadataModelInfo(blob []byte, parseProto func([]byte) []ProtoField) (string, string) {
	if parseProto == nil {
		return "", ""
	}
	var concID, fbID, disp string
	for _, f := range parseProto(blob) {
		if f.Wire != 2 || len(f.Path) != 2 {
			continue
		}
		val := strings.TrimSpace(Printable(string(f.Bytes)))
		if val == "" || strings.EqualFold(val, "auto") {
			continue
		}
		switch {
		case f.Path[0] == 3 && f.Path[1] == 28 && concID == "":
			concID = val
		case f.Path[0] == 1 && f.Path[1] == 19 && fbID == "":
			fbID = val
		case f.Path[0] == 1 && f.Path[1] == 21 && disp == "":
			disp = vet.CollapseOneLine(val, 120)
		}
	}
	if concID != "" {
		return concID, disp
	}
	return fbID, disp
}
