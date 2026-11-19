package search

// The two reads `fyr dream` makes with the SQLite driver, kept in this
// package because it imports the driver (AGENTS.md; R4's
// dream clause, ADR-047a): the global index's project list, and Antigravity's
// conversation databases, both read-only.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kesokaj/fyr/internal/harness"
)

// GlobalWorkspace is the synthetic root for Antigravity conversations that have
// an empty workspace_uris list (ADR-524).
const GlobalWorkspace = "__global__"

// Project is one checkout the global search index knows.
type Project struct {
	Root, Name string
}

// AgyConversation is the text of one Antigravity conversation.
type AgyConversation struct {
	ID       string
	Modified time.Time
	Text     string
}

// ListProjects returns the checkouts in the global search index, in the order
// they were first registered, raw: a row may predate ADR-068 or have lost its
// marker, so each consumer (the dream, import --global) checks
// trail.Registered itself (audit-choices D15). No index is no projects: the
// file is never created here.
func ListProjects(home string) ([]Project, error) {
	path := globalDB(home)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	// A copy, not the file: a read-only open of a WAL database still creates
	// its -wal and -shm files, and a dry run must leave the directory as it was.
	tmp, err := os.MkdirTemp(filepath.Dir(path), "dream-projects-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	db, err := openCopy(filepath.Dir(path), filepath.Base(path), tmp)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT root, name FROM projects ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("reading the projects of %s: %w", path, err)
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Root, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func withAgyTemp(home, prefix string, fn func(src, tmp string) error) error {
	src := filepath.Join(home, ".gemini", "antigravity-cli")
	if _, err := os.Stat(filepath.Join(src, "conversation_summaries.db")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	conf := filepath.Join(home, harness.StateDir)
	_, statErr := os.Stat(conf)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(conf, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(conf, prefix)
	if err != nil {
		if created {
			removeIfEmpty(conf)
		}
		return err
	}
	// The copies go first, then the directory, and only when this call made
	// it: one deferred function, so the order cannot be inverted.
	defer func() {
		os.RemoveAll(tmp)
		if created {
			removeIfEmpty(conf)
		}
	}()
	return fn(src, tmp)
}

// agyTimeLayout is conversation_summaries.last_modified_time as the D8 probe
// found it.
const agyTimeLayout = "2006-01-02 15:04:05.999999999-07:00"

// AgyConversationText returns the text of every Antigravity conversation
// that is root's own (workspaces.judge: every workspace under root, in root's
// checkout, not a nested one, and not the dream's own in
// harness.DreamWork(home)) and that was modified after since, and names
// each conversation under root that was left out ("<id> (<why>)"). The
// databases are copied first and the copies opened, never the live files
// Antigravity may be writing (D24, D27); the copies are removed after.
func AgyConversationText(home, root string, since time.Time) ([]AgyConversation, []string, error) {
	return AgyConversationTextInWindow(home, root, since, time.Time{})
}

// AgyConversationTextInWindow behaves like AgyConversationText, restricting
// conversations to (since, maxModified] when maxModified is non-zero.
func AgyConversationTextInWindow(home, root string, since, maxModified time.Time) ([]AgyConversation, []string, error) {
	var out []AgyConversation
	var leftOut []string
	err := withAgyTemp(home, "dream-agy-", func(src, tmp string) error {
		ws := newWorkspaces(root, harness.DreamWorkPaths(home))
		ws.sandboxes = sandboxExcludePaths(home)
		var err error
		out, leftOut, err = listConversations(src, tmp, ws, since, maxModified)
		if err != nil {
			return err
		}
		for i := range out {
			text, err := conversationText(src, out[i].ID, tmp)
			if err != nil {
				return err
			}
			out[i].Text = text
		}
		return nil
	})
	return out, leftOut, err
}

// listConversations reads the conversation list from a copy in tmp: the
// conversations ws keeps modified in (since, maxModified], without their text,
// and the names of those under its root it left out. The copy is closed before
// it returns.
func listConversations(src, tmp string, ws *workspaces, since, maxModified time.Time) ([]AgyConversation, []string, error) {
	db, err := openCopy(src, "conversation_summaries.db", tmp)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT conversation_id, last_modified_time, workspace_uris FROM conversation_summaries`)
	if err != nil {
		return nil, nil, fmt.Errorf("reading Antigravity's conversation list: %w", err)
	}
	defer rows.Close()
	var out []AgyConversation
	var leftOut []string
	for rows.Next() {
		var id, uris string
		var modified any
		if err := rows.Scan(&id, &modified, &uris); err != nil {
			return nil, nil, err
		}
		t, ok := agyTime(modified)
		if !ok || !t.After(since) || (!maxModified.IsZero() && t.After(maxModified)) {
			continue
		}
		if keep, why := ws.judge(uris); !keep {
			if why != "" {
				leftOut = append(leftOut, Printable(id)+" ("+why+")")
			}
			continue
		}
		out = append(out, AgyConversation{ID: id, Modified: t})
	}
	return out, leftOut, rows.Err()
}

// agyTime reads last_modified_time: the driver returns a datetime column as
// a time.Time when it parses, else the stored text. A time before 1970, Go's
// zero time among them, is no time (BUG-181).
func agyTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, t.Unix() >= 0
	case string:
		for _, layout := range []string{agyTimeLayout, time.RFC3339Nano} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return agyTime(parsed)
			}
		}
	case []byte:
		return agyTime(string(t))
	}
	return time.Time{}, false
}

// conversationText is the prose of one conversation's steps, in step order,
// each run once.
func conversationText(src, id, tmp string) (string, error) {
	if strings.ContainsAny(id, `/\`) || id == "" || id == "." || id == ".." {
		return "", nil
	}
	defer func() { _ = removeDB(filepath.Join(tmp, id+".db")) }()
	db, err := openCopy(filepath.Join(src, "conversations"), id+".db", tmp)
	// A missing database, or one (or its -wal, -shm) that is not a regular
	// file, is skipped: one planted FIFO must not fail a project's dream
	// (plan 2026-10-27-harness-audit-fix D8).
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrNotRegular) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer db.Close()
	// Only the user's input (14) and the model's replies (15): a tool step
	// (132) holds file and command output, which never leaves the machine
	// (security review 2026-09-30; the D8 probe's step types).
	rows, err := db.Query(`SELECT step_payload FROM steps WHERE step_type IN (14, 15) ORDER BY idx`)
	if err != nil {
		return "", fmt.Errorf("reading conversation %s: %w", id, err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	var b strings.Builder
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return "", err
		}
		for _, text := range proseLeaves(payload) {
			if t := strings.TrimSpace(text); !seen[t] {
				seen[t] = true
				b.WriteString(t + "\n")
			}
		}
	}
	return b.String(), rows.Err()
}

// removeIfEmpty removes dir only when nothing is in it, so a run that had to
// create ~/.config/fyr for its temp copies leaves no empty directory.
func removeIfEmpty(dir string) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
}
