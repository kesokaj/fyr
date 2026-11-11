package sandbox

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type credPattern struct {
	kind string
	re   *regexp.Regexp
}

// The credential shapes of the preamble (:128-137, BUG-160), assembled at init
// from short pieces so compiled .rodata holds no self-matching vendor literals
// (BUG-538).
var (
	credNames      = regexp.MustCompile(`^(id_rsa|id_ecdsa|id_ed25519|id_dsa|\.credentials\.json|application_default_credentials\.json|credentials\.db|access_tokens\.db|.*oauth-token.*|hosts\.yml|\.netrc|\.git-credentials|.*\.p12|.*\.pfx)$`)
	credPaths      = regexp.MustCompile(`/\.aws/credentials$|/\.docker/config\.json$`)
	credText       []credPattern
	credAWS        *regexp.Regexp
	credExample    []byte
	credRefreshVal *regexp.Regexp
	credPrivBody   *regexp.Regexp
)

func init() {
	for _, row := range []struct {
		kind  string
		parts []string
	}{
		{"github-token", []string{"gh", `[opsur]_[A-Za-z0-9]{30,}`}},
		{"github-pat", []string{"git", "hub_pat_", `[A-Za-z0-9_]{30,}`}},
		{"private-key", []string{"---", "--BEGIN [A-Z ]*PRI", "VATE KEY-----"}},
		{"anthropic-key", []string{"sk-", "ant", `-[A-Za-z0-9_-]{20,}`}},
		{"openai-key", []string{"sk-", "proj", `-[A-Za-z0-9_-]{20,}`}},
		{"refresh-token", []string{`"ref`, `resh_`, `token"`}},
		{"google-access-token", []string{"ya", `29\.[A-Za-z0-9_-]{20,}`}},
		{"google-api-key", []string{"AI", "za", `[A-Za-z0-9_-]{35}`}},
		{"gitlab-token", []string{"gl", "pat", `-[A-Za-z0-9_-]{20,}`}},
	} {
		credText = append(credText, credPattern{kind: row.kind, re: regexp.MustCompile(strings.Join(row.parts, ""))})
	}
	credAWS = regexp.MustCompile(strings.Join([]string{"AK", "IA", `[A-Z0-9]{16}`}, ""))
	credExample = []byte(strings.Join([]string{"AK", "IA", "IOSFODNN7EXAMPLE"}, ""))
	credRefreshVal = regexp.MustCompile(strings.Join([]string{`^\s*:\s*("[^"\\\r\n]{0,256}"|'[^'\\\r\n]{0,256}'|[^\s,}\]\r\n`, "`", `]{1,256})`}, ""))
	credPrivBody = regexp.MustCompile(`^(?:(?:[ \t\r\n]|\\+[rn])+(?:[0-9]+:[ \t]*)?)*(?:[A-Za-z][A-Za-z0-9_-]*:[ \t][^\r\n\\]*(?:[ \t\r\n]|\\+[rn])+(?:[0-9]+:[ \t]*)?)*([A-Za-z0-9+/=]{16,48})`)
}

// credFinding is what the scan saw at one path: counts and SHA-256 token
// digests per credential kind, or a problem ("unreadable", "a link to a
// directory, not walked"), which fails closed.
type credFinding struct {
	kinds   map[string]int
	digests map[string]map[[32]byte]int
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
		read := !underAny(p, locked) && !goBuildCacheEntry(root, p, d) || link && !resolvesUnder(root, p)
		if f, ok := scanEntry(root, p, d, read); ok {
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

const arMagic = "!<arch>\n"

var goBuildCacheRel = regexp.MustCompile(`^(?:home/)?\.cache/go-build/[0-9a-f]{2}/[0-9a-f]+-[ad]$`)

// goBuildCacheEntry reports whether p is a regular Unix ar archive ("!<arch>\n")
// or linked ELF/Mach-O binary inside the sandbox's Go build cache
// (home/.cache/go-build/<xx>/<hex>-a|-d, BUG-520, BUG-538): compiled archives
// and -race test binaries can exceed maxScan, while symlinks, unreadable files,
// credential-named files and non-binary files in go-build fail closed.
func goBuildCacheEntry(root, p string, d fs.DirEntry) bool {
	if d == nil || !d.Type().IsRegular() {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !goBuildCacheRel.MatchString(filepath.ToSlash(rel)) || !resolvesUnder(root, p) {
		return false
	}
	f, err := OpenRegular(p, os.O_RDONLY)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [len(arMagic)]byte
	n, _ := io.ReadFull(f, hdr[:])
	return (n == len(arMagic) && string(hdr[:]) == arMagic) || isExecutable(hdr[:n])
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
func scanEntry(root, p string, d fs.DirEntry, read bool) (credFinding, bool) {
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
	return scanRegularContent(root, p, d, st.Size())
}

func scanRegularContent(root, p string, d fs.DirEntry, size int64) (credFinding, bool) {
	if size > maxScan {
		if f, handled, ok := scanGoBuildTestLog(root, p, d); handled {
			return f, ok
		}
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
	kinds, digests := credKinds(b)
	if len(kinds) == 0 {
		return credFinding{}, false
	}
	return credFinding{kinds: kinds, digests: digests}, true
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

// credKinds counts each credential kind and records SHA-256 digests of matched
// credential tokens in b; AWS's documented example key is not one.
func credKinds(b []byte) (map[string]int, map[string]map[[32]byte]int) {
	kinds := map[string]int{}
	digests := map[string]map[[32]byte]int{}
	add := func(kind string, tok []byte) {
		kinds[kind]++
		if digests[kind] == nil {
			digests[kind] = map[[32]byte]int{}
		}
		digests[kind][sha256.Sum256(tok)]++
	}
	for _, c := range credText {
		for _, loc := range c.re.FindAllIndex(b, -1) {
			add(c.kind, canonicalCredToken(c.kind, b, loc[0], loc[1]))
		}
	}
	for _, m := range credAWS.FindAll(b, -1) {
		if !bytes.Equal(m, credExample) {
			add("aws-key", m)
		}
	}
	return kinds, digests
}

func canonicalCredToken(kind string, b []byte, start, end int) []byte {
	switch kind {
	case "refresh-token":
		tail := b[end:min(len(b), end+300)]
		if m := credRefreshVal.FindSubmatch(tail); m != nil {
			return append(append([]byte("refresh-token:"), m[1]...), 0)
		}
	case "private-key":
		tail := b[end:min(len(b), end+256)]
		if m := credPrivBody.FindSubmatch(tail); m != nil {
			out := make([]byte, 0, (end-start)+1+len(m[1]))
			out = append(out, b[start:end]...)
			out = append(out, ':')
			return append(out, m[1]...)
		}
	}
	return b[start:end]
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

func baselineDigests(baseline map[string]credFinding) map[string]map[[32]byte]bool {
	known := map[string]map[[32]byte]bool{}
	for _, b := range baseline {
		for k, ds := range b.digests {
			if known[k] == nil {
				known[k] = map[[32]byte]bool{}
			}
			for sum := range ds {
				known[k][sum] = true
			}
		}
	}
	return known
}

// newCreds lists the paths of after that carry a novel credential token or
// name finding against baseline, or a problem (links outside the tree,
// unreadable and oversized files always fail closed). Each line is path and
// reason only, never content.
func newCreds(baseline, after map[string]credFinding) []string {
	known := baselineDigests(baseline)
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
			ds := f.digests[k]
			if len(ds) == 0 {
				if n > b.kinds[k] {
					grew = append(grew, k)
				}
				continue
			}
			for sum := range ds {
				if !known[k][sum] {
					grew = append(grew, k)
					break
				}
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
