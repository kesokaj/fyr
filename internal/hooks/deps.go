package hooks

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kesokaj/fyr/internal/hooks/guard"
	"github.com/kesokaj/fyr/internal/hooks/telemetry"
	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/symgraph"
	"github.com/kesokaj/fyr/internal/vet"
)

// Entry represents one file entry during dirty-path fast-path reconciliation.
type Entry struct {
	Path  string
	OID   string
	State string
}

// Store is an opaque database handle passed between internal/search and internal/hooks.
type Store struct {
	SQLDB             *sql.DB
	Path              string
	Raw               any
	CloseFn           func() error
	WithImmediateTxFn func(func() error) error
	LookupFTSFn       func(string) ([]symgraph.FTSChunkHit, bool)
	StoreFTSFn        func(string, []symgraph.FTSChunkHit)
	ftsMu             sync.Mutex
	ftsCache          map[string][]symgraph.FTSChunkHit
}

// DB implements readledger.DBStore.
func (s *Store) DB() *sql.DB {
	if s == nil {
		return nil
	}
	return s.SQLDB
}

// WithImmediateTx implements readledger.DBStore.
func (s *Store) WithImmediateTx(fn func() error) error {
	if s == nil || s.WithImmediateTxFn == nil {
		return fn()
	}
	return s.WithImmediateTxFn(fn)
}

// Close implements readledger.DBStore.
func (s *Store) Close() {
	_ = s.CloseErr()
}

// CloseErr closes the underlying store handle and returns any error.
func (s *Store) CloseErr() error {
	if s == nil {
		return nil
	}
	if s.CloseFn != nil {
		return s.CloseFn()
	}
	if s.SQLDB != nil {
		return s.SQLDB.Close()
	}
	return nil
}

// LookupFTSTurnCache returns cached FTS fallback chunks for key if present.
func (s *Store) LookupFTSTurnCache(k string) ([]symgraph.FTSChunkHit, bool) {
	if s == nil {
		return nil, false
	}
	if s.LookupFTSFn != nil {
		return s.LookupFTSFn(k)
	}
	s.ftsMu.Lock()
	defer s.ftsMu.Unlock()
	if s.ftsCache == nil {
		return nil, false
	}
	hits, ok := s.ftsCache[k]
	return hits, ok
}

// StoreFTSTurnCache stores FTS fallback chunks for key in the per-store turn cache.
func (s *Store) StoreFTSTurnCache(k string, hits []symgraph.FTSChunkHit) {
	if s == nil {
		return
	}
	if s.StoreFTSFn != nil {
		s.StoreFTSFn(k, hits)
		return
	}
	s.ftsMu.Lock()
	if s.ftsCache == nil {
		s.ftsCache = map[string][]symgraph.FTSChunkHit{}
	}
	s.ftsCache[k] = hits
	s.ftsMu.Unlock()
}

// Scope holds an open Store and the project roots covered by a hook query.
type Scope struct {
	St       *Store
	Projects []string
	LeftOut  []string
}

// Close closes the underlying store if open.
func (sc *Scope) Close() {
	if sc != nil && sc.St != nil {
		_ = sc.St.CloseErr()
	}
}

func (sc *Scope) inClause() string {
	if sc == nil || len(sc.Projects) == 0 {
		return "('')"
	}
	return "(" + strings.TrimRight(strings.Repeat("?,", len(sc.Projects)), ",") + ")"
}

func (sc *Scope) projectArgs() []any {
	if sc == nil {
		return nil
	}
	out := make([]any, len(sc.Projects))
	for i, p := range sc.Projects {
		out[i] = p
	}
	return out
}

// Options carries hook-relevant options and callbacks from internal/search.
type Options struct {
	Dir                  string
	Home                 string
	Session              string
	Vet                  string
	Hook                 string
	ActiveFiles          map[string]bool
	BrainRefsForDir      func(dir, target string) []symgraph.LinkedRecord
	BrainReprime         func(dir, home string) string
	BrainTurnContext     func(dir, home, prompt string, files []string) vet.TurnBrainContext
	BrainTurnContextCtx  func(ctx context.Context, dir, home, prompt string, files []string) vet.TurnBrainContext
	FormatContractDigest func(dir, home string, paths []string, maxBytes int) string
	SkillCatalog         func(home string) []vet.SkillMeta
	HookDeadline         time.Duration
	HookDispatch         func(context.Context, Options, string, string, HookInput, io.Writer) error
	NowFunc              func() time.Time
}

// Deps supplies store, index, git, and text callbacks from internal/search so
// internal/hooks remains strictly acyclic and free of SQLite driver imports.
type Deps struct {
	Now                           func() time.Time
	ProjectDB                     func(root string) string
	GuardProject                  func(root string) error
	OpenStore                     func(path string) (*Store, error)
	OpenLedgerStore               func(dir string) (*Store, string, error)
	OpenSessionLedgerStore        func(sessionDir, home string) (*Store, string, error)
	OpenShortTimeoutStoreCtx      func(ctx context.Context, path string) *Store
	OpenSliceReadOnlyStore        func(path string) *Store
	ResolveSessionLedgerDir       func(sessionDir, toolDir string) string
	CheckAndRecordResolvedRead    func(st *Store, root, convID, resolved string, startLine, endLine, turnNum int) (readledger.ReadDecision, error)
	CheckPreWriteHashWithEdit     func(sessionDir, home, toolDir, convID, target string, args map[string]any) (readledger.ReadDecision, error)
	RegisterCapsulePivotsWithHome func(dir, home, convID string, turnNum int, pivots []readledger.CapsulePivot) error
	MarkPreWriteInLedger          func(dir, convID, resolvedPath string) error
	RecordStopCoActivity          func(dir, convID string) error
	RelPathInRepo                 func(root, p string) string
	MatchesGitWarmSig             func(ctx context.Context, st *Store, root string) bool
	MatchesGitIdxHeadSig          func(ctx context.Context, st *Store, root string) bool
	SaveGitWarmSig                func(st *Store, root string)
	ApplyDirtyPaths               func(ctx context.Context, st *Store, root string, dirty []Entry, existingFiles int) error
	GitView                       func(root string) ([]Entry, []string, error)
	DiffCount                     func(st *Store, root string, view []Entry) (int, error)
	ApplyView                     func(st *Store, root string, view []Entry) (int, error)
	GitCtx                        func(ctx context.Context, dir string, args ...string) ([]byte, error)
	BlobID                        func(data []byte) string
	Indexed                       func(path string) bool
	IsNoIndexMarker               func(path string) bool
	ReadAll                       func(fsRoot *os.Root, rel string) ([]byte, error)
	MaxFileBytes                  int64
	MaxWarmSigEntries             int
	SessionActiveFiles            func(sc *Scope, sessionID string, extra map[string]bool) map[string]bool
	LoadBoundedSymGraphCtx        func(ctx context.Context, sc *Scope, prompt string, active map[string]bool, includeDocs bool, maxHops int) (*symgraph.Graph, error)
	RenderCompactHookCapsule      func(sc *Scope, o Options, prompt string, g *symgraph.Graph, tiers symgraph.SliceTiers, maxBytes int) string
	FTSQuery                      func(words []string, op string) string
	Printable                     func(s string) string
	TruncateMultilineUTF8         func(s string, maxB int) string
	DefaultContractDigestBytes    int
	CopyDB                        func(dir, name, tmp string) (string, error)
	OpenCopy                      func(dir, name, tmp string) (*sql.DB, error)
	RODSN                         func(path string) string
	ProtoFields                   func(msg []byte) []telemetry.ProtoField
	UsageError                    func(msg string) error
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d Deps) printable(s string) string {
	if d.Printable != nil {
		return d.Printable(s)
	}
	return s
}

func (d Deps) maxFileBytes() int64 {
	if d.MaxFileBytes > 0 {
		return d.MaxFileBytes
	}
	return 1 << 20
}

func (d Deps) maxWarmSigEntries() int {
	if d.MaxWarmSigEntries > 0 {
		return d.MaxWarmSigEntries
	}
	return 2500
}

func (d Deps) defaultContractDigestBytes() int {
	if d.DefaultContractDigestBytes > 0 {
		return d.DefaultContractDigestBytes
	}
	return 600
}

func (d Deps) usageErr(msg string) error {
	if d.UsageError != nil {
		return d.UsageError(msg)
	}
	return errors.New(msg)
}

func (d Deps) openGuardLedgerDB(root string) (*sql.DB, func(), error) {
	if d.OpenLedgerStore == nil {
		return nil, nil, guard.ErrNotCheckout
	}
	st, _, err := d.OpenLedgerStore(root)
	if err != nil || st == nil {
		return nil, nil, err
	}
	return st.SQLDB, func() { st.Close() }, nil
}

func (d Deps) guardWatchdogRoleDeps() guard.WatchdogRoleDeps {
	return guard.WatchdogRoleDeps{RODSN: d.RODSN, OpenCopy: d.OpenCopy}
}

func (d Deps) guardNudgeDeps() guard.NudgeDeps {
	return guard.NudgeDeps{
		HooksDisabled:     HooksDisabledForHome,
		NormalizeToolName: NormalizeHookToolName,
		OpenLedgerDB:      d.openGuardLedgerDB,
		MarkSentinelOnce:  MarkSessionSentinelOnce,
		WriteDecision:     writeDecision,
	}
}

func (d Deps) telemetryConvDeps(sc *Scope) telemetry.ConvDeps {
	var metaDB *sql.DB
	if sc != nil && sc.St != nil {
		metaDB = sc.St.SQLDB
	}
	return telemetry.ConvDeps{
		MetaDB:      metaDB,
		CopyDB:      d.CopyDB,
		RODSN:       d.RODSN,
		ProtoFields: d.ProtoFields,
	}
}

func (d Deps) telemetryArtifactDeps(sc *Scope) telemetry.ArtifactDeps {
	return telemetry.ArtifactDeps{
		HooksDisabled: HooksDisabledForHome,
		RecordVetHistory: func(ctx context.Context, convID string, turnNum int, rawPrompt string, res vet.Result, pivotFiles []string, m vet.TurnMetrics) error {
			if sc == nil || sc.St == nil || sc.St.SQLDB == nil {
				return nil
			}
			return vet.RecordHistoryCtx(ctx, sc.St.SQLDB, d.now().Unix(), convID, turnNum, rawPrompt, res, pivotFiles, m)
		},
		ListVetHistory: func(dir, convID string, limit int) ([]vet.HistoryRow, bool) {
			return d.listVetHistoryForArtifact(sc, dir, convID, limit)
		},
	}
}

func (d Deps) listVetHistoryForArtifact(sc *Scope, dir, convID string, limit int) ([]vet.HistoryRow, bool) {
	if sc != nil && sc.St != nil && sc.St.SQLDB != nil {
		rows, _ := vet.ListConversationHistory(sc.St.SQLDB, convID, limit)
		return rows, true
	}
	if d.OpenStore == nil || d.ProjectDB == nil {
		return nil, false
	}
	root, _, ok := guard.FindGitRootFast(dir)
	if !ok {
		return nil, false
	}
	st, err := d.OpenStore(d.ProjectDB(root))
	if err != nil || st == nil {
		return nil, false
	}
	defer st.Close()
	rows, _ := vet.ListConversationHistory(st.SQLDB, convID, limit)
	return rows, true
}

func toTelemetryConvInput(in HookInput) telemetry.ConvInput {
	return telemetry.ConvInput{
		ConversationID:        in.ConversationID,
		ArtifactDirectoryPath: in.ArtifactDirectoryPath,
		TranscriptPath:        in.TranscriptPath,
		ModelName:             in.ModelName,
	}
}

func guardAskInput(ctx context.Context, o Options, dir string, wd watchdogPreState, in HookInput) guard.AskInput {
	meta := hookSubagentMetaFromCtx(ctx)
	return guard.AskInput{
		Home: o.Home, Dir: dir, ConversationID: in.ConversationID,
		LastUserInput: in.LastUserInput, TranscriptPath: in.TranscriptPath,
		ToolCallArgs: in.ToolCall.Args, ParentConversationID: meta.ParentConversationID,
		AgentName: meta.AgentName, WatchdogRole: wd.role,
	}
}
