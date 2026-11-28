package search

import "github.com/kesokaj/fyr/internal/readledger"

// BashFileRead describes a single-file read extracted from a shell command string.
type BashFileRead = readledger.BashFileRead

// ParseBashFileReads extracts file reads from &&, ;, and newline shell chains
// (including benign env prefixes and strictly read-only pipes), tracking cd prefixes.
func ParseBashFileReads(cmd string) []BashFileRead {
	return readledger.ParseBashFileReads(cmd)
}

func isEnvAssignToken(tok string) bool {
	return readledger.IsEnvAssignToken(tok)
}

func scanUnquotedShellChars(s string, onUnquoted func(i int, ch byte) bool) bool {
	return readledger.ScanUnquotedShellChars(s, onUnquoted)
}
