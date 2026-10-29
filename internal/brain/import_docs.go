package brain

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/kesokaj/fyr/internal/trail"
)

// docFile is one tracked file of an old docs tree.
type docFile struct {
	rel  string // relative to the docs root: "plans/x.md"
	path string // relative to the checkout: ".agents/docs/plans/x.md"
	text string
}

// docsRootOf resolves import's <docs-root>: the argument, relative to the
// working directory, or the checkout's .agents/docs, else its docs/.
func docsRootOf(dir, top, arg string) (string, error) {
	cands := []string{filepath.Join(top, ".agents", "docs"), filepath.Join(top, "docs")}
	if arg != "" {
		if !filepath.IsAbs(arg) {
			arg = filepath.Join(dir, arg)
		}
		cands = []string{filepath.Clean(arg)}
	}
	for _, c := range cands {
		rel, err := filepath.Rel(top, c)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return "", refuse("%s is not a directory inside the checkout %s", c, top)
		}
		fi, err := os.Lstat(c)
		if errors.Is(err, os.ErrNotExist) && arg == "" {
			continue
		}
		if err != nil {
			return "", refuse("%s: %v", c, err)
		}
		if !fi.IsDir() {
			return "", refuse("%s is not a directory (a symlinked docs root is refused)", c)
		}
		return c, nil
	}
	return "", refuse("no .agents/docs/ or docs/ in %s: nothing to import", top)
}

// readDocs reads every file git tracks under root. A file that is not
// markdown is refused (D17: a script is not a record; move it first), as is
// a symlink; an empty `.gitkeep` placeholder (every /init-project scaffold
// has them) is returned apart, to be removed with the tree.
func readDocs(top, root string, b *budget) ([]docFile, []string, error) {
	relRoot, _ := filepath.Rel(top, root)
	out, err := trail.Command(top, "ls-files", "-z", "--", filepath.ToSlash(relRoot)).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("git ls-files %s: %w", relRoot, err)
	}
	var files []docFile
	var notMD, keeps []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		abs := filepath.Join(top, filepath.FromSlash(p))
		fi, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			continue // deleted in the work tree; the dirty check refuses a real run
		}
		if err != nil {
			return nil, nil, err
		}
		if !fi.Mode().IsRegular() {
			return nil, nil, refuse("%s is not a regular file", p)
		}
		if placeholder(p, abs, fi) {
			keeps = append(keeps, p)
			continue
		}
		if !strings.HasSuffix(p, ".md") {
			notMD = append(notMD, p)
			continue
		}
		f, err := readDoc(root, p, abs, b)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, f)
	}
	if len(notMD) > 0 {
		return nil, nil, refuse("not a record (D17: move scripts out of the trail first): %s", strings.Join(notMD, ", "))
	}
	return files, keeps, nil
}

// placeholder reports an empty or whitespace-only `.gitkeep` (the old
// template's hold one newline), removed with the tree; one with text is
// refused like any other file that is not markdown.
func placeholder(p, abs string, fi os.FileInfo) bool {
	if path.Base(p) != ".gitkeep" || fi.Size() > 64 {
		return false
	}
	data, err := readRegular(abs, 64)
	return err == nil && strings.TrimSpace(string(data)) == ""
}

// readDoc reads one tracked record of the docs tree, within the body cap
// and the import's read budget.
func readDoc(root, p, abs string, b *budget) (docFile, error) {
	data, err := readRegular(abs, maxBody)
	if err != nil {
		if IsRefusal(err) {
			return docFile{}, refuse("%s: %v; a record body is at most 1 MiB of a regular file", p, err)
		}
		return docFile{}, err
	}
	if err := b.take(len(data), p); err != nil {
		return docFile{}, err
	}
	rel, _ := filepath.Rel(root, abs)
	return docFile{rel: filepath.ToSlash(rel), path: p, text: strings.ReplaceAll(string(data), "\r\n", "\n")}, nil
}

// frontmatter splits a leading --- block into its key: value lines and the
// text after it.
func frontmatter(text string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(text, "---\n") {
		return meta, text
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return meta, text
	}
	for _, l := range strings.Split(text[4:4+end], "\n") {
		if k, v, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(l, " ") {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return meta, text[4+end+5:]
}

// heading is the text of the first "# " line, or "".
func heading(body string) string {
	for _, l := range strings.Split(body, "\n") {
		if t, ok := strings.CutPrefix(l, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

// section returns the "## name" section of body, heading included, or "".
func section(body, name string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, l := range lines {
		if start < 0 && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(l, "## ")), name) && strings.HasPrefix(l, "## ") {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(l, "## ") {
			return strings.TrimSpace(strings.Join(lines[start:i], "\n"))
		}
	}
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:], "\n"))
}

// outsideText is what D8's closure starts from besides the kept records:
// the project's own files that stay files, as git tracks them (an ignored or
// untracked file is not the project's text), each read without following a
// link and within the import's budget.
func outsideText(top string, b *budget) ([]string, error) {
	out, err := trail.Command(top, "ls-files", "-z", "--", "PRD.md", "SDD.md", "TDD.md", "REQUIREMENTS.md", "AGENTS.md", "README.md", "context", "skills", "agents", "rules").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var texts []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		data, err := readRegular(filepath.Join(top, filepath.FromSlash(p)), maxBody)
		if err != nil {
			continue // a link, a FIFO or a large asset: no reference is read from it
		}
		if err := b.take(len(data), p); err != nil {
			return nil, err
		}
		texts = append(texts, string(data))
	}
	return texts, nil
}

// keep applies D8: the roots, then everything they or the outside files
// reference, by path or by ID, transitively.
func keep(cands []candidate, outside []string) []candidate {
	byKey := map[string]int{}
	for i, c := range cands {
		byKey[c.rec.ID] = i
		byKey[c.rel] = i
	}
	kept := make([]bool, len(cands))
	var queue []string
	for i, c := range cands {
		if c.root {
			kept[i] = true
			queue = append(queue, c.rec.Body)
		}
	}
	queue = append(queue, outside...)
	for len(queue) > 0 {
		text := queue[0]
		queue = queue[1:]
		refs := append(pathRef.FindAllString(text, -1), idRef.FindAllString(text, -1)...)
		for _, r := range refs {
			if i, ok := byKey[r]; ok && !kept[i] {
				kept[i] = true
				queue = append(queue, cands[i].rec.Body)
			}
		}
	}
	var out []candidate
	for i, c := range cands {
		if kept[i] {
			out = append(out, c)
		}
	}
	return out
}
