package search

import (
	"strings"
	"unicode"
)

// splitIdentTokens extracts deduplicated lowercase sub-tokens from path
// segments and camelCase, acronym, or snake_case identifiers in heading and
// body so natural-language queries match code identifiers (SD61).
func splitIdentTokens(path, heading, body string) string {
	var out []string
	seen := map[string]bool{}
	add := func(tok string) {
		if len(tok) < 2 || seen[tok] {
			return
		}
		seen[tok] = true
		out = append(out, tok)
	}
	appendPathTokens(path, add)
	appendTextIdentTokens(heading, add)
	appendTextIdentTokens(body, add)
	return strings.Join(out, " ")
}

func appendPathTokens(path string, add func(string)) {
	for _, seg := range strings.FieldsFunc(path, isNotAlnum) {
		for _, part := range splitCamelParts(seg) {
			add(part)
		}
	}
}

func appendTextIdentTokens(text string, add func(string)) {
	for _, run := range strings.FieldsFunc(text, isNotIdentRune) {
		parts := splitIdentParts(run)
		if len(parts) < 2 {
			continue
		}
		for _, part := range parts {
			add(part)
		}
	}
}

// splitQueryIdent splits a pure identifier query token into lowercase parts
// when it has camelCase, acronym, or snake_case boundaries; otherwise nil.
func splitQueryIdent(tok string) []string {
	if tok == "" || strings.IndexFunc(tok, isNotIdentRune) >= 0 {
		return nil
	}
	parts := splitIdentParts(tok)
	if len(parts) < 2 {
		return nil
	}
	return parts
}

func splitIdentParts(ident string) []string {
	var parts []string
	for _, seg := range strings.Split(ident, "_") {
		if seg == "" {
			continue
		}
		parts = append(parts, splitCamelParts(seg)...)
	}
	return parts
}

func splitCamelParts(s string) []string {
	rs := []rune(s)
	if len(rs) == 0 {
		return nil
	}
	var out []string
	start := 0
	for i := 1; i < len(rs); i++ {
		if isCamelBoundary(rs, i) {
			out = append(out, strings.ToLower(string(rs[start:i])))
			start = i
		}
	}
	out = append(out, strings.ToLower(string(rs[start:])))
	return out
}

func isCamelBoundary(rs []rune, i int) bool {
	prev, cur := rs[i-1], rs[i]
	if !unicode.IsUpper(cur) {
		return false
	}
	if unicode.IsLower(prev) || unicode.IsDigit(prev) {
		return true
	}
	return unicode.IsUpper(prev) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
}

func isNotAlnum(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func isNotIdentRune(r rune) bool {
	return r != '_' && isNotAlnum(r)
}
