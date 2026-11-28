package vet

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxMatchedSkills  = 3
	minSkillBM25Score = 3.2
)

var intentPrimarySkill = map[IntentMode]string{
	IntentBug:     "fyr-bug",
	IntentFeature: "fyr-feature",
	IntentReview:  "fyr-review",
	IntentRelease: "fyr-preflight",
}

type skillDoc struct {
	meta        SkillMeta
	nameTF      map[string]int
	descTF      map[string]int
	descExactTF map[string]int
	digestTF    map[string]int
	negTerms    map[string]bool
	loadOnly    map[string]bool
	docLen      float64
	isSwarmLike bool
}

// MatchSkills dynamically scores the installed skill catalog using frontmatter
// BM25F + file-extension signals (Top-3 cap, 0 phantom skills on empty catalog)
// and sets the mandatory security subagent flag on auth/crypto/secret/frontmatter surfaces.
func MatchSkills(prompt string, intent IntentMode, files []string, catalog []SkillMeta) ([]SkillMeta, bool) {
	low := strings.ToLower(prompt)
	secReq := intent != IntentQuestion && touchesSecuritySurface(low, files)
	if intent == IntentApproval || intent == IntentTrivial {
		return nil, false
	}
	installed := normalizeSkillCatalog(catalog)
	if len(installed) == 0 {
		return nil, secReq
	}
	if intent == IntentCorrection {
		return selectInstalledNamedSkills(installed, "operator correction", "fyr-project-memory"), secReq
	}
	return rankMatchingSkills(installed, low, prompt, intent, files), secReq
}

func rankMatchingSkills(installed []SkillMeta, low, prompt string, intent IntentMode, files []string) []SkillMeta {
	docs, idf, avgLen := buildSkillCorpus(installed)
	queryTerms := extractPromptQueryTerms(low)
	fileWeights := extractFileConceptWeights(low, files)
	type scoredSkill struct {
		meta  SkillMeta
		score float64
	}
	var scored []scoredSkill
	for _, doc := range docs {
		if sc, reason := scoreSkillDoc(doc, low, prompt, intent, queryTerms, fileWeights, idf, avgLen); sc > 0 {
			cand := doc.meta
			cand.Digest = truncateSkillDigestUTF8(cand.Digest, MaxSkillDigestBytes)
			cand.Reason = reason
			scored = append(scored, scoredSkill{meta: cand, score: sc})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if math.Abs(scored[i].score-scored[j].score) > 1e-6 {
			return scored[i].score > scored[j].score
		}
		return scored[i].meta.Name < scored[j].meta.Name
	})
	limit := min(maxMatchedSkills, len(scored))
	out := make([]SkillMeta, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, scored[i].meta)
	}
	return out
}

func selectInstalledNamedSkills(installed []SkillMeta, reason string, names ...string) []SkillMeta {
	byName := make(map[string]SkillMeta, len(installed))
	for _, s := range installed {
		byName[s.Name] = s
	}
	var out []SkillMeta
	for _, name := range names {
		if s, ok := byName[name]; ok {
			s.Digest = truncateSkillDigestUTF8(s.Digest, MaxSkillDigestBytes)
			s.Reason = reason
			out = append(out, s)
		}
	}
	return out
}

func buildSkillCorpus(installed []SkillMeta) ([]skillDoc, map[string]float64, float64) {
	installedNames := make(map[string]bool, len(installed))
	for _, s := range installed {
		installedNames[strings.ToLower(s.Name)] = true
	}
	docs := make([]skillDoc, 0, len(installed))
	df := map[string]int{}
	totalLen := 0.0
	for _, s := range installed {
		doc := newSkillDoc(s, installedNames)
		totalLen += doc.docLen
		recordDocFreq(df, doc)
		docs = append(docs, doc)
	}
	effectiveN := float64(max(len(docs), 20))
	idf := make(map[string]float64, len(df))
	for t, freq := range df {
		idf[t] = math.Log(1.0 + (effectiveN-float64(freq)+0.5)/(float64(freq)+0.5))
	}
	avgLen := 1.0
	if len(docs) > 0 && totalLen > 0 {
		avgLen = totalLen / float64(len(docs))
	}
	return docs, idf, avgLen
}

func newSkillDoc(s SkillMeta, installedNames map[string]bool) skillDoc {
	descSource, digestSource := s.Description, s.Digest
	if strings.TrimSpace(descSource) == "" {
		descSource, digestSource = s.Digest, ""
	}
	posDesc, negDesc, loadOnlyCue := splitSkillScope(descSource)
	posDesc = stripOtherSkillSlashRefs(posDesc, s.Name, installedNames)
	doc := skillDoc{
		meta:        s,
		nameTF:      extractSkillTerms(s.Name),
		descTF:      extractSkillTerms(posDesc),
		descExactTF: extractSkillTermsOpts(posDesc, false),
		digestTF:    extractSkillTerms(digestSource),
		negTerms:    map[string]bool{},
		loadOnly:    map[string]bool{},
		isSwarmLike: ContainsAnyWord(strings.ToLower(posDesc), "multi-agent", "swarm"),
	}
	doc.nameTF[strings.ToLower(s.Name)]++
	for t := range extractSkillTerms(negDesc) {
		if doc.nameTF[t] == 0 && doc.descTF[t] == 0 {
			doc.negTerms[t] = true
		}
	}
	for t := range extractSkillTerms(loadOnlyCue) {
		doc.loadOnly[t] = true
	}
	doc.docLen = weightedDocLen(doc)
	return doc
}

func recordDocFreq(df map[string]int, doc skillDoc) {
	seen := map[string]bool{}
	for _, m := range []map[string]int{doc.nameTF, doc.descTF, doc.digestTF} {
		for t := range m {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
}

func weightedDocLen(doc skillDoc) float64 {
	sum := 0.0
	for _, c := range doc.nameTF {
		sum += 3.0 * float64(c)
	}
	for _, c := range doc.descTF {
		sum += 2.0 * float64(c)
	}
	for _, c := range doc.digestTF {
		sum += 0.5 * float64(c)
	}
	if sum < 1.0 {
		return 1.0
	}
	return sum
}

func scoreSkillDoc(
	doc skillDoc,
	lowPrompt, origPrompt string,
	intent IntentMode,
	queryTerms map[string]int,
	fileWeights map[string]float64,
	idf map[string]float64,
	avgLen float64,
) (float64, string) {
	name := doc.meta.Name
	directName := isDirectSkillMention(name, lowPrompt)
	if !skillDocEligible(doc, lowPrompt, origPrompt, directName, queryTerms) {
		return 0, ""
	}
	bm25, nameDescHits, digestHits := computeSkillBM25(doc, queryTerms, idf, avgLen)
	fileScore := computeFileConceptScore(doc, fileWeights)
	total, reason := baseSkillScore(bm25, fileScore, directName)
	isPrimary := intentPrimarySkill[intent] == name
	if !directName && !isPrimary {
		var ok bool
		if total, ok = applyNegativeSkillPenalty(doc, total, nameDescHits, fileScore, queryTerms, fileWeights); !ok {
			return 0, ""
		}
	}
	if isPrimary {
		return max(total, 100.0), "intent=" + string(intent)
	}
	if !passesSkillScoreThreshold(intent, directName, fileScore, total, nameDescHits, digestHits) {
		return 0, ""
	}
	return total, pickSkillMatchReason(reason, fileScore, bm25)
}

func isDirectSkillMention(name, lowPrompt string) bool {
	lowName := strings.ToLower(name)
	return ContainsAnyWord(lowPrompt, lowName) || strings.Contains(lowPrompt, "/"+lowName)
}

func skillDocEligible(doc skillDoc, lowPrompt, origPrompt string, directName bool, queryTerms map[string]int) bool {
	if isDiagnosticOrNegatedSkillMention(doc.meta.Name, lowPrompt) {
		return false
	}
	if len(doc.loadOnly) > 0 && !directName && !matchesLoadOnlyCue(doc, queryTerms) {
		return false
	}
	return !doc.isSwarmLike || !IsWorkerBriefPrompt(origPrompt) || directName
}

func baseSkillScore(bm25, fileScore float64, directName bool) (float64, string) {
	total := bm25 + fileScore
	if directName {
		return total + 12.0, "named in prompt"
	}
	return total, ""
}

func applyNegativeSkillPenalty(doc skillDoc, total float64, nameDescHits int, fileScore float64, queryTerms map[string]int, fileWeights map[string]float64) (float64, bool) {
	negHits := countNegativeScopeHits(doc, queryTerms, fileWeights)
	if negHits == 0 {
		return total, true
	}
	if nameDescHits < 2 && fileScore < 7.0 {
		return 0, false
	}
	return total - 8.0*float64(negHits), true
}

func pickSkillMatchReason(reason string, fileScore, bm25 float64) string {
	if reason != "" {
		return reason
	}
	if fileScore > bm25 {
		return "file surface match"
	}
	return "catalog BM25 match"
}

func passesSkillScoreThreshold(intent IntentMode, directName bool, fileScore, total float64, nameDescHits, digestHits int) bool {
	if directName {
		return true
	}
	if intent == IntentQuestion && fileScore < 4.0 && nameDescHits < 2 {
		return false
	}
	return (fileScore >= 4.0 && total >= 3.0) || (total >= minSkillBM25Score && (nameDescHits >= 1 || digestHits >= 2))
}

func matchesLoadOnlyCue(doc skillDoc, queryTerms map[string]int) bool {
	for t := range doc.loadOnly {
		if queryTerms[t] > 0 {
			return true
		}
	}
	return queryTerms[strings.ToLower(doc.meta.Name)] > 0
}

func computeSkillBM25(doc skillDoc, queryTerms map[string]int, idf map[string]float64, avgLen float64) (float64, int, int) {
	const k1, b = 1.2, 0.5
	lenNorm := 1.0 - b + b*(doc.docLen/avgLen)
	score, nameDescHits, digestHits := 0.0, 0, 0
	for qt := range queryTerms {
		nTF, dTF, gTF := doc.nameTF[qt], doc.descTF[qt], doc.digestTF[qt]
		tf := 3.0*float64(nTF) + 2.0*float64(dTF) + 0.5*float64(gTF)
		if tf <= 0 {
			continue
		}
		if nTF > 0 || dTF > 0 {
			nameDescHits++
		} else {
			digestHits++
		}
		score += idf[qt] * (tf * (k1 + 1.0)) / (tf + k1*lenNorm)
	}
	return score, nameDescHits, digestHits
}

func computeFileConceptScore(doc skillDoc, fileWeights map[string]float64) float64 {
	best := 0.0
	for concept, w := range fileWeights {
		score := 0.0
		if doc.nameTF[concept] > 0 {
			score = w
		} else if doc.descExactTF[concept] > 0 {
			score = w * 0.9
		}
		if score > best {
			best = score
		}
	}
	return best
}

func countNegativeScopeHits(doc skillDoc, queryTerms map[string]int, fileWeights map[string]float64) int {
	hits := 0
	for nt := range doc.negTerms {
		if queryTerms[nt] > 0 || fileWeights[nt] > 0 {
			hits++
		}
	}
	return hits
}

func isDiagnosticOrNegatedSkillMention(name, lowPrompt string) bool {
	if !ContainsAnyWord(lowPrompt, name) {
		return false
	}
	for _, phrase := range []string{
		"loading the " + name + " skill", "loads the " + name + " skill", "loading " + name + " skill",
		"triggering the " + name + " skill", "triggers the " + name + " skill", "over-triggers " + name,
		name + " skill very often", "don't use " + name, "do not use " + name, "never use " + name,
		"without " + name, "stop loading " + name, "instead of " + name,
	} {
		if strings.Contains(lowPrompt, phrase) {
			return true
		}
	}
	return strings.Contains(lowPrompt, "why ") && strings.Contains(lowPrompt, name+" skill")
}

func touchesSecuritySurface(lowPrompt string, files []string) bool {
	if ContainsAnySubstr(
		lowPrompt,
		"auth", "authn", "authz", "oauth", "oidc", "jwt", "iap",
		"auth token", "api token", "id token", "bearer"+" token", "oauth token",
		"password", "crypto", "cipher", "hmac", "sha256", "tls", "secret",
		"redact", "credential", "iam", "permission", "frontmatter", "allowlist",
		"allow-list", "skillkeys", "agentkeys", "knowntools", "inject", "injection", "sanitize",
	) {
		return true
	}
	for _, f := range files {
		if ContainsAnySubstr(strings.ToLower(f), "redact", "security", "auth", "guard", "inventory.go", "go.mod") {
			return true
		}
	}
	return false
}

func truncateSkillDigestUTF8(s string, maxB int) string {
	if maxB <= 0 || len(s) <= maxB {
		return s
	}
	cut := 0
	for i := range s {
		if i > maxB {
			break
		}
		cut = i
	}
	if cut == 0 && maxB > 0 {
		for cut < len(s) && cut < maxB {
			_, sz := utf8.DecodeRuneInString(s[cut:])
			if cut+sz > maxB {
				break
			}
			cut += sz
		}
	}
	return strings.TrimRight(s[:cut], " \t\n\r")
}
