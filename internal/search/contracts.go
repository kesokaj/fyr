package search

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultContractsBudget     = 1500
	defaultContractDigestBytes = 600
	faultsTSVThreshold         = 15
	contractSpecCapPct         = 60
	contractSigFloorPct        = 35
	contractGuardCapPct        = 25
	codeSignaturesHeader       = "\n[CODE_SIGNATURES]\n"
)

// SpecADRRef carries one linked accepted ADR entry for contract guardrails.
type SpecADRRef struct {
	ID     string
	Status string
	Title  string
}

// SpecContractSource carries one current SPEC-* record (and optionally linked
// accepted ADR-* records) passed into internal/search via Options.BrainSpecBodies
// so internal/search never imports internal/brain.
type SpecContractSource struct {
	ID     string
	Kind   string // "spec" (default when empty) or "adr"
	Status string
	Title  string
	Body   string
	ADRs   []SpecADRRef
}

func (sc *scope) buildContractsProjection(o Options, out io.Writer) error {
	if o.Budget <= 0 {
		o.Budget = defaultContractsBudget
	}
	syms, lookupPaths, err := sc.resolveContractsTarget(o.Contracts)
	if err != nil {
		return err
	}
	var sources []SpecContractSource
	if o.BrainSpecBodies != nil {
		sources = o.BrainSpecBodies(o.Dir, o.Home, lookupPaths)
	}
	specs, adrs := partitionContractSources(sources)
	adrs = mergeBrainRefADRs(adrs, lookupPaths, o.BrainRefs)
	if len(syms) == 0 && len(specs) == 0 {
		return fmt.Errorf("search: no indexed symbols or linked specs for %s", printable(o.Contracts))
	}
	doc := renderContractsDocument(o.Contracts, o.Budget, syms, specs, adrs)
	maxBytes := max(200, o.Budget*4)
	if len(doc) > maxBytes {
		doc = truncateMultilineUTF8(doc, maxBytes-1) + "\n"
	}
	_, err = io.WriteString(out, doc)
	return err
}

func (sc *scope) resolveContractsTarget(target string) ([]RankedSymbol, []string, error) {
	allSyms, err := querySymbolNodesDB(sc.st.db, sc.projects, false)
	if err != nil {
		return nil, nil, err
	}
	raw := strings.TrimSpace(target)
	norm := sc.normalizePath(raw)
	dirPrefix := strings.TrimSuffix(norm, "/")
	exported := make([]RankedSymbol, 0, len(allSyms))
	for _, s := range allSyms {
		if isExportedContractSymbol(s) {
			exported = append(exported, s)
		}
	}
	matched := matchContractSymbols(exported, raw, norm, dirPrefix)
	sortContractSymbols(matched)
	paths := collectContractLookupPaths(matched, raw, norm, dirPrefix)
	return matched, paths, nil
}

func isExportedContractSymbol(s RankedSymbol) bool {
	if !s.Exported || !isContractCodePath(s.Path) {
		return false
	}
	if strings.EqualFold(path.Ext(s.Path), ".go") && s.Recv != "" && isUnexportedIdent(s.Recv) {
		return false
	}
	return true
}

func isContractCodePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs":
		return true
	}
	return false
}

func isContractTestPath(p string) bool {
	base := strings.ToLower(path.Base(p))
	for _, suf := range []string{
		"_test.go",
		".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx",
		".test.js", ".test.jsx", ".test.mjs", ".spec.js", ".spec.jsx", ".spec.mjs",
	} {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}

func isUnexportedIdent(name string) bool {
	name = strings.TrimLeft(strings.TrimSpace(name), "*")
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return !unicode.IsUpper(r)
}

func matchContractSymbols(exported []RankedSymbol, raw, norm, dirPrefix string) []RankedSymbol {
	if norm != "" && !strings.HasSuffix(norm, "/") {
		if exact := filterSymsByPath(exported, norm, false); len(exact) > 0 {
			return exact
		}
	}
	prod := filterNonTestSyms(exported)
	if dirPrefix != "" {
		if dirSyms := filterSymsByPath(prod, dirPrefix+"/", true); len(dirSyms) > 0 {
			return dirSyms
		}
	}
	return matchSymsByName(prod, raw)
}

func filterNonTestSyms(syms []RankedSymbol) []RankedSymbol {
	out := make([]RankedSymbol, 0, len(syms))
	for _, s := range syms {
		if !isContractTestPath(s.Path) {
			out = append(out, s)
		}
	}
	return out
}

func filterSymsByPath(syms []RankedSymbol, target string, prefix bool) []RankedSymbol {
	var out []RankedSymbol
	for _, s := range syms {
		if (!prefix && s.Path == target) || (prefix && strings.HasPrefix(s.Path, target)) {
			out = append(out, s)
		}
	}
	return out
}

func matchSymsByName(syms []RankedSymbol, raw string) []RankedSymbol {
	if raw == "" {
		return nil
	}
	exactTypes := map[string]bool{}
	var exact []RankedSymbol
	for _, s := range syms {
		if s.Name == raw || s.QualName() == raw {
			exact = append(exact, s)
			if s.Recv == "" && (s.Kind == "struct" || s.Kind == "interface" || s.Kind == "type" || s.Kind == "class") {
				exactTypes[s.Name] = true
			}
		}
	}
	if len(exact) > 0 {
		return appendRecvMethods(syms, exact, exactTypes)
	}
	var ci []RankedSymbol
	ciTypes := map[string]bool{}
	for _, s := range syms {
		if strings.EqualFold(s.Name, raw) || strings.EqualFold(s.QualName(), raw) {
			ci = append(ci, s)
			if s.Recv == "" && (s.Kind == "struct" || s.Kind == "interface" || s.Kind == "type" || s.Kind == "class") {
				ciTypes[s.Name] = true
			}
		}
	}
	return appendRecvMethods(syms, ci, ciTypes)
}

func appendRecvMethods(all, base []RankedSymbol, types map[string]bool) []RankedSymbol {
	if len(types) == 0 {
		return base
	}
	seen := make(map[int]bool, len(base))
	for _, s := range base {
		seen[s.Idx] = true
	}
	for _, s := range all {
		if !seen[s.Idx] && s.Recv != "" && types[s.Recv] {
			seen[s.Idx] = true
			base = append(base, s)
		}
	}
	return base
}

func sortContractSymbols(syms []RankedSymbol) {
	sort.Slice(syms, func(i, j int) bool {
		a, b := syms[i], syms[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.StartLine != b.StartLine {
			return a.StartLine < b.StartLine
		}
		if a.EndLine != b.EndLine {
			return a.EndLine < b.EndLine
		}
		if a.Recv != b.Recv {
			return a.Recv < b.Recv
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Sig < b.Sig
	})
}

func collectContractLookupPaths(syms []RankedSymbol, raw, norm, dirPrefix string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, s := range syms {
		add(s.Path)
	}
	add(norm)
	if dirPrefix != "" && dirPrefix != norm {
		add(dirPrefix)
	}
	add(raw)
	sort.Strings(out)
	return out
}

func partitionContractSources(sources []SpecContractSource) ([]SpecContractSource, []SpecADRRef) {
	specByID := map[string]SpecContractSource{}
	adrByID := map[string]SpecADRRef{}
	addADR := func(ref SpecADRRef) {
		id := strings.TrimSpace(ref.ID)
		if id == "" || (ref.Status != "" && !strings.EqualFold(ref.Status, "accepted")) {
			return
		}
		if _, exists := adrByID[id]; !exists {
			adrByID[id] = SpecADRRef{ID: id, Status: ref.Status, Title: ref.Title}
		}
	}
	for _, s := range sources {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			continue
		}
		isADR := strings.EqualFold(s.Kind, "adr") || strings.HasPrefix(strings.ToUpper(id), "ADR-")
		if isADR {
			addADR(SpecADRRef{ID: id, Status: s.Status, Title: s.Title})
			continue
		}
		if s.Status != "" && !strings.EqualFold(s.Status, "current") {
			continue
		}
		if _, exists := specByID[id]; !exists {
			specByID[id] = s
		}
		for _, a := range s.ADRs {
			addADR(a)
		}
	}
	specs := make([]SpecContractSource, 0, len(specByID))
	for _, s := range specByID {
		specs = append(specs, s)
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	adrs := make([]SpecADRRef, 0, len(adrByID))
	for _, a := range adrByID {
		adrs = append(adrs, a)
	}
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].ID < adrs[j].ID })
	return specs, adrs
}

func mergeBrainRefADRs(existing []SpecADRRef, paths []string, brainRefs func(string) []LinkedRecord) []SpecADRRef {
	if brainRefs == nil || len(paths) == 0 {
		return existing
	}
	byID := make(map[string]SpecADRRef, len(existing))
	for _, a := range existing {
		byID[a.ID] = a
	}
	for _, p := range paths {
		for _, lr := range brainRefs(p) {
			if !strings.HasPrefix(strings.ToUpper(lr.ID), "ADR-") {
				continue
			}
			if lr.Status != "" && !strings.EqualFold(lr.Status, "accepted") {
				continue
			}
			if _, ok := byID[lr.ID]; !ok {
				byID[lr.ID] = SpecADRRef{ID: lr.ID, Status: lr.Status, Title: lr.Title}
			}
		}
	}
	out := make([]SpecADRRef, 0, len(byID))
	for _, a := range byID {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func renderContractsDocument(target string, budget int, syms []RankedSymbol, specs []SpecContractSource, adrs []SpecADRRef) string {
	header := fmt.Sprintf("# contracts: %s (budget: %d tokens)\n", printable(target), budget)
	maxBytes := max(200, budget*4)
	remaining := maxBytes - len(header)
	if remaining <= 0 {
		return header
	}
	specTail := renderSpecADRTail(specs, adrs)
	if len(syms) == 0 {
		return header + clipSpecTail(specs, adrs, specTail, remaining)
	}
	if specTail == "" {
		return header + renderCodeSignatures(syms, remaining)
	}
	return header + renderBalancedSections(syms, specs, adrs, specTail, maxBytes, remaining)
}

func renderBalancedSections(syms []RankedSymbol, specs []SpecContractSource, adrs []SpecADRRef, specTail string, maxBytes, remaining int) string {
	lines, fullSigBytes := formatContractSigLines(syms)
	if fullSigBytes+len(specTail) <= remaining {
		return joinContractSigLines(lines, fullSigBytes) + specTail
	}
	minSigBytes := min(remaining, maxBytes*contractSigFloorPct/100)
	maxSpecBytes := min(remaining*contractSpecCapPct/100, remaining-minSigBytes)
	if maxSpecBytes < 0 {
		maxSpecBytes = 0
	}
	specLimit := maxSpecBytes
	if fullSigBytes < remaining-specLimit {
		specLimit = remaining - fullSigBytes
	}
	cappedTail := clipSpecTail(specs, adrs, specTail, specLimit)
	sigBudget := remaining - len(cappedTail)
	return renderContractSigLines(lines, fullSigBytes, sigBudget) + cappedTail
}

func renderCodeSignatures(syms []RankedSymbol, maxBytes int) string {
	lines, fullBytes := formatContractSigLines(syms)
	return renderContractSigLines(lines, fullBytes, maxBytes)
}

func formatContractSigLines(syms []RankedSymbol) ([]string, int) {
	lines := make([]string, len(syms))
	total := len(codeSignaturesHeader)
	for i, s := range syms {
		sig := strings.TrimSpace(s.Sig)
		if sig == "" {
			sig = s.Kind + " " + s.QualName()
		}
		lines[i] = fmt.Sprintf("%s:%d-%d  %s\n", printable(s.Path), s.StartLine, s.EndLine, sanitizeCodeLine(sig))
		total += len(lines[i])
	}
	return lines, total
}

func joinContractSigLines(lines []string, total int) string {
	var b strings.Builder
	b.Grow(total)
	b.WriteString(codeSignaturesHeader)
	for _, line := range lines {
		b.WriteString(line)
	}
	return b.String()
}

func renderContractSigLines(lines []string, fullBytes, maxBytes int) string {
	if len(lines) == 0 || maxBytes <= len(codeSignaturesHeader) {
		return ""
	}
	if fullBytes <= maxBytes {
		return joinContractSigLines(lines, fullBytes)
	}
	var b strings.Builder
	b.WriteString(codeSignaturesHeader)
	kept := 0
	for i, line := range lines {
		omittedAfter := len(lines) - (i + 1)
		footerLen := 0
		if omittedAfter > 0 {
			footerLen = len(fmt.Sprintf("... (+%d more signatures omitted)\n", omittedAfter))
		}
		if b.Len()+len(line)+footerLen > maxBytes {
			break
		}
		b.WriteString(line)
		kept++
	}
	if omitted := len(lines) - kept; omitted > 0 {
		footer := fmt.Sprintf("... (+%d more signatures omitted)\n", omitted)
		if b.Len()+len(footer) <= maxBytes {
			b.WriteString(footer)
		} else if kept == 0 {
			return ""
		}
	}
	return b.String()
}

func renderSpecADRTail(specs []SpecContractSource, adrs []SpecADRRef) string {
	return renderSpecsBody(specs) + renderGuardrailsSection(adrs)
}

func renderSpecsBody(specs []SpecContractSource) string {
	var b strings.Builder
	for _, sp := range specs {
		writeSpecProjection(&b, sp)
	}
	return b.String()
}

func renderGuardrailsSection(adrs []SpecADRRef) string {
	if len(adrs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n[GUARDRAILS]\n")
	for _, a := range adrs {
		fmt.Fprintf(&b, "- %s: %s\n", printable(a.ID), printable(strings.Join(strings.Fields(a.Title), " ")))
	}
	return b.String()
}

func clipSpecTail(specs []SpecContractSource, adrs []SpecADRRef, specTail string, limit int) string {
	if limit <= 0 || specTail == "" {
		return ""
	}
	if len(specTail) <= limit {
		return specTail
	}
	guard := renderGuardrailsSection(adrs)
	specBody := renderSpecsBody(specs)
	if guard == "" || specBody == "" {
		return clipUTF8Block(specTail, limit)
	}
	guardBudget := min(len(guard), max(limit*contractGuardCapPct/100, limit-len(specBody)))
	clippedGuard := clipLinesUTF8(guard, guardBudget)
	if !strings.Contains(clippedGuard, "\n- ") {
		return clipUTF8Block(specTail, limit)
	}
	return clipUTF8Block(specBody, limit-len(clippedGuard)) + clippedGuard
}

func clipLinesUTF8(s string, maxB int) string {
	if maxB <= 0 || s == "" {
		return ""
	}
	if len(s) <= maxB {
		return s
	}
	var b strings.Builder
	for _, line := range strings.SplitAfter(s, "\n") {
		if line == "" {
			continue
		}
		if b.Len()+len(line) > maxB {
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

func clipUTF8Block(s string, maxB int) string {
	if maxB <= 0 || s == "" {
		return ""
	}
	if len(s) <= maxB {
		return s
	}
	if maxB == 1 {
		return "\n"
	}
	return truncateMultilineUTF8(s, maxB-1) + "\n"
}

func writeSpecProjection(b *strings.Builder, sp SpecContractSource) {
	id := printable(sp.ID)
	sec := parseSpecSections(sp.Body)
	if len(sec.interfaces) > 0 {
		fmt.Fprintf(b, "\n[SPEC_CONTRACTS] (%s)\n", id)
		b.WriteString(strings.Join(sec.interfaces, "\n\n") + "\n")
	}
	if len(sec.invariants) > 0 {
		fmt.Fprintf(b, "\n[INVARIANTS] (%s)\n", id)
		b.WriteString(strings.Join(sec.invariants, "\n") + "\n")
	}
	if len(sec.faults) > 0 {
		fmt.Fprintf(b, "\n[FAULTS] (%s)\n", id)
		parts := make([]string, len(sec.faults))
		for i, t := range sec.faults {
			parts[i] = formatFaultTable(t)
		}
		b.WriteString(strings.Join(parts, "\n\n") + "\n")
	}
}
