package search

import "strings"

// LedgerNotice renders Surface 2 (PreToolUse [UNCHANGED] / [MODIFIED] notices)
// as a fixed sentinel line followed by 2-space pretty-printed lowerCamelCase JSON.
type LedgerNotice struct {
	Status         string   `json:"status"`
	Path           string   `json:"path"`
	StartLine      int      `json:"startLine,omitempty"`
	EndLine        int      `json:"endLine,omitempty"`
	TotalLines     int      `json:"totalLines,omitempty"`
	Sha256         string   `json:"sha256,omitempty"`
	PreviousSha256 string   `json:"previousSha256,omitempty"`
	CurrentSha256  string   `json:"currentSha256,omitempty"`
	HitCount       int      `json:"hitCount,omitempty"`
	StampConsumed  bool     `json:"stampConsumed,omitempty"`
	Message        string   `json:"message"`
	Skeleton       []string `json:"skeleton,omitempty"`
}

func splitSkeletonLines(skel string) []string {
	var out []string
	for _, line := range strings.Split(skel, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			out = append(out, printable(line))
		}
	}
	return out
}

func formatUnchangedLedgerNotice(relPath string, start, end int, sumHex string, hitCount int, skel string) string {
	n := LedgerNotice{
		Status:    "UNCHANGED",
		Path:      printable(relPath),
		StartLine: start,
		EndLine:   end,
		Sha256:    sumHex[:min(12, len(sumHex))],
		HitCount:  hitCount,
		Message:   "already in context (unchanged sha256; re-read only after content changes or an overlapping edit)",
		Skeleton:  splitSkeletonLines(skel),
	}
	return "[UNCHANGED]\n" + marshalPrettyJSON(n)
}

func formatClampedLedgerNotice(relPath string, totalLines int, sumHex, skel string) string {
	n := LedgerNotice{
		Status:     "CLAMPED",
		Path:       printable(relPath),
		TotalLines: totalLines,
		Sha256:     sumHex[:min(12, len(sumHex))],
		Message:    "unranged read of large file clamped; inspect skeleton and re-call read_file with StartLine and EndLine",
		Skeleton:   splitSkeletonLines(skel),
	}
	return "[CLAMPED]\n" + marshalPrettyJSON(n)
}

func formatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel string) string {
	n := LedgerNotice{
		Status:         "MODIFIED",
		Path:           printable(relPath),
		PreviousSha256: prevSHA[:min(12, len(prevSHA))],
		CurrentSha256:  curSHA[:min(12, len(curSHA))],
		Message:        "changed on disk since last read; re-read before editing",
		Skeleton:       splitSkeletonLines(skel),
	}
	return "[MODIFIED]\n" + marshalPrettyJSON(n)
}
