package search

import (
	"fmt"
	"strconv"
	"strings"
)

// LedgerNotice renders Surface 2 (PreToolUse [UNCHANGED] / [CLAMPED] / [MODIFIED]
// notices) as a fixed sentinel line followed by clean key-value fields.
type LedgerNotice struct {
	Status         string
	Path           string
	StartLine      int
	EndLine        int
	TotalLines     int
	Sha256         string
	PreviousSha256 string
	CurrentSha256  string
	HitCount       int
	StampConsumed  bool
	Message        string
	Skeleton       []string
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

func renderLedgerNotice(n LedgerNotice) string {
	var sb strings.Builder
	sb.WriteByte('[')
	sb.WriteString(n.Status)
	sb.WriteString("]\n")
	writeKV(&sb, "Path", n.Path)
	if n.StartLine > 0 || n.EndLine > 0 {
		writeKV(&sb, "Range", fmt.Sprintf("%d-%d", n.StartLine, n.EndLine))
	}
	if n.TotalLines > 0 {
		writeKV(&sb, "Lines", strconv.Itoa(n.TotalLines))
	}
	writeKV(&sb, "Sha256", n.Sha256)
	writeKV(&sb, "PreviousSha", n.PreviousSha256)
	writeKV(&sb, "CurrentSha", n.CurrentSha256)
	if n.HitCount > 0 {
		writeKV(&sb, "Hits", strconv.Itoa(n.HitCount))
	}
	if n.StampConsumed {
		writeKV(&sb, "Stamp", "consumed")
	}
	writeKV(&sb, "Note", n.Message)
	if len(n.Skeleton) > 0 {
		sb.WriteString("Skeleton:\n")
		for _, line := range n.Skeleton {
			sb.WriteString("  ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatUnchangedLedgerNotice(relPath string, start, end int, sumHex string, hitCount int, skel string) string {
	return renderLedgerNotice(LedgerNotice{
		Status:    "UNCHANGED",
		Path:      printable(relPath),
		StartLine: start,
		EndLine:   end,
		Sha256:    sumHex[:min(12, len(sumHex))],
		HitCount:  hitCount,
		Message:   "already in context (unchanged sha256; re-read only after content changes or an overlapping edit)",
		Skeleton:  splitSkeletonLines(skel),
	})
}

func formatClampedLedgerNotice(relPath string, totalLines int, sumHex, skel string) string {
	return renderLedgerNotice(LedgerNotice{
		Status:     "CLAMPED",
		Path:       printable(relPath),
		TotalLines: totalLines,
		Sha256:     sumHex[:min(12, len(sumHex))],
		Message:    "unranged read of large file clamped; inspect skeleton and re-call read_file with StartLine and EndLine",
		Skeleton:   splitSkeletonLines(skel),
	})
}

func formatModifiedLedgerNotice(relPath, prevSHA, curSHA, skel string) string {
	return renderLedgerNotice(LedgerNotice{
		Status:         "MODIFIED",
		Path:           printable(relPath),
		PreviousSha256: prevSHA[:min(12, len(prevSHA))],
		CurrentSha256:  curSHA[:min(12, len(curSHA))],
		Message:        "changed on disk since last read; re-read before editing",
		Skeleton:       splitSkeletonLines(skel),
	})
}
