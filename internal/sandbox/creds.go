package sandbox

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The credential shapes of the preamble (:128-137, BUG-160), kept as one
// regexp per kind so the scan can count each: a growth in any one count is a
// new credential (D17).
var (
	credNames = regexp.MustCompile(`^(id_rsa|id_ecdsa|id_ed25519|id_dsa|\.credentials\.json|application_default_credentials\.json|credentials\.db|access_tokens\.db|.*oauth-token.*|hosts\.yml|\.netrc|\.git-credentials|.*\.p12|.*\.pfx)$`)
	credPaths = regexp.MustCompile(`/\.aws/credentials$|/\.docker/config\.json$`)
	credText  = []struct {
		kind string
		re   *regexp.Regexp
	}{
		{"github-token", regexp.MustCompile(`gh[opsur]_[A-Za-z0-9]{30,}`)},
		{"github-pat", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{30,}`)},
		{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
		{"anthropic-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
		{"openai-key", regexp.MustCompile(`sk-proj-[A-Za-z0-9_-]{20,}`)},
		{"refresh-token", regexp.MustCompile(`"refresh_token"`)},
		{"google-access-token", regexp.MustCompile(`ya29\.[A-Za-z0-9_-]{20,}`)},
		{"google-api-key", regexp.MustCompile(`AIza[A-Za-z0-9_-]{35}`)},
		{"gitlab-token", regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`)},
	}
	// AWS keys are counted apart: AWS's published example key is
	// documentation an agent reads in a test, and only that exact key is set
	// aside.
	credAWS     = regexp.MustCompile(`AKIA[A-Z0-9]{16}`)
	credExample = []byte("AKIA" + "IOSFODNN7EXAMPLE")
)

// credFinding is what the scan saw at one path: a count per credential
// kind, or a problem ("unreadable", "a link to a directory, not walked"),
// which fails closed. A link's problem carries its target: a link the
// baseline held, unchanged, is the fixture's (a tracked directory link),
// while an unreadable file always fails, since its content cannot be
// compared.
type credFinding struct {
	kinds   map[string]int
	problem string
	target  string
	outside bool // a directory link that resolves outside the scanned tree
}

// maxScan bounds the bytes read from one file: a larger file is a problem,
// not a read of its whole into memory. A variable so a test can lower it.
var maxScan int64 = 64 << 20

// scanCreds walks dir and returns its findings by path relative to dir. It
// never returns content. dir is the box's own sandbox directory; a dir that is
// itself a link is reported, never followed.
func scanCreds(dir string) map[string]credFinding {
	out := map[string]credFinding{}
	st, err := os.Lstat(dir)
	if err != nil {
		out["."] = credFinding{problem: "unreadable"}
		return out
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		target, _ := os.Readlink(dir)
		out["."] = credFinding{problem: "a link, not followed", target: target}
		return out
	}
	root := dir
	var locked []string // installed node_modules trees, read by name only
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, p)
		if err != nil {
			out[rel] = credFinding{problem: "find could not read"}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != root && lockedModules(p, d) {
				locked = append(locked, p+string(filepath.Separator))
			}
			return nil
		}
		// A link out of the tree is read even inside an install: what it
		// leads to is not third-party code the walk reaches on its own.
		link := d.Type()&fs.ModeSymlink != 0
		read := !underAny(p, locked) || link && !resolvesUnder(root, p)
		if f, ok := scanEntry(p, d, read); ok {
			f.outside = f.problem == dirLink && !resolvesUnder(root, p)
			out[rel] = f
		}
		return nil
	})
	return out
}

const dirLink = "a link to a directory, not walked"

// lockfiles are the package managers' lockfiles: a node_modules directory
// beside one is that manager's install, third-party code that carries
// credential-shaped strings of its own (prettier's flow plugin, an
// AKIA-shaped string; BUG-185). Its files are scanned by name, not read.
var lockfiles = []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb"}

func lockedModules(p string, d fs.DirEntry) bool {
	if d.Name() != "node_modules" {
		return false
	}
	for _, l := range lockfiles {
		if st, err := os.Lstat(filepath.Join(filepath.Dir(p), l)); err == nil && st.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

// resolvesUnder says whether link p, followed to the end, lands inside root.
func resolvesUnder(root, p string) bool {
	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	t, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(r, t)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// scanEntry reads p's content only when read is set; a name, a link and an
// unreadable file are checked either way, and a size only matters to a read.
func scanEntry(p string, d fs.DirEntry, read bool) (credFinding, bool) {
	link := d.Type()&fs.ModeSymlink != 0
	st, err := os.Stat(p)
	if link {
		target, _ := os.Readlink(p)
		switch {
		case err == nil && st.IsDir():
			return credFinding{problem: dirLink, target: target}, true
		case err != nil:
			return credFinding{problem: "a dangling link", target: target}, true
		}
	}
	if err != nil {
		return credFinding{problem: "unreadable"}, true
	}
	if credNames.MatchString(d.Name()) || credPaths.MatchString(filepath.ToSlash(p)) {
		return credFinding{kinds: map[string]int{"name": 1}}, true
	}
	if !st.Mode().IsRegular() {
		return credFinding{}, false // a device or a fifo is never read
	}
	if !read {
		f, err := os.Open(p)
		if err != nil {
			return credFinding{problem: "unreadable"}, true
		}
		f.Close()
		return credFinding{}, false
	}
	if st.Size() > maxScan {
		return credFinding{problem: fmt.Sprintf("larger than %d bytes, not read", maxScan)}, true
	}
	b, err := readBounded(p)
	if err != nil {
		return credFinding{problem: "unreadable"}, true
	}
	if int64(len(b)) > maxScan {
		return credFinding{problem: fmt.Sprintf("larger than %d bytes, not read", maxScan)}, true
	}
	if isExecutable(b) {
		return credFinding{}, false // the installed fyr carries the redaction patterns as text
	}
	kinds := credKinds(b)
	if len(kinds) == 0 {
		return credFinding{}, false
	}
	return credFinding{kinds: kinds}, true
}

// readBounded reads at most maxScan+1 bytes of p: a /proc file stats as size
// 0 and may never end, and a link a docker session plants resolves on the
// host (the v5.5.0 gate, finding S1).
func readBounded(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxScan+1))
}

// credKinds counts each credential kind in b; AWS's documented example key
// is not one.
func credKinds(b []byte) map[string]int {
	kinds := map[string]int{}
	for _, c := range credText {
		if n := len(c.re.FindAllIndex(b, -1)); n > 0 {
			kinds[c.kind] = n
		}
	}
	n := 0
	for _, m := range credAWS.FindAll(b, -1) {
		if !bytes.Equal(m, credExample) {
			n++
		}
	}
	if n > 0 {
		kinds["aws-key"] = n
	}
	return kinds
}

// isExecutable is the ELF and Mach-O magic skip of the preamble (:161-162).
func isExecutable(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	switch string(b[:4]) {
	case "\x7fELF", "\xcf\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xca\xfe\xba\xbe":
		return true
	}
	return false
}

// newCreds lists the paths of after that are new against the baseline: a
// path the baseline lacks, a kind it lacks, or a kind whose count grew. A
// problem counts unless it is a link the baseline held with the same
// target and, for a directory link, whose target lies inside the scanned
// tree, where the walk reads it under its own path; one that leads out is
// unread, so it always counts (gate review of v3.0.0). An unreadable or
// oversized file always counts, since it could hold anything (D17, fail
// closed). Each line is the path and why, never
// content.
func newCreds(baseline, after map[string]credFinding) []string {
	var out []string
	for rel, f := range after {
		if f.problem != "" {
			if b, ok := baseline[rel]; ok && f.target != "" && b.problem == f.problem && b.target == f.target && !f.outside {
				continue
			}
			out = append(out, rel+" ("+f.problem+")")
			continue
		}
		b := baseline[rel]
		var grew []string
		for k, n := range f.kinds {
			if n > b.kinds[k] {
				grew = append(grew, k)
			}
		}
		if len(grew) > 0 {
			sort.Strings(grew)
			out = append(out, rel+" ("+strings.Join(grew, ", ")+")")
		}
	}
	sort.Strings(out)
	return out
}
