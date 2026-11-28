package vet

import (
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// ContainsAnySubstr reports whether s contains at least one of subs.
func ContainsAnySubstr(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ExtractPromptAndSliceFiles merges sliceFiles with file/directory tokens extracted from prompt.
func ExtractPromptAndSliceFiles(prompt string, sliceFiles []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range sliceFiles {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, m := range FilePathRe.FindAllString(prompt, -1) {
		if m = strings.TrimSpace(strings.Trim(m, "`'\".,;:()")); m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// ContainsAnyWord reports whether low contains any whole word in words.
func ContainsAnyWord(low string, words ...string) bool {
	tokens := strings.FieldsFunc(low, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
	})
	for _, t := range tokens {
		if slices.Contains(words, t) {
			return true
		}
	}
	return false
}

func hasExt(files []string, exts ...string) bool {
	return countExt(files, exts...) > 0
}

func countExt(files []string, exts ...string) int {
	n := 0
	for _, f := range files {
		if slices.Contains(exts, strings.ToLower(filepath.Ext(f))) {
			n++
		}
	}
	return n
}

func promptHasExt(lowPrompt string, exts ...string) bool {
	for _, m := range FilePathRe.FindAllString(lowPrompt, -1) {
		m = strings.Trim(m, "`'\".,;:()")
		if slices.Contains(exts, strings.ToLower(filepath.Ext(m))) {
			return true
		}
	}
	return false
}

func hasBaseOrExt(files []string, subs ...string) bool {
	for _, f := range files {
		low := strings.ToLower(filepath.Base(f))
		for _, sub := range subs {
			if strings.Contains(low, sub) {
				return true
			}
		}
	}
	return false
}

// normalizeSkillCatalog deduplicates the installed catalog without fabricating
// phantom fallback skills when catalog is empty.
func normalizeSkillCatalog(catalog []SkillMeta) []SkillMeta {
	if len(catalog) == 0 {
		return nil
	}
	order := make([]string, 0, len(catalog))
	byName := make(map[string]SkillMeta, len(catalog))
	for _, s := range catalog {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		s.Name = name
		if strings.TrimSpace(s.Digest) == "" {
			s.Digest = s.Description
		}
		if strings.TrimSpace(s.Path) == "" {
			s.Path = "~/.gemini/config/skills/" + name + "/SKILL.md"
		}
		if _, exists := byName[name]; !exists {
			order = append(order, name)
		}
		byName[name] = s
	}
	out := make([]SkillMeta, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out
}

func splitSkillScope(text string) (pos, neg, loadOnly string) {
	low := strings.ToLower(text)
	cutIdx, kind := len(text), ""
	markers := []struct {
		sep  string
		kind string
	}{
		{". not for ", "neg"},
		{"; not for ", "neg"},
		{". not ", "neg"},
		{". load only ", "load"},
		{". use /", "use"},
	}
	for _, m := range markers {
		if idx := strings.Index(low, m.sep); idx >= 0 && idx < cutIdx {
			cutIdx, kind = idx, m.kind
		}
	}
	pos = strings.TrimSpace(text[:cutIdx])
	if cutIdx >= len(text) {
		return pos, "", ""
	}
	rest := strings.TrimSpace(text[cutIdx+1:])
	switch kind {
	case "neg":
		neg = rest
	case "load":
		loadOnly = rest
	}
	return pos, neg, loadOnly
}

var skillStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "as": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true, "will": true,
	"would": true, "could": true, "should": true, "can": true, "may": true, "might": true,
	"must": true, "not": true, "no": true, "nor": true, "so": true, "if": true,
	"then": true, "else": true, "when": true, "where": true, "why": true, "how": true,
	"what": true, "which": true, "who": true, "this": true, "that": true, "these": true,
	"those": true, "it": true, "its": true, "we": true, "our": true, "you": true,
	"your": true, "they": true, "their": true, "i": true, "my": true, "me": true,
	"all": true, "any": true, "both": true, "each": true, "few": true, "more": true,
	"most": true, "other": true, "some": true, "such": true, "only": true, "own": true,
	"same": true, "than": true, "too": true, "very": true, "just": true, "into": true,
	"onto": true, "over": true, "under": true, "about": true, "after": true, "before": true,
	"between": true, "through": true, "during": true, "without": true, "via": true, "per": true,
	"up": true, "out": true, "off": true, "down": true, "one": true, "two": true,
	"three": true, "new": true, "use": true, "using": true, "used": true, "make": true,
	"get": true, "set": true, "put": true, "keep": true, "take": true, "give": true,
	"say": true, "see": true, "know": true, "want": true, "need": true, "like": true,
	"also": true, "now": true, "here": true, "there": true, "yes": true, "ok": true,
	"okay": true, "please": true, "help": true, "tell": true, "explain": true, "show": true,
	"look": true, "find": true, "work": true, "works": true, "working": true, "go": true,
	"fyr": true, "build": true, "builds": true, "code": true, "app": true, "apps": true,
	"file": true, "files": true, "line": true, "lines": true, "run": true, "runs": true,
	"running": true, "local": true, "first": true, "short": true, "plain": true, "single": true,
	"task": true, "tasks": true, "step": true, "steps": true, "note": true, "notes": true,
	"rule": true, "rules": true, "path": true, "paths": true, "dir": true, "name": true,
	"turn": true, "turns": true, "end": true, "start": true, "read": true, "write": true,
	"edit": true, "update": true, "fix": true, "add": true, "create": true, "change": true,
	"changes": true, "remove": true, "delete": true, "move": true, "open": true, "close": true,
	"list": true, "load": true, "save": true, "store": true, "data": true, "error": true,
	"errors": true, "flag": true, "flags": true, "mode": true, "type": true, "value": true,
	"count": true, "index": true, "query": true, "search": true, "project": true, "repo": true,
	"repository": true, "service": true, "services": true, "system": true, "tool": true,
	"tools": true, "command": true, "commands": true, "prompt": true, "prompts": true,
	"session": true, "agent": true, "agents": true, "worker": true, "workers": true,
	"workflow": true, "standard": true, "standards": true, "check": true, "checks": true,
	"plan": true, "plans": true, "record": true, "records": true, "design": true, "forward": true,
	"test": true, "tests": true, "testing": true, "google": true, "web": true, "modern": true,
}

func buildAliasMap(pairs [][2]string) map[string]string {
	m := make(map[string]string, len(pairs))
	for _, p := range pairs {
		m[p[0]] = p[1]
	}
	return m
}

var skillTermAliases = buildAliasMap([][2]string{
	{"documentation", "docs"},
	{"doc", "docs"},
	{"docset", "docs"},
	{"retrospective", "retro"},
	{"specification", "spec"},
	{"specifications", "spec"},
	{"postgresql", "postgres"},
	{"remember", "recall"},
	{"shell", "bash"},
	{"shellcheck", "bash"},
	{"shfmt", "bash"},
	{"opentofu", "terraform"},
	{"kubernetes", "k8s"},
	{"goroutine", "golang"},
	{"gofmt", "golang"},
	{"govulncheck", "golang"},
	{"httptest", "golang"},
	{"uibutton", "slateui"},
	{"themeservice", "slateui"},
	{"angular", "angular"},
	{"sequencediagram", "mermaid"},
	{"statediagram", "mermaid"},
	{"erdiagram", "mermaid"},
	{"lowercamelcase", "aip"},
	{"nextpagetoken", "pagetoken"},
	{"set_gcp_secret", "wizard"},
	{"provideslateuitheme", "slateui"},
})

func isAllSkillDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

func stemSkillTerm(tok string) string {
	if len(tok) < 4 || strings.Contains(tok, "-") {
		return tok
	}
	switch {
	case strings.HasSuffix(tok, "ies") && len(tok) >= 5:
		return tok[:len(tok)-3] + "y"
	case strings.HasSuffix(tok, "ing") && len(tok) >= 6:
		return tok[:len(tok)-3]
	case strings.HasSuffix(tok, "es") && len(tok) >= 5 && (strings.HasSuffix(tok, "shes") || strings.HasSuffix(tok, "ches") || strings.HasSuffix(tok, "xes") || strings.HasSuffix(tok, "zes")):
		return tok[:len(tok)-2]
	case strings.HasSuffix(tok, "s") && !strings.HasSuffix(tok, "ss") && !strings.HasSuffix(tok, "us") && !strings.HasSuffix(tok, "is"):
		return tok[:len(tok)-1]
	}
	return tok
}

func addNormalizedSkillTerm(tf map[string]int, raw string) {
	t := strings.Trim(strings.ToLower(raw), "-_")
	if len(t) < 2 || isAllSkillDigits(t) {
		return
	}
	if mapped, ok := skillTermAliases[t]; ok {
		tf[mapped]++
		return
	}
	if skillStopwords[t] {
		return
	}
	stem := stemSkillTerm(t)
	if mapped, ok := skillTermAliases[stem]; ok {
		tf[mapped]++
		return
	}
	if skillStopwords[stem] {
		return
	}
	tf[stem]++
}

func extractSkillTerms(text string) map[string]int {
	return extractSkillTermsOpts(text, true)
}

func isNotSkillTermRune(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_'
}

func extractSkillTermsOpts(text string, splitHyphens bool) map[string]int {
	tf := map[string]int{}
	for _, raw := range strings.FieldsFunc(strings.ToLower(text), isNotSkillTermRune) {
		addNormalizedSkillTerm(tf, raw)
		if splitHyphens && strings.Contains(raw, "-") {
			addHyphenatedSkillTerms(tf, raw)
		}
	}
	return tf
}

func addHyphenatedSkillTerms(tf map[string]int, raw string) {
	parts := strings.Split(raw, "-")
	for _, p := range parts {
		addNormalizedSkillTerm(tf, p)
	}
	for i := 0; i+1 < len(parts); i++ {
		if len(parts[i]) >= 2 && len(parts[i+1]) >= 2 {
			addNormalizedSkillTerm(tf, parts[i]+parts[i+1])
		}
	}
}

func stripOtherSkillSlashRefs(text, selfName string, installedNames map[string]bool) string {
	if !strings.Contains(text, "/") || len(installedNames) == 0 {
		return text
	}
	selfLow := strings.ToLower(selfName)
	words := strings.Fields(text)
	kept := make([]string, 0, len(words))
	for _, w := range words {
		clean := strings.Trim(strings.ToLower(w), "().,;:\"'`")
		if strings.HasPrefix(clean, "/") {
			ref := strings.TrimPrefix(clean, "/")
			if ref != selfLow && installedNames[ref] {
				continue
			}
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, " ")
}

func extractPromptQueryTerms(lowPrompt string) map[string]int {
	tf := extractSkillTerms(lowPrompt)
	phraseSignals := []struct {
		sub  string
		term string
	}{
		{"go test", "golang"}, {"go build", "golang"}, {"go run", "golang"},
		{"go vet", "golang"}, {"go.mod", "golang"}, {"go code", "golang"},
		{"go service", "golang"}, {"in go", "golang"},
		{"fyr auto", "auto"}, {"fyr memory", "recall"},
		{"fyr gate", "swarm"}, {"fyr land", "swarm"},
		{"cloud run", "cloud-run"}, {"cloud sql", "cloud-sql"},
		{"plain html", "plain-html"}, {"setup wizard", "wizard"},
		{"id token", "oidc"}, {"identity-aware proxy", "iap"},
		{"agent brief", "triage"}, {"enforced by", "requirement"},
		{"risk rubric", "rubric"}, {"drafts jsonl", "jsonl"},
		{"hand off", "handoff"}, {"wrap up for next session", "handoff"},
		{"dead code", "decoupling"}, {"code-excellence", "checklist"},
		{"ship/no-ship", "preflight"}, {"white-space", "editorial"},
	}
	for _, ps := range phraseSignals {
		if strings.Contains(lowPrompt, ps.sub) {
			tf[ps.term]++
		}
	}
	return tf
}

func extractFileConceptWeights(lowPrompt string, files []string) map[string]float64 {
	weights := map[string]float64{}
	bump := func(term string, w float64) {
		if w > weights[term] {
			weights[term] = w
		}
	}
	addCodeFileConceptWeights(lowPrompt, files, bump)
	addInfraAndDocConceptWeights(lowPrompt, files, bump)
	return weights
}

func addCodeFileConceptWeights(lowPrompt string, files []string, bump func(string, float64)) {
	if goCount := countExt(files, ".go"); goCount > 0 {
		bump("golang", 5.5+min(1.5, float64(goCount)*0.3))
	}
	if hasBaseOrExt(files, ".component.ts", ".component.html") || ContainsAnySubstr(lowPrompt, ".component.ts", ".component.html", "@angular/") {
		bump("angular", 8.0)
		bump("slateui", 8.0)
		bump("ng-primitives", 7.0)
	}
	if tsCount := countExt(files, ".ts"); tsCount > 0 && !hasBaseOrExt(files, ".component.ts") {
		bump("typescript", 5.5+min(1.5, float64(tsCount)*0.3))
		bump("tsconfig", 4.5)
	}
	if hasExt(files, ".tsx", ".jsx") {
		bump("react", 6.5)
		bump("typescript", 5.0)
	}
	if shCount := countExt(files, ".sh", ".zsh"); promptHasExt(lowPrompt, ".sh", ".zsh") || (shCount > 0 && shCount*2 > len(files)) {
		bump("bash", 6.5)
		bump("zsh", 5.5)
	}
	if hasExt(files, ".html", ".css", ".svg") && !hasBaseOrExt(files, ".component.html") {
		bump("html", 5.5)
		bump("svg", 5.5)
		bump("css", 5.0)
	}
}

func addInfraAndDocConceptWeights(lowPrompt string, files []string, bump func(string, float64)) {
	if hasExt(files, ".tf", ".tfvars", ".hcl") {
		bump("terraform", 7.5)
		bump("hcl", 7.5)
	}
	if hasExt(files, ".sql") {
		bump("sql", 6.5)
		bump("postgres", 6.5)
		bump("pgx", 5.5)
	}
	if hasBaseOrExt(files, "dockerfile", "docker-compose", "compose.yml", "compose.yaml") {
		bump("docker", 7.0)
		bump("dockerfile", 7.0)
		bump("compose", 6.0)
	}
	if hasBaseOrExt(files, "prd.md") {
		bump("prd", 6.5)
		bump("requirement", 6.5)
	}
	if hasBaseOrExt(files, "readme.md", "sdd.md", "tdd.md") && !ContainsAnyWord(lowPrompt, "typo", "rename") {
		bump("docs", 5.0)
		bump("bluf", 4.5)
	}
}
