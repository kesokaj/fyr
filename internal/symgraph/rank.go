package symgraph

import (
	"bytes"
	"database/sql"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	// HookMinIdentLen is the minimum identifier length for hook symbol matching.
	HookMinIdentLen = 6
	// MaxFileBytes is the largest file read into the index or go.mod scanner.
	MaxFileBytes = 1 << 20
	// DefaultContractDigestBytes caps rendered spec_contracts blocks in slices.
	DefaultContractDigestBytes = 600
	// DefaultHiddenCouplingMinCount is the default minimum co-activity count for hidden coupling.
	DefaultHiddenCouplingMinCount = 3

	binarySniffBytes = 8192
	multInPrompt     = 10.0
	multLongIdent    = 10.0
	multUnexported   = 0.1
	multManyFiles    = 0.1
	multActiveFile   = 50.0
	pprIterations    = 20
	pprDamping       = 0.85
)

// LinkedRecord is a BUG-NNN or ADR-NNN record linked to a file or symbol.
type LinkedRecord struct {
	ID     string
	Status string
	Title  string
}

// StructuralSignals holds strictly numeric, shape-only structural and behavioral health metrics.
type StructuralSignals struct {
	HiddenCouplingPairs     int     `json:"hiddenCouplingPairs"`
	HiddenCouplingEditPairs int     `json:"hiddenCouplingEditPairs"`
	HiddenCouplingReadPairs int     `json:"hiddenCouplingReadPairs"`
	HiddenCouplingRate      float64 `json:"hiddenCouplingRate"`
	GodNodeClampHits        int     `json:"godNodeClampHits"`
	GodNodeRepeatReads      int     `json:"godNodeRepeatReads"`
	GodNodeChurnRate        float64 `json:"godNodeChurnRate"`
	ProdPkgCycles           int     `json:"prodPkgCycles"`
	HubConcentration        float64 `json:"hubConcentration"`
	LargeFilesOver350       int     `json:"largeFilesOver350"`
	TotalCodeFiles          int     `json:"totalCodeFiles"`
	TotalProdPkgs           int     `json:"totalProdPkgs"`
}

// HiddenCouplingHint is a project-local redacted co-activity pair lacking any AST edge.
type HiddenCouplingHint struct {
	FileA string `json:"fileA"`
	FileB string `json:"fileB"`
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// FileChurnStat tracks read clamp and repeat-read counts for one file.
type FileChurnStat struct {
	ClampHits   int
	RepeatReads int
}

// FTSChunkHit is one FTS5 fallback chunk result passed from internal/search.
type FTSChunkHit struct {
	Path    string
	Line    int
	Heading string
	Body    string
}

// SliceOptions carries query and callback options for slice and graph query helpers.
type SliceOptions struct {
	Slice            string
	Hops             int
	Budget           int
	Session          string
	Path             string
	To               string
	Affected         string
	IncludeDocs      bool
	Hook             string
	ActiveFiles      map[string]bool
	BrainRefs        func(target string) []LinkedRecord
	FormatSpecDigest func(paths []string, maxBytes int) string
}

// Scope holds the SQLite handle, workspace projects, and host callbacks from internal/search.
type Scope struct {
	DB                  *sql.DB
	Projects            []string
	ActiveFiles         func(sessionID string, extra map[string]bool) map[string]bool
	LinkedRecords       func(paths map[string]bool, brainRefs func(string) []LinkedRecord) []LinkedRecord
	CoChangeHints       func(paths map[string]bool) []string
	FTSFallbackChunks   func(words []string, lowPrompt string) []FTSChunkHit
	ExtractFTSWords     func(prompt string) []string
	LineText            func(oid string, line int) string
	ComputeSignals      func(db *sql.DB, root string, prodGraph *Graph) StructuralSignals
	QueryFileChurnStats func(db *sql.DB, root string) (map[string]FileChurnStat, int)
	QueryHiddenCoupling func(db *sql.DB, root string, minCount int, astLinked map[[2]string]bool) []HiddenCouplingHint
}

// FTSFallbackStopwords lists common prose words skipped by bounded symbol queries and FTS fallback.
var FTSFallbackStopwords = map[string]bool{
	"about": true, "added": true, "adding": true, "after": true, "again": true, "also": true, "always": true,
	"another": true, "based": true, "before": true, "being": true, "below": true, "better": true,
	"between": true, "both": true, "broken": true, "build": true, "change": true, "changes": true,
	"check": true, "checks": true, "clean": true, "cleanup": true, "could": true, "count": true,
	"create": true, "current": true, "debug": true, "delete": true, "does": true, "doing": true,
	"done": true, "each": true, "else": true, "enough": true, "ensure": true, "error": true,
	"errors": true, "even": true, "every": true, "example": true, "existing": true, "explain": true,
	"failed": true, "failing": true, "fails": true, "find": true, "first": true,
	"from": true, "further": true, "give": true, "good": true, "great": true,
	"have": true, "having": true, "help": true, "here": true, "however": true,
	"implement": true, "improve": true, "improved": true, "improving": true, "inspect": true, "into": true, "investigate": true, "issue": true, "just": true, "keep": true, "know": true,
	"like": true, "list": true, "loading": true, "look": true, "looking": true,
	"make": true, "making": true, "many": true, "modify": true, "more": true, "most": true,
	"much": true, "must": true, "need": true, "never": true, "next": true,
	"note": true, "notes": true, "often": true, "only": true, "other": true,
	"over": true, "part": true, "please": true, "problem": true, "question": true,
	"refactor": true, "remove": true, "rename": true, "return": true, "returns": true, "review": true, "same": true,
	"section": true, "several": true, "should": true, "show": true, "since": true,
	"skill": true, "skills": true, "some": true, "something": true, "still": true,
	"such": true, "sure": true, "take": true, "tell": true, "than": true,
	"that": true, "their": true, "them": true, "then": true, "there": true,
	"these": true, "they": true, "thing": true, "think": true, "this": true,
	"those": true, "through": true, "trace": true, "under": true, "until": true, "update": true, "upon": true,
	"used": true, "using": true, "value": true, "values": true, "verify": true, "very": true, "want": true, "wants": true,
	"well": true, "were": true, "what": true, "when": true, "where": true,
	"whether": true, "which": true, "while": true, "with": true, "without": true,
	"work": true, "working": true, "works": true, "would": true, "write": true,
	"your": true,
}

// SortedMapKeys returns the true keys of m in sorted order.
func SortedMapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// RoundSignal4 rounds v to 4 decimal places.
func RoundSignal4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// TruncateBytesUTF8 trims s to at most maxB bytes on a valid UTF-8 rune boundary.
func TruncateBytesUTF8(s string, maxB int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxB {
		return s
	}
	i := maxB
	for i > 0 && (s[i]&0xc0) == 0x80 {
		i--
	}
	return strings.TrimSpace(s[:i])
}

// TableHasColumn reports whether table in db defines col.
func TableHasColumn(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ReadIndexable reads a bounded regular non-binary file from fsRoot.
func ReadIndexable(fsRoot *os.Root, rel string) (string, bool) {
	fi, err := fsRoot.Lstat(rel)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > MaxFileBytes {
		return "", false
	}
	f, err := fsRoot.Open(rel)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", false
	}
	sniff := data
	if len(sniff) > binarySniffBytes {
		sniff = sniff[:binarySniffBytes]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "", false
	}
	return string(data), true
}

// Rank scores every symbol in g with Personalized PageRank biased toward query and activeFiles.
func (g *Graph) Rank(query string, activeFiles map[string]bool) []RankedSymbol {
	if len(g.Syms) == 0 {
		return nil
	}
	terms := ParseQueryTerms(query)
	mul, matched := g.computeMultipliers(terms, activeFiles)
	pers := g.buildPersonalization(mul, matched, activeFiles)
	scores := g.runPowerMethod(mul, pers, len(terms) > 0 || len(activeFiles) > 0)
	for i := range g.Syms {
		g.Syms[i].Score = scores[i]
	}
	out := make([]RankedSymbol, len(g.Syms))
	copy(out, g.Syms)
	sort.Slice(out, func(i, j int) bool {
		if math.Abs(out[i].Score-out[j].Score) > 1e-12 {
			return out[i].Score > out[j].Score
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].StartLine < out[j].StartLine
	})
	return out
}

func (g *Graph) computeMultipliers(terms []string, activeFiles map[string]bool) ([]float64, []bool) {
	mul, matched := make([]float64, len(g.Syms)), make([]bool, len(g.Syms))
	for i, s := range g.Syms {
		m := 1.0
		if matched[i] = MatchesPromptTerms(s, terms); matched[i] {
			m *= multInPrompt
		}
		if len(s.Name) >= 8 {
			m *= multLongIdent
		}
		if !s.Exported {
			m *= multUnexported
		}
		if g.defFile[s.Name] > 5 {
			m *= multManyFiles
		}
		if activeFiles[s.Path] {
			m *= multActiveFile
		}
		if !matched[i] && strings.HasSuffix(s.Path, "_test.go") {
			m *= multUnexported
		}
		mul[i] = m
	}
	return mul, matched
}

func (g *Graph) buildPersonalization(mul []float64, matched []bool, activeFiles map[string]bool) []float64 {
	pers, hasSeed, sum := make([]float64, len(g.Syms)), false, 0.0
	for i, s := range g.Syms {
		if matched[i] || activeFiles[s.Path] {
			hasSeed = true
			break
		}
	}
	for i, s := range g.Syms {
		w := mul[i] / math.Sqrt(float64(max(1, s.InRefs)))
		if hasSeed {
			if matched[i] || activeFiles[s.Path] {
				w = mul[i]
			} else {
				w *= 1e-3
			}
		}
		pers[i] = w
		sum += w
	}
	if sum > 0 {
		for i := range pers {
			pers[i] /= sum
		}
	}
	return pers
}

func (g *Graph) runPowerMethod(mul, pers []float64, personalized bool) []float64 {
	n := len(g.Syms)
	scores, next, nodeW := make([]float64, n), make([]float64, n), make([]float64, n)
	for i, s := range g.Syms {
		nodeW[i] = mul[i] / math.Sqrt(float64(max(1, s.InRefs)))
	}
	copy(scores, pers)
	revFactor := 0.0
	if personalized {
		revFactor = 0.35
	}
	for iter := 0; iter < pprIterations; iter++ {
		for i := range next {
			next[i] = (1.0 - pprDamping) * pers[i]
		}
		for u := 0; u < n; u++ {
			g.stepNodePower(u, mul[u], nodeW, scores[u], revFactor, next)
		}
		copy(scores, next)
	}
	return scores
}

func (g *Graph) stepNodePower(u int, selfW float64, nodeW []float64, scoreU, revFactor float64, next []float64) {
	denom := selfW
	for _, e := range g.Out[u] {
		denom += e.Weight * nodeW[e.ToIdx]
	}
	for _, e := range g.In[u] {
		if e.FromIdx >= 0 && revFactor > 0 {
			denom += revFactor * e.Weight * nodeW[e.FromIdx]
		}
	}
	if denom <= 0 {
		return
	}
	mass := pprDamping * scoreU / denom
	next[u] += mass * selfW
	for _, e := range g.Out[u] {
		next[e.ToIdx] += mass * e.Weight * nodeW[e.ToIdx]
	}
	for _, e := range g.In[u] {
		if e.FromIdx >= 0 && revFactor > 0 {
			next[e.FromIdx] += mass * revFactor * e.Weight * nodeW[e.FromIdx]
		}
	}
}

// ParseQueryTerms splits query into deduplicated symbol/path search terms.
func ParseQueryTerms(query string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(query, func(r rune) bool {
		return !isSymWordRune(r) && r != '.' && r != '/' && r != '-'
	}) {
		if f = strings.Trim(f, "./-"); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func isSymWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// MatchesPromptTerms reports whether symbol s matches any query term by path, name, qualname, or base stem.
func MatchesPromptTerms(s RankedSymbol, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	nameLow, qualLow, pathLow := strings.ToLower(s.Name), strings.ToLower(s.QualName()), strings.ToLower(s.Path)
	baseLow := strings.ToLower(strings.TrimSuffix(filepath.Base(s.Path), filepath.Ext(s.Path)))
	shortShellSym := strings.HasSuffix(pathLow, ".sh") && len(nameLow) < HookMinIdentLen
	for _, t := range terms {
		if matchSinglePromptTerm(strings.ToLower(t), nameLow, qualLow, pathLow, baseLow, shortShellSym) {
			return true
		}
	}
	return false
}

func matchSinglePromptTerm(tl, nameLow, qualLow, pathLow, baseLow string, shortShellSym bool) bool {
	if pathLow == tl || (strings.HasSuffix(tl, ".sh") && filepath.Base(pathLow) == filepath.Base(tl)) {
		return true
	}
	if shortShellSym {
		return false
	}
	if nameLow == tl || qualLow == tl || baseLow == tl {
		return true
	}
	return len(tl) >= 4 && (strings.Contains(nameLow, tl) || strings.Contains(qualLow, tl))
}

// CollectUndirectedASTFilePairs returns undirected file pairs connected by symbol or import edges.
func (g *Graph) CollectUndirectedASTFilePairs() map[[2]string]bool {
	pairs := map[[2]string]bool{}
	for i, s := range g.Syms {
		for _, e := range g.Out[i] {
			addUndirectedFilePair(pairs, s.Path, g.Syms[e.ToIdx].Path)
		}
	}
	for _, ie := range g.ImportEdges {
		addImportEdgeFilePairs(pairs, g.Files, ie)
	}
	return pairs
}

func addUndirectedFilePair(pairs map[[2]string]bool, a, b string) {
	if a == "" || b == "" || a == b {
		return
	}
	if a > b {
		a, b = b, a
	}
	pairs[[2]string{a, b}] = true
}

func addImportEdgeFilePairs(pairs map[[2]string]bool, files []FileMeta, ie ResolvedImportEdge) {
	if ie.ToPath != "" {
		addUndirectedFilePair(pairs, ie.FromPath, ie.ToPath)
		return
	}
	for _, f := range files {
		if f.Pkg == ie.ToPkg {
			addUndirectedFilePair(pairs, ie.FromPath, f.Path)
		}
	}
}
