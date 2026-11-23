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
	lit, ok := call.Args[argIdx].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil || !astFlagNameRe.MatchString(name) {
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

// undocumentedFlagsRule reports non-global CLI flags in cmd/fyr (from AST scan
// and Vocabulary) that are absent from tracked docs, living records, and CLI
// help text (drift-M4).
func (k *checker) undocumentedFlagsRule(docsFiles []string, living []brain.Record, effVocab, rawVocab Vocabulary, sc cmdFyrScan) []Finding {
	if !sc.hasASTFlags() && !sc.hasHelpText {
		return nil
	}
	sites := collectCandidateFlags(effVocab, rawVocab, sc)
	if len(sites) == 0 {
		return nil
	}
	documented := k.documentedFlags(docsFiles, living)
	names := make([]string, 0, len(sites))
	for name := range sites {
		if !globalCheckFlags[name] && !documented[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	out := make([]Finding, 0, len(names))
	for _, name := range names {
		s := sites[name]
		out = append(out, Finding{
			Where: s.file,
			Line:  s.line,
			Rule:  "flag-undoc",
			Text:  fmt.Sprintf("`--%s`: CLI flag in cmd/fyr is not documented in docs, specs or help_text", name),
		})
	}
	return out
}

func collectCandidateFlags(effVocab, rawVocab Vocabulary, sc cmdFyrScan) map[string]flagSite {
	sites := make(map[string]flagSite, len(sc.flags))
	for name, s := range sc.flags {
		sites[name] = s
	}
	src := effVocab
	if !sc.hasASTFlags() {
		src = rawVocab
	}
	addVocab := func(m map[string]bool) {
		for name := range m {
			if _, ok := sites[name]; !ok {
				sites[name] = flagSite{file: "cmd/fyr"}
			}
		}
	}
	for _, spec := range src {
		addVocab(spec.Flags)
		for _, sub := range spec.Subverbs {
			addVocab(sub)
		}
	}
	return sites
}

func (k *checker) documentedFlags(docsFiles []string, living []brain.Record) map[string]bool {
	out := map[string]bool{}
	for _, p := range docsFiles {
		if notInRuleA[p] {
			continue
		}
		if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
			collectFlagTokens(string(data), out)
		}
	}
	for _, r := range living {
		collectFlagTokens(r.Body, out)
	}
	for _, p := range k.tracked {
		if path.Dir(p) != "cmd/fyr" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		if data, err := k.readFile(filepath.FromSlash(p)); err == nil {
			extractHelpFileFlags(data, strings.HasPrefix(path.Base(p), "help_text"), out)
		}
	}
	return out
}

func extractHelpFileFlags(data []byte, wholeFile bool, out map[string]bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", data, 0)
	if err != nil {
		return
	}
	scanNode := func(root ast.Node) {
		ast.Inspect(root, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if ok && lit.Kind == token.STRING {
				if unq, err := strconv.Unquote(lit.Value); err == nil {
					collectFlagTokens(unq, out)
				}
			}
			return true
		})
	}
	if wholeFile {
		scanNode(file)
		return
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Body != nil && strings.HasSuffix(fn.Name.Name, "Help") && returnsSingleString(fn.Type) {
			scanNode(fn.Body)
		}
	}
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
