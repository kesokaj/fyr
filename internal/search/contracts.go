package search

import (
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const (
	defaultContractsBudget     = 1500
	defaultContractDigestBytes = 600
	faultsTSVThreshold         = 15
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
	if strings.EqualFold(path.Ext(s.Path), ".go") && s.Kind == "interface_method" && isUnexportedIdent(s.Recv) {
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

func isUnexportedIdent(name string) bool {
	return name != "" && name[0] >= 'a' && name[0] <= 'z'
}

func matchContractSymbols(exported []RankedSymbol, raw, norm, dirPrefix string) []RankedSymbol {
	if norm != "" && !strings.HasSuffix(norm, "/") {
		if exact := filterSymsByPath(exported, norm, false); len(exact) > 0 {
			return exact
		}
	}
	if dirPrefix != "" {
		if dirSyms := filterSymsByPath(exported, dirPrefix+"/", true); len(dirSyms) > 0 {
			return dirSyms
		}
	}
	return matchSymsByName(exported, raw)
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
	var b strings.Builder
	fmt.Fprintf(&b, "# contracts: %s (budget: %d tokens)\n", printable(target), budget)
	if len(syms) > 0 {
		b.WriteString("\n[CODE_SIGNATURES]\n")
		for _, s := range syms {
			sig := strings.TrimSpace(s.Sig)
			if sig == "" {
				sig = s.Kind + " " + s.QualName()
			}
			fmt.Fprintf(&b, "%s:%d-%d  %s\n", printable(s.Path), s.StartLine, s.EndLine, sanitizeCodeLine(sig))
		}
	}
	for _, sp := range specs {
		writeSpecProjection(&b, sp)
	}
	if len(adrs) > 0 {
		b.WriteString("\n[GUARDRAILS]\n")
		for _, a := range adrs {
			fmt.Fprintf(&b, "- %s: %s\n", printable(a.ID), printable(strings.Join(strings.Fields(a.Title), " ")))
		}
	}
	return b.String()
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
