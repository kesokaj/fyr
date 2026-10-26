package brain

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kesokaj/fyr/internal/trail"
)

// readMemoryStore reads the old memory store at <root>/.agents/memory (the
// main worktree's, which every worktree shared) as memory records, with the
// trust checks the store always had (ADR-043): a symlinked or non-directory
// .agents or store is refused, and so is a store git tracks anything under (a
// committed memory is the repository's text, not the operator's). No store
// is no records. A file it cannot take is a problem, and only the files it
// took, with the store's index and lock, are listed for removal.
func readMemoryStore(root string, b *budget) ([]Imported, []string, []string, error) {
	if err := guardExportPath(root); err != nil {
		return nil, nil, nil, err
	}
	store := filepath.Join(root, ".agents", "memory")
	fi, err := os.Lstat(store)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	if !fi.IsDir() {
		return nil, nil, nil, refuse("%s is not a directory (a symlinked store is refused)", store)
	}
	tracked, err := trail.Command(root, "ls-files", "--", ".agents/memory").Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("git ls-files .agents/memory: %w", err)
	}
	if len(bytes.TrimSpace(tracked)) > 0 {
		return nil, nil, nil, refuse("git tracks files under %s: a committed memory is the repository's text, never the operator's; not imported", store)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil, nil, nil, err
	}
	var recs []Imported
	var files, problems []string
	for _, e := range entries {
		rel := filepath.ToSlash(filepath.Join(".agents", "memory", e.Name()))
		if e.Name() == "MEMORY.md" || e.Name() == ".lock" {
			if e.Type().IsRegular() {
				files = append(files, rel)
			}
			continue
		}
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			problems = append(problems, rel+": not a memory file; left alone")
			continue
		}
		data, err := readRegular(filepath.Join(store, e.Name()), maxBody)
		if err != nil {
			problems = append(problems, rel+": "+err.Error()+"; not imported")
			continue
		}
		if err := b.take(len(data), rel); err != nil {
			return nil, nil, nil, err
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		meta, body := frontmatter(text)
		name := strings.TrimSuffix(e.Name(), ".md")
		typ, modified := "project", ""
		for _, l := range strings.Split(text, "\n") {
			l = strings.TrimSpace(l)
			if t, ok := strings.CutPrefix(l, "type: "); ok && memoryTypes[t] {
				typ = t
			}
			if t, ok := strings.CutPrefix(l, "modified: "); ok {
				modified = t
			}
		}
		body = strings.TrimSpace(body)
		if !memorySlug.MatchString(name) || body == "" {
			problems = append(problems, rel+": not a memory the store takes (its name, or an empty body); not imported")
			continue
		}
		day, updated := "", ""
		if t, err := time.Parse(time.RFC3339, modified); err == nil {
			day, updated = t.UTC().Format("2006-01-02"), t.UTC().Format(time.RFC3339)
		} else if info, err := e.Info(); err == nil {
			day, updated = info.ModTime().UTC().Format("2006-01-02"), info.ModTime().UTC().Format(time.RFC3339)
		}
		r := normalize(Record{ID: "MEMORY-" + name, Kind: "memory", Status: "closed", Date: day, Title: meta["description"],
			Body: "type: " + typ + "\n\n" + body, Updated: updated})
		// A file the store would refuse is reported and skipped here: left to
		// insertNew it would roll back the whole import, every project's under
		// --global (a body the type line pushes past maxBody, a day no clock
		// gave).
		if err := checkRecord(r); err != nil {
			problems = append(problems, rel+": "+err.Error()+"; not imported")
			continue
		}
		files = append(files, rel)
		recs = append(recs, Imported{ID: r.ID, Kind: "memory", Status: "closed", Path: rel, rec: r})
	}
	return recs, files, problems, nil
}

// normalize makes an imported record one the load takes back: control
// characters but tab and newline as "?", a title of at most maxTitle bytes
// on one line, an empty title or body named.
func normalize(r Record) Record {
	clean := func(s string) string {
		return strings.Map(func(c rune) rune {
			if (c < 0x20 && c != '\t' && c != '\n') || c == 0x7f {
				return '?'
			}
			return c
		}, strings.ToValidUTF8(s, "?"))
	}
	r.Body = clean(r.Body)
	if strings.TrimSpace(r.Body) == "" {
		r.Body = "(empty)"
	}
	t := oneLine(clean(r.Title))
	for len(t) > maxTitle {
		_, size := utf8.DecodeLastRuneInString(t)
		t = t[:len(t)-size]
	}
	r.Title = t
	return r
}
