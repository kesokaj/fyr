package search

import (
	"context"
	"database/sql"
	"io"

	"github.com/kesokaj/fyr/internal/symgraph"
)

// Exported type aliases preserving full binary and source compatibility for external callers.
type (
	LinkedRecord        = symgraph.LinkedRecord
	StructuralSignals   = symgraph.StructuralSignals
	HiddenCouplingHint  = symgraph.HiddenCouplingHint
	RankedSymbol        = symgraph.RankedSymbol
	GraphOptions        = symgraph.GraphOptions
	GraphEdge           = symgraph.GraphEdge
	GraphPackage        = symgraph.GraphPackage
	GraphFile           = symgraph.GraphFile
	GraphSymbol         = symgraph.GraphSymbol
	GraphHiddenCoupling = symgraph.GraphHiddenCoupling
	GraphSnapshot       = symgraph.GraphSnapshot
	LouvainEdge         = symgraph.LouvainEdge
)

const (
	ConfidenceExtracted = symgraph.ConfidenceExtracted
	ConfidenceInferred  = symgraph.ConfidenceInferred
	WeightExtracted     = symgraph.WeightExtracted
	WeightInferred      = symgraph.WeightInferred

	EdgeMaskCall   = symgraph.EdgeMaskCall
	EdgeMaskType   = symgraph.EdgeMaskType
	EdgeMaskIface  = symgraph.EdgeMaskIface
	EdgeMaskImport = symgraph.EdgeMaskImport
)

// Package-private type aliases for remaining internal/search files and tests.
type (
	symGraph       = symgraph.Graph
	symbolRow      = symgraph.SymbolRow
	symRefRow      = symgraph.SymRefRow
	chunk          = symgraph.Chunk
	ref            = symgraph.Ref
	fence          = symgraph.Fence
	sliceTiers     = symgraph.SliceTiers
	sliceLineCache = symgraph.SliceLineCache
	fileChurnStat  = symgraph.FileChurnStat
	ftsChunkHit    = symgraph.FTSChunkHit
)

const (
	chunkLines              = symgraph.ChunkLines
	compactHookMaxBytes     = symgraph.CompactHookMaxBytes
	defaultSliceBudget      = symgraph.DefaultSliceBudget
	defaultSliceHops        = symgraph.DefaultSliceHops
	maxBoundedQueryTerms    = symgraph.MaxBoundedQueryTerms
	maxBoundedSeedNames     = symgraph.MaxBoundedSeedNames
	maxImplicitImplementers = symgraph.MaxImplicitImplementers
)

var (
	unticked             = symgraph.Unticked
	kinds                = symgraph.Kinds
	refExtensions        = symgraph.RefExtensions
	idRefRe              = symgraph.IDRefRe
	pathRefRe            = symgraph.PathRefRe
	ftsFallbackStopwords = symgraph.FTSFallbackStopwords
)

// TarjanSCC returns strongly connected components (size >= 2) in deterministic ascending order.
func TarjanSCC(numNodes int, adj [][]int) [][]int { return symgraph.TarjanSCC(numNodes, adj) }

// TarjanPackageSCCs computes non-trivial SCCs over sorted package names and directed dependencies.
func TarjanPackageSCCs(pkgs []string, deps map[string][]string) [][]int {
	return symgraph.TarjanPackageSCCs(pkgs, deps)
}

// LouvainCommunities partitions numNodes into deterministic modularity communities.
func LouvainCommunities(numNodes int, edges []LouvainEdge) []int {
	return symgraph.LouvainCommunities(numNodes, edges)
}

// IsVendoredSkillPath reports whether p is inside a skill's vendored references/ or assets/ subtree.
func IsVendoredSkillPath(p string) bool { return symgraph.IsVendoredSkillPath(p) }

// LoadGraphSnapshot reconciles the checkout index and returns its AST + telemetry snapshot.
func LoadGraphSnapshot(opts Options, gopts GraphOptions) (*GraphSnapshot, error) {
	includeDocs := opts.IncludeDocs || gopts.IncludeDocs
	prepOpts := opts
	prepOpts.Global = false
	prepOpts.IncludeDocs = includeDocs
	sc, err := prepare(prepOpts, io.Discard)
	if err != nil {
		return nil, err
	}
	defer sc.close()
	return sc.symScope().LoadGraphSnapshot(gopts, includeDocs)
}

// BuildContextSlice emits a tiered 0/1/2-hop context capsule within o.Budget tokens (chars / 4).
func BuildContextSlice(o Options, out io.Writer) error {
	sc, err := prepare(o, out)
	if err != nil {
		return err
	}
	defer sc.close()
	return sc.buildContextSlice(o, out)
}

func (sc *scope) symScope() *symgraph.Scope {
	var db *sql.DB
	if sc != nil && sc.st != nil {
		db = sc.st.db
	}
	var projects []string
	if sc != nil {
		projects = sc.projects
	}
	return &symgraph.Scope{
		DB:                  db,
		Projects:            projects,
		ActiveFiles:         sc.sessionActiveFiles,
		LinkedRecords:       sc.activeLinkedRecords,
		CoChangeHints:       sc.activeCoChangeHints,
		FTSFallbackChunks:   sc.queryFTSFallbackChunks,
		ExtractFTSWords:     extractFTSFallbackWords,
		LineText:            sc.lineText,
		ComputeSignals:      computeStructuralSignalsFromGraph,
		QueryFileChurnStats: queryFileChurnStats,
		QueryHiddenCoupling: queryHiddenCouplingPairs,
	}
}

func toSymSliceOpts(o Options) symgraph.SliceOptions {
	var specDigest func([]string, int) string
	if o.BrainSpecBodies != nil {
		specDigest = func(paths []string, maxBytes int) string {
			return FormatContractDigest(o.BrainSpecBodies(o.Dir, o.Home, paths), maxBytes)
		}
	}
	return symgraph.SliceOptions{
		Slice:            o.Slice,
		Hops:             o.Hops,
		Budget:           o.Budget,
		Session:          o.Session,
		Path:             o.Path,
		To:               o.To,
		Affected:         o.Affected,
		IncludeDocs:      o.IncludeDocs,
		Hook:             o.Hook,
		ActiveFiles:      o.ActiveFiles,
		BrainRefs:        o.BrainRefs,
		FormatSpecDigest: specDigest,
	}
}

func (sc *scope) loadSymGraphOpts(includeDocs bool) (*symGraph, error) {
	return sc.symScope().LoadSymGraphOpts(includeDocs)
}

func (sc *scope) loadBoundedSymGraph(prompt string, active map[string]bool, maxHops int) (*symGraph, error) {
	return sc.symScope().LoadBoundedSymGraph(prompt, active, false, maxHops)
}

func (sc *scope) loadBoundedSymGraphCtx(ctx context.Context, prompt string, active map[string]bool, includeDocs bool, maxHops int) (*symGraph, error) {
	return sc.symScope().LoadBoundedSymGraphCtx(ctx, prompt, active, includeDocs, maxHops)
}

func (sc *scope) buildContextSlice(o Options, out io.Writer) error {
	return sc.symScope().BuildContextSlice(toSymSliceOpts(o), out)
}

func (sc *scope) buildFileSkeleton(targetPath string, out io.Writer) error {
	return sc.symScope().BuildFileSkeleton(targetPath, out)
}

func (sc *scope) normalizePath(p string) string {
	return sc.symScope().NormalizePath(p)
}

func (sc *scope) findSymbolPath(o Options, out io.Writer) error {
	return sc.symScope().FindSymbolPath(toSymSliceOpts(o), out)
}

func (sc *scope) findAffected(o Options, out io.Writer) error {
	return sc.symScope().FindAffected(toSymSliceOpts(o), out)
}

func (sc *scope) repoMapSummary(o Options, out io.Writer) error {
	return sc.symScope().RepoMapSummary(toSymSliceOpts(o), out)
}

func (sc *scope) renderCompactHookCapsule(o Options, g *symGraph, tiers sliceTiers, maxBytes int) string {
	return sc.symScope().RenderCompactHookCapsule(toSymSliceOpts(o), g, tiers, maxBytes)
}

func (sc *scope) formatHop0Symbol(cache sliceLineCache, s RankedSymbol, remBytes int) string {
	return sc.symScope().FormatHop0Symbol(cache, s, remBytes)
}

func kindOf(p, docsRoot string) string                     { return symgraph.KindOf(p, docsRoot) }
func splitLines(text string) []string                      { return symgraph.SplitLines(text) }
func chunkText(p, text string) []chunk                     { return symgraph.ChunkText(p, text) }
func parseRefs(text string) []ref                          { return symgraph.ParseRefs(text) }
func normalizeRef(t string) string                         { return symgraph.NormalizeRef(t) }
func canonicalPkgDir(filePath string) string               { return symgraph.CanonicalPkgDir(filePath) }
func isCodePath(p string) bool                             { return symgraph.IsCodePath(p) }
func isDocPath(p string) bool                              { return symgraph.IsDocPath(p) }
func isTestPath(p string) bool                             { return symgraph.IsTestPath(p) }
func isExcludedASTPath(p string) bool                      { return symgraph.IsExcludedASTPath(p) }
func vendoredExtractAlias(p string) (string, string, bool) { return symgraph.VendoredExtractAlias(p) }
func findPackageCycles(deps map[string][]string) []string  { return symgraph.FindPackageCycles(deps) }
func compactSig(s string, maxLen int) string               { return symgraph.CompactSig(s, maxLen) }

func extractSymbolsAndRefs(filePath, text string) ([]symbolRow, []symRefRow) {
	return symgraph.ExtractSymbolsAndRefs(filePath, text)
}

func extractSymbolsAndRefsForIndex(filePath, text string) ([]symbolRow, []symRefRow) {
	return symgraph.ExtractSymbolsAndRefsForIndex(filePath, text)
}

func extractMarkdownSymbols(p, text string) ([]symbolRow, []symRefRow) {
	return symgraph.ExtractMarkdownSymbols(p, text)
}

func matchImplicitInterfaces(syms []symbolRow) []symRefRow {
	return symgraph.MatchImplicitInterfaces(syms)
}

func loadGoModulesFromDB(db *sql.DB, projects []string) map[string]string {
	return symgraph.LoadGoModulesFromDB(db, projects)
}

func readGoModAt(root, relPath string, out map[string]string) {
	symgraph.ReadGoModAt(root, relPath, out)
}

func resolveImportTarget(fromPath, toPkg string, goMods map[string]string, pkgSet map[string]bool, fileByStem map[string]string) (string, string, bool) {
	return symgraph.ResolveImportTarget(fromPath, toPkg, goMods, pkgSet, fileByStem)
}

func extractCompactCapsuleMeta(capsule string) ([]CapsulePivot, []string, int) {
	return symgraph.ExtractCompactCapsuleMeta(capsule)
}

func buildSymGraphFromDB(db *sql.DB, projects []string, goMods map[string]string, includeDocs bool) (*symGraph, error) {
	return symgraph.BuildSymGraphFromDB(db, projects, goMods, includeDocs)
}

func queryIndexedFilesDB(db *sql.DB, projects []string, includeDocs bool) ([]symgraph.FileMeta, error) {
	return symgraph.QueryIndexedFilesDB(db, projects, includeDocs)
}

func querySymbolNodesDB(db *sql.DB, projects []string, includeDocs bool) ([]RankedSymbol, error) {
	return symgraph.QuerySymbolNodesDB(db, projects, includeDocs)
}

func querySeedSymbolsLikeMinLenDBCtx(ctx context.Context, db *sql.DB, projects []string, includeDocs bool, terms []string, maxNames, minLikeLen int) ([]RankedSymbol, error) {
	return symgraph.QuerySeedSymbolsLikeMinLenDBCtx(ctx, db, projects, includeDocs, terms, maxNames, minLikeLen)
}

func matchSymbolNamesFromIndexCtx(ctx context.Context, db *sql.DB, includeDocs bool, exact, long []string) ([]string, map[string]bool, error) {
	return symgraph.MatchSymbolNamesFromIndexCtx(ctx, db, includeDocs, exact, long)
}

func buildSymbolNameMatchSQL(includeDocs bool, exact, long []string) (string, []any) {
	return symgraph.BuildSymbolNameMatchSQL(includeDocs, exact, long)
}

func expandBoundedSymbolTerms(terms []string) []string {
	return symgraph.ExpandBoundedSymbolTerms(terms)
}
