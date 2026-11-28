package metrics

import (
	"io"

	"github.com/kesokaj/fyr/internal/metrics/ingest"
	"github.com/kesokaj/fyr/internal/search"
)

const agyFailed = ingest.AgyFailed

var agyTools = ingest.AgyTools

type (
	// Options are one ingest's inputs (spec § Verbs).
	Options = ingest.Options
	// Result counts one ingest.
	Result  = ingest.Result
	agyConv = ingest.AgyConv
	fields  = ingest.Fields
)

// Verb reduces a shell command line to its shape: one string per command in
// it, "program [subcommand] [flag names…]".
func Verb(cmd string) []string { return ingest.Verb(cmd) }

// Ingest reads every transcript the options name into s. A source that fails
// is named in Result.Failed and the rest are still read; the error is only for
// a store that cannot be written at all, or a Run that must not reach it.
func Ingest(s *Store, o Options, log io.Writer) (Result, error) {
	return ingest.Ingest(s.ingestDeps(), o, log)
}

func (s *Store) ingestDeps() ingest.Deps {
	return ingest.Deps{
		DB:                   s.db,
		StorePath:            s.path,
		MaxSchemaVersion:     len(migrations),
		ResolveRepoID:        ResolveRepoID,
		ResolveMetricsHome:   func(home, storePath string) string { return resolveMetricsHome(ShowOptions{Home: home}, storePath) },
		ResolveWorktreeRoots: resolveWorktreeRootsFor,
		IsAgyWorktreePath:    isAgyWorktreePath,
		IsUsageProbeSummary:  isUsageProbeSummary,
		JoinEventsWithBrainHome: func(home, brainHome string) ([]string, error) {
			return s.joinEventsWithBrainHome(home, brainHome)
		},
		IsRealDirChain: isRealDirChain,
	}
}

func (s *Store) ingestWorkWaves(home string, log io.Writer) {
	s.ingestDeps().IngestWorkWaves(home, log)
}

func (s *Store) ingestAgyConversationWithOverrides(src string, c agyConv, source, tmp string, o Options, overrides map[string]int64) error {
	return s.ingestDeps().IngestAgyConversationWithOverrides(src, c, source, tmp, o, overrides)
}

// printable filters control, format, and separator characters via search.Printable.
func printable(s string) string { return search.Printable(s) }

func identifier(s string) string                     { return ingest.Identifier(s) }
func decodeWaveStamp(stored string) (string, string) { return ingest.DecodeWaveStamp(stored) }
func workRunKey(rawStamp, worker string) string      { return ingest.WorkRunKey(rawStamp, worker) }
func scopedKey(prefix, val string) string            { return ingest.ScopedKey(prefix, val) }
func recordRead(cmd string) string                   { return ingest.RecordRead(cmd) }
func stripHeredocs(cmd string) string                { return ingest.StripHeredocs(cmd) }
func hookDenialKind(s string) string                 { return ingest.HookDenialKind(s) }
func normalizeSessionModel(first string, seen map[string]bool) string {
	return ingest.NormalizeSessionModel(first, seen)
}
func normalizeSuggestedSkillsCSV(s string) string { return ingest.NormalizeSuggestedSkillsCSV(s) }
func waitMS(meta []byte, ended int64) int64       { return ingest.WaitMS(meta, ended) }
func fieldMap(b []byte) fields                    { return ingest.FieldMap(b) }
