package eval

import (
	"bytes"
	"fmt"
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
}

// maxScan bounds the bytes read from one file: a larger file is a problem,
// not a read of its whole into memory. A variable so a test can lower it.
var maxScan int64 = 64 << 20

// scanCreds walks dir and returns its findings by path relative to dir. It
// never returns content. dir is eval's own sandbox directory; a dir that is
// itself a link is reported, never followed.
func scanCreds(dir string) map[string]credFinding {
	out := map[string]credFinding{}
	st, err := os.Lstat(dir)
	if err != nil {
		return out
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		target, _ := os.Readlink(dir)
		out["."] = credFinding{problem: "a link, not followed", target: target}
		return out
	}
	root := dir
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
			return nil
		}
		if f, ok := scanEntry(p, d); ok {
			out[rel] = f
		}
		return nil
	})
	return out
}

func scanEntry(p string, d fs.DirEntry) (credFinding, bool) {
	link := d.Type()&fs.ModeSymlink != 0
	st, err := os.Stat(p)
	if link {
		target, _ := os.Readlink(p)
		switch {
		case err == nil && st.IsDir():
			return credFinding{problem: "a link to a directory, not walked", target: target}, true
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
	if st.Size() > maxScan {
		return credFinding{problem: fmt.Sprintf("larger than %d bytes, not read", maxScan)}, true
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return credFinding{problem: "unreadable"}, true
	}
	if isExecutable(b) {
		return credFinding{}, false // the installed fyr carries the redaction patterns as text
	}
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
	if len(kinds) == 0 {
		return credFinding{}, false
	}
	return credFinding{kinds: kinds}, true
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
// target; an unreadable or oversized file always counts, since it could
// hold anything (D17, fail closed). Each line is the path and why, never
// content.
func newCreds(baseline, after map[string]credFinding) []string {
	var out []string
	for rel, f := range after {
		if f.problem != "" {
			if b, ok := baseline[rel]; ok && f.target != "" && b.problem == f.problem && b.target == f.target {
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
