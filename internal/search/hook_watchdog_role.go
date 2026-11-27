package search

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/harness"
)

var watchdogPromptRoles = [][2]string{
	{"you are a senior code reviewer", "reviewer"}, {"you are the security reviewer", "security"},
	{"you run runtime e2e", "verifier"}, {"you are a verifier", "verifier"}, {"you are the verifier", "verifier"},
	{"you are the recon unit", "explorer"}, {"you are an explorer", "explorer"}, {"you are the explorer", "explorer"},
	{"you read what the project has learned", "dreamer"}, {"you are a dreamer", "dreamer"}, {"you are the dreamer", "dreamer"},
	{"you maintain explanatory documentation", "documentation"}, {"you are the documentation", "documentation"}, {"you are a documentation", "documentation"},
}

func resolveWatchdogRoleCtx(ctx context.Context, home, convID, lastInput string) (string, string) {
	role, name, found := lookupWatchdogSummaryRoleCtx(ctx, home, convID)
	if found {
		if role != "main" && role != "self" {
			return role, name
		}
		if role == "main" {
			return "main", ""
		}
		if r := matchWatchdogPromptPreamble(lastInput); r != "" {
			return r, r
		}
		return "self", "self"
	}
	if r := matchWatchdogPromptPreamble(lastInput); r != "" {
		return r, r
	}
	return "main", ""
}

func matchWatchdogPromptPreamble(lastInput string) string {
	stripped := strings.TrimSpace(StripSandboxPreamble(lastInput))
	if stripped == "" {
		return ""
	}
	lines := strings.Split(strings.ToLower(strings.ReplaceAll(stripped, "\r\n", "\n")), "\n")
	checked := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		for _, p := range watchdogPromptRoles {
			if strings.HasPrefix(t, p[0]) {
				return p[1]
			}
		}
		checked++
		if checked >= 2 {
			break
		}
	}
	return ""
}

func lookupWatchdogSummaryRoleCtx(ctx context.Context, home, convID string) (string, string, bool) {
	h := strings.TrimSpace(home)
	agyDir, summaryPath, ok := checkWatchdogSummaryFiles(h)
	if !ok || ctx.Err() != nil {
		return "main", "", false
	}
	if role, name, found, handled := queryWatchdogSummaryRO(ctx, summaryPath, convID); handled {
		return role, name, found
	}
	if ctx.Err() != nil {
		return "main", "", false
	}
	return lookupWatchdogSummaryRoleCopy(ctx, h, agyDir, convID)
}

func checkWatchdogSummaryFiles(home string) (string, string, bool) {
	if home == "" {
		return "", "", false
	}
	gemDir := filepath.Join(home, ".gemini")
	agyDir := filepath.Join(gemDir, "antigravity-cli")
	if !isRegularDirNoSymlink(gemDir) || !isRegularDirNoSymlink(agyDir) {
		return "", "", false
	}
	summaryPath := filepath.Join(agyDir, "conversation_summaries.db")
	fi, err := os.Lstat(summaryPath)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return "", "", false
	}
	for _, suf := range []string{"-wal", "-shm", "-journal"} {
		sfi, sErr := os.Lstat(summaryPath + suf)
		if errors.Is(sErr, os.ErrNotExist) {
			continue
		}
		if sErr != nil || sfi.Mode()&os.ModeSymlink != 0 || !sfi.Mode().IsRegular() {
			return "", "", false
		}
	}
	return agyDir, summaryPath, true
}

func queryWatchdogSummaryRO(ctx context.Context, summaryPath, convID string) (string, string, bool, bool) {
	db, err := sql.Open("sqlite", roDSN(summaryPath))
	if err != nil {
		return "", "", false, false
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	role, name, found, qErr := queryWatchdogSummaryRow(ctx, db, convID)
	if qErr == nil {
		return role, name, found, true
	}
	if errors.Is(qErr, sql.ErrNoRows) {
		return "main", "", false, true
	}
	return "", "", false, false
}

func lookupWatchdogSummaryRoleCopy(ctx context.Context, home, agyDir, convID string) (string, string, bool) {
	conf := filepath.Join(home, harness.StateDir)
	_, stErr := os.Stat(conf)
	created := errors.Is(stErr, os.ErrNotExist)
	if os.MkdirAll(conf, 0o700) != nil {
		return "main", "", false
	}
	tmp, err := os.MkdirTemp(conf, "watchdog-agy-")
	if err != nil {
		if created {
			_ = os.Remove(conf)
		}
		return "main", "", false
	}
	defer func() {
		_ = os.RemoveAll(tmp)
		if created {
			_ = os.Remove(conf)
		}
	}()
	return queryWatchdogSummaryRoleCtx(ctx, agyDir, tmp, convID)
}

func queryWatchdogSummaryRoleCtx(ctx context.Context, agyDir, tmp, convID string) (string, string, bool) {
	db, err := openCopy(agyDir, "conversation_summaries.db", tmp)
	if err != nil {
		return "main", "", false
	}
	defer db.Close()
	role, name, found, _ := queryWatchdogSummaryRow(ctx, db, convID)
	return role, name, found
}

func queryWatchdogSummaryRow(ctx context.Context, db *sql.DB, convID string) (string, string, bool, error) {
	var agentName, parentID string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(agent_name, ''), COALESCE(parent_conversation_id, '') FROM conversation_summaries WHERE conversation_id = ? LIMIT 1`, convID).Scan(&agentName, &parentID)
	if err != nil {
		return "main", "", false, err
	}
	if a := strings.TrimSpace(agentName); a != "" {
		return a, a, true, nil
	}
	if strings.TrimSpace(parentID) != "" {
		return "self", "self", true, nil
	}
	return "main", "", true, nil
}
