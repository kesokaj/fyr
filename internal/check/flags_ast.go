package check

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
)

type flagSite struct {
	file string
	line int
}

type cmdFyrScan struct {
	flags         map[string]flagSite
	hasFlagImport bool
	hasHelpText   bool
}

func (s cmdFyrScan) hasASTFlags() bool {
	return len(s.flags) > 0 || s.hasFlagImport
}

var (
	astFlagNameRe    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
	globalCheckFlags = map[string]bool{"home": true, "repo": true, "dir": true, "C": true, "help": true, "h": true}
)

// scanCMDFyrFlags AST-scans tracked non-test cmd/fyr/*.go files for FlagSet
// flag declarations (drift-M4, drift-M5b).
func (k *checker) scanCMDFyrFlags() cmdFyrScan {
	sc := cmdFyrScan{flags: map[string]flagSite{}}
	fset := token.NewFileSet()
	for _, p := range k.tracked {
		if path.Dir(p) != "cmd/fyr" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		if strings.HasPrefix(path.Base(p), "help_text") {
			sc.hasHelpText = true
		}
		data, err := k.readFile(filepath.FromSlash(p))
		if err != nil {
			continue
		}
		file, err := parser.ParseFile(fset, p, data, 0)
		if err != nil {
			continue
		}
		if extractASTFlags(fset, file, p, sc.flags) {
			sc.hasFlagImport = true
		}
	}
	return sc
}

func extractASTFlags(fset *token.FileSet, file *ast.File, rel string, out map[string]flagSite) bool {
	hasFlag := false
	for _, imp := range file.Imports {
		if unq, err := strconv.Unquote(imp.Path.Value); err == nil && unq == "flag" {
			hasFlag = true
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, ok := flagCallName(call); ok {
			if _, exists := out[name]; !exists {
				out[name] = flagSite{file: rel, line: fset.Position(call.Pos()).Line}
			}
		}
		return true
	})
	return hasFlag
}

func flagCallName(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	argIdx := -1
	switch sel.Sel.Name {
	case "BoolVar", "StringVar", "IntVar", "Int64Var", "UintVar", "Uint64Var", "Float64Var", "DurationVar":
		if len(call.Args) == 4 {
			argIdx = 1
		}
	case "Var":
		if len(call.Args) == 3 {
			argIdx = 1
		}
	case "Bool", "String", "Int", "Int64", "Uint", "Uint64", "Float64", "Duration":
		if len(call.Args) == 3 {
			argIdx = 0
		}
	}
	if argIdx < 0 {
		return "", false
	}
	name, ok := stringLitValue(call.Args[argIdx])
	if !ok || !astFlagNameRe.MatchString(name) {
		return "", false
	}
	return name, true
}

// effectiveVocabulary intersects v with the AST-scanned flags in cmd/fyr/*.go
// when cmd/fyr declares flags (drift-M5b), so a flag deleted in the tree fails
// docsRule even under an unrebuilt installed binary.
func (k *checker) effectiveVocabulary(v Vocabulary, sc cmdFyrScan) Vocabulary {
	if !sc.hasASTFlags() {
		return v
	}
	out := make(Vocabulary, len(v))
	for verb, spec := range v {
		ns := VerbSpec{Flags: filterFlagMap(spec.Flags, sc.flags)}
		if spec.Subverbs != nil {
			ns.Subverbs = make(map[string]map[string]bool, len(spec.Subverbs))
			for sub, flags := range spec.Subverbs {
				ns.Subverbs[sub] = filterFlagMap(flags, sc.flags)
			}
		}
		out[verb] = ns
	}
	return out
}

func filterFlagMap(src map[string]bool, keep map[string]flagSite) map[string]bool {
	out := make(map[string]bool, len(src))
	for f := range src {
		if globalCheckFlags[f] {
			out[f] = true
			continue
		}
		if _, ok := keep[f]; ok {
			out[f] = true
		}
	}
	return out
}

type scopedFlagCandidate struct {
	verb, subverb, flag string
	site                flagSite
}

func (c scopedFlagCandidate) scopeKey() string {
	if c.subverb != "" {
		return c.verb + " " + c.subverb
	}
	return c.verb
}

type docFlagIndex struct {
	scoped     map[string]map[string]bool // "verb" or "verb sub" -> flag set
	any        map[string]bool
	unattached map[string]bool
	aliases    map[string]string
}

var (
	verbAliasRe = regexp.MustCompile(`\b([a-z][a-z-]*) is an alias of ([a-z][a-z-]*)\b`)
	wordSpanRe  = regexp.MustCompile(`\S+`)
)

func (d *docFlagIndex) addScope(scope string, flags map[string]bool) {
	if len(flags) == 0 || scope == "" {
		return
	}
	dst := d.scoped[scope]
	if dst == nil {
		dst = map[string]bool{}
		d.scoped[scope] = dst
	}
	for f := range flags {
		dst[f] = true
		d.any[f] = true
	}
}

func (d *docFlagIndex) add(verb, sub string, flags map[string]bool) {
	d.addScope(verb, flags)
	if sub != "" {
		d.addScope(verb+" "+sub, flags)
	}
}

func (d *docFlagIndex) applyAliases() {
	for alias, target := range d.aliases {
		d.addScope(alias, d.scoped[target])
		for k, flags := range d.scoped {
			if rest, ok := strings.CutPrefix(k, target+" "); ok {
				d.addScope(alias+" "+rest, flags)
			}
		}
	}
}

func (d *docFlagIndex) isDocumented(c scopedFlagCandidate, owners int) bool {
	if c.verb == "" {
		return d.any[c.flag]
	}
	return d.scoped[c.scopeKey()][c.flag] || (owners == 1 && (d.scoped[c.verb][c.flag] || d.unattached[c.flag]))
}

// undocumentedFlagsRule reports non-global CLI flags in cmd/fyr (from AST scan
// and Vocabulary) that are absent from tracked docs, living records, and CLI
// help text for the verb/subverb that accepts them (drift-M4, F6).
func (k *checker) undocumentedFlagsRule(docsFiles []string, living []brain.Record, effVocab, rawVocab Vocabulary, sc cmdFyrScan) []Finding {
	if !sc.hasASTFlags() && !sc.hasHelpText {
		return nil
	}
	vocab := effVocab
	if !sc.hasASTFlags() {
		vocab = rawVocab
	}
	cands, owners := collectScopedCandidates(vocab, sc)
	if len(cands) == 0 {
		return nil
	}
	idx := k.documentedScopedFlags(docsFiles, living, vocab)
	var out []Finding
	for _, c := range cands {
		if idx.isDocumented(c, owners[c.flag]) {
			continue
		}
		msg := fmt.Sprintf("`--%s`: CLI flag in cmd/fyr is not documented in docs, specs or help_text", c.flag)
		if key := c.scopeKey(); key != "" {
			msg = fmt.Sprintf("`--%s` on `fyr %s`: CLI flag in cmd/fyr is not documented in docs, specs or help_text", c.flag, key)
		}
		out = append(out, Finding{Where: c.site.file, Line: c.site.line, Rule: "flag-undoc", Text: msg})
	}
	return out
}

func collectScopedCandidates(vocab Vocabulary, sc cmdFyrScan) ([]scopedFlagCandidate, map[string]int) {
	var cands []scopedFlagCandidate
	owners := map[string]int{}
	add := func(verb, sub, name string) {
		if globalCheckFlags[name] {
			return
		}
		site, ok := sc.flags[name]
		if !ok {
			site = flagSite{file: "cmd/fyr"}
		}
		cands = append(cands, scopedFlagCandidate{verb: verb, subverb: sub, flag: name, site: site})
		owners[name]++
	}
	for verb, spec := range vocab {
		if verb != "help" {
			addVerbCandidates(verb, spec, add)
		}
	}
	for name, s := range sc.flags {
		if !globalCheckFlags[name] && owners[name] == 0 {
			cands = append(cands, scopedFlagCandidate{flag: name, site: s})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.scopeKey() != b.scopeKey() {
			return a.scopeKey() < b.scopeKey()
		}
		return a.flag < b.flag
	})
	return cands, owners
}

func addVerbCandidates(verb string, spec VerbSpec, add func(verb, sub, name string)) {
	if len(spec.Subverbs) == 0 {
		for f := range spec.Flags {
			add(verb, "", f)
		}
		return
	}
	covered := map[string]bool{}
	for sub, flags := range spec.Subverbs {
		for f := range flags {
			covered[f] = true
			add(verb, sub, f)
		}
	}
	for f := range spec.Flags {
		if !covered[f] {
			add(verb, "", f)
		}
	}
}

func (k *checker) documentedScopedFlags(docsFiles []string, living []brain.Record, vocab Vocabulary) *docFlagIndex {
	idx := &docFlagIndex{scoped: map[string]map[string]bool{}, any: map[string]bool{}, unattached: map[string]bool{}, aliases: map[string]string{}}
	for _, p := range docsFiles {
		if !notInRuleA[p] {
			if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
				recordScopedText(string(data), vocab, idx)
			}
		}
	}
	for _, r := range living {
		recordScopedText(r.Body, vocab, idx)
	}
	for _, p := range k.tracked {
		if path.Dir(p) == "cmd/fyr" && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
				extractHelpFileFlags(data, strings.HasPrefix(path.Base(p), "help_text"), vocab, idx)
			}
		}
	}
	idx.applyAliases()
	return idx
}

func extractHelpFileFlags(data []byte, wholeFile bool, vocab Vocabulary, idx *docFlagIndex) {
	file, err := parser.ParseFile(token.NewFileSet(), "", data, 0)
	if err != nil {
		return
	}
	if wholeFile {
		extractHelpTextAST(file, vocab, idx)
		return
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && strings.HasSuffix(fn.Name.Name, "Help") && returnsSingleString(fn.Type) {
			for _, s := range collectStringLits(fn.Body) {
				recordScopedText(s, vocab, idx)
			}
		}
	}
}

func extractHelpTextAST(file *ast.File, vocab Vocabulary, idx *docFlagIndex) {
	ast.Inspect(file, func(n ast.Node) bool {
		if verb, comp, ok := matchVerbPageKV(n, vocab); ok {
			recordVerbPageLit(verb, comp, vocab, idx)
			return false
		}
		if unq, ok := stringLitValue(n); ok {
			recordScopedText(unq, vocab, idx)
		}
		return true
	})
}

func matchVerbPageKV(n ast.Node, vocab Vocabulary) (string, *ast.CompositeLit, bool) {
	kv, ok := n.(*ast.KeyValueExpr)
	if !ok {
		return "", nil, false
	}
	verb, ok := stringLitValue(kv.Key)
	if !ok || verb == "help" {
		return "", nil, false
	}
	if _, known := vocab[verb]; !known {
		return "", nil, false
	}
	comp, ok := kv.Value.(*ast.CompositeLit)
	return verb, comp, ok
}

func recordVerbPageLit(verb string, comp *ast.CompositeLit, vocab Vocabulary, idx *docFlagIndex) {
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		field, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		text := strings.Join(collectStringLits(kv.Value), "\n")
		switch field.Name {
		case "synopsis":
			recordScopedText(text, vocab, idx)
		case "about":
			recordVerbAbout(verb, text, vocab, idx)
		}
	}
}

func recordVerbAbout(verb, text string, vocab Vocabulary, idx *docFlagIndex) {
	for _, m := range verbAliasRe.FindAllStringSubmatch(text, -1) {
		if _, ok := vocab[m[1]]; ok && m[2] == verb {
			idx.aliases[m[1]] = verb
		}
	}
	if len(vocab[verb].Subverbs) == 0 {
		flags := map[string]bool{}
		collectFlagTokens(text, flags)
		idx.addScope(verb, flags)
		return
	}
	recordScopedText(text, vocab, idx)
}

func collectStringLits(root ast.Node) []string {
	var out []string
	ast.Inspect(root, func(n ast.Node) bool {
		if s, ok := stringLitValue(n); ok {
			out = append(out, s)
		}
		return true
	})
	return out
}

func stringLitValue(n ast.Node) (string, bool) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	unq, err := strconv.Unquote(lit.Value)
	return unq, err == nil
}

type fyrCmdSpan struct {
	start     int
	verb, sub string
}

type scopeLineState struct {
	activeVerb, activeSub, headVerb, headSub string
	contSlash, contUsage                     bool
}

func (st *scopeLineState) resetActive() {
	st.activeVerb, st.activeSub, st.contSlash, st.contUsage = "", "", false, false
}

func recordScopedText(text string, vocab Vocabulary, idx *docFlagIndex) {
	var st scopeLineState
	for _, line := range strings.Split(text, "\n") {
		st.processLine(line, vocab, idx)
	}
}

func (st *scopeLineState) processLine(line string, vocab Vocabulary, idx *docFlagIndex) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		st.headVerb, st.headSub = "", ""
		if sp := findFyrCommands(trimmed, vocab); len(sp) == 1 {
			st.headVerb, st.headSub = sp[0].verb, sp[0].sub
		}
		st.resetActive()
		return
	}
	if spans := findFyrCommands(line, vocab); len(spans) > 0 {
		recordLineSpans(line, spans, idx)
		last := spans[len(spans)-1]
		st.activeVerb, st.activeSub = last.verb, last.sub
		st.contSlash = strings.HasSuffix(trimmed, `\`)
		st.contUsage = strings.HasPrefix(trimmed, "usage:") || (isIndentedLine(line) && strings.HasPrefix(trimmed, "fyr "))
		return
	}
	st.recordContinuationOrProse(line, trimmed, idx)
}

func (st *scopeLineState) recordContinuationOrProse(line, trimmed string, idx *docFlagIndex) {
	inCont := st.activeVerb != "" && trimmed != "" && (st.contSlash || (st.contUsage && isIndentedLine(line)))
	flags := map[string]bool{}
	collectFlagTokens(line, flags)
	switch {
	case inCont:
		idx.add(st.activeVerb, st.activeSub, flags)
		st.contSlash = strings.HasSuffix(trimmed, `\`)
	case st.headVerb != "":
		st.resetActive()
		idx.add(st.headVerb, st.headSub, flags)
	default:
		st.resetActive()
		for f := range flags {
			idx.any[f], idx.unattached[f] = true, true
		}
	}
}

func recordLineSpans(line string, spans []fyrCmdSpan, idx *docFlagIndex) {
	for i, sp := range spans {
		end := len(line)
		if i+1 < len(spans) {
			end = spans[i+1].start
		}
		flags := map[string]bool{}
		collectFlagTokens(line[sp.start:end], flags)
		idx.add(sp.verb, sp.sub, flags)
	}
}

func isIndentedLine(line string) bool { return len(line) > 0 && (line[0] == ' ' || line[0] == '\t') }

func findFyrCommands(line string, vocab Vocabulary) []fyrCmdSpan {
	idx := wordSpanRe.FindAllStringIndex(line, -1)
	var out []fyrCmdSpan
	for i := 0; i < len(idx); i++ {
		next, ok := matchFyrProg(line, idx, i)
		if !ok {
			continue
		}
		next = skipGlobalFlagWords(line, idx, next)
		if next >= len(idx) {
			continue
		}
		verb := strings.Trim(line[idx[next][0]:idx[next][1]], "`\"'()[]{}.,;:|")
		spec, known := vocab[verb]
		if !known || verb == "help" {
			continue
		}
		out = append(out, fyrCmdSpan{start: idx[i][0], verb: verb, sub: matchSubverbToken(line, idx[next+1:], spec)})
		i = next
	}
	return out
}

func matchFyrProg(line string, idx [][]int, i int) (int, bool) {
	w := strings.TrimLeft(line[idx[i][0]:idx[i][1]], "`\"'([")
	if w == "fyr" || w == "./fyr" || strings.HasSuffix(w, "/bin/fyr") {
		return i + 1, true
	}
	if w == "go" && i+2 < len(idx) && line[idx[i+1][0]:idx[i+1][1]] == "run" &&
		strings.TrimRight(line[idx[i+2][0]:idx[i+2][1]], "`\"'") == "./cmd/fyr" {
		return i + 3, true
	}
	return 0, false
}

func skipGlobalFlagWords(line string, idx [][]int, i int) int {
	for i < len(idx) {
		k, _, hasEq := strings.Cut(strings.Trim(line[idx[i][0]:idx[i][1]], "`\"'[]()"), "=")
		if k != "--home" && k != "--repo" && k != "--dir" && k != "-C" {
			break
		}
		if hasEq {
			i++
		} else {
			i += 2
		}
	}
	return i
}

func matchSubverbToken(line string, rest [][]int, spec VerbSpec) string {
	if len(spec.Subverbs) == 0 {
		return ""
	}
	for _, sp := range rest {
		tok := line[sp[0]:sp[1]]
		if strings.HasPrefix(strings.TrimLeft(tok, "`\"'(["), "-") {
			continue
		}
		if w := strings.Trim(tok, "`\"'()[]{}.,;:|"); spec.Subverbs[w] != nil {
			return w
		}
		return ""
	}
	return ""
}

func returnsSingleString(ft *ast.FuncType) bool {
	if ft == nil || ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	id, ok := ft.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "string"
}

func collectFlagTokens(text string, out map[string]bool) {
	for i := 0; i+1 < len(text); i++ {
		if text[i] != '-' || !isFlagBoundaryBefore(text, i) {
			continue
		}
		dashes := 1
		if text[i+1] == '-' {
			dashes = 2
		}
		start := i + dashes
		if start >= len(text) || !isASCIIAlpha(text[start]) {
			continue
		}
		j := start + 1
		for j < len(text) && isFlagTokenChar(text[j]) {
			j++
		}
		name := strings.TrimRight(text[start:j], "-")
		if name != "" && astFlagNameRe.MatchString(name) {
			out[name] = true
		}
		i = j - 1
	}
}

func isFlagBoundaryBefore(text string, i int) bool {
	if i == 0 {
		return true
	}
	switch text[i-1] {
	case ' ', '\t', '\n', '\r', '[', '(', '{', ',', '|', '/', ':', ';':
		return true
	case '*':
		j := i - 1
		for j > 0 && text[j-1] == '*' {
			j--
		}
		return j == 0 || !isFlagTokenChar(text[j-1])
	case '`', '"', '\'':
		return i < 2 || !isFlagTokenChar(text[i-2])
	default:
		return false
	}
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isFlagTokenChar(b byte) bool {
	return isASCIIAlpha(b) || (b >= '0' && b <= '9') || b == '-'
}
