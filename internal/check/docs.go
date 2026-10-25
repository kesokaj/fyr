package check

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	placeholderRe = regexp.MustCompile(`<[^<>]*>`)
	envWordRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	wordRe        = regexp.MustCompile(`^[a-z][a-z-]*$`)
	flagRe        = regexp.MustCompile(`^[\[("']*--([a-z][a-z0-9-]*)`)
	lineSuffixRe  = regexp.MustCompile(`^(.+?):([0-9]+)(?:-([0-9]+))?$`)
	goIdentRe     = regexp.MustCompile(`\.[A-Z]`)
	fenceSplit    = strings.NewReplacer("&&", "\n", "||", "\n", "|", "\n", ";", "\n", "(", "\n", ")", "\n")
	spanSplit     = strings.NewReplacer("&&", "\n", "||", "\n", "|", "\n", "(", "\n", ")", "\n")
)

// maxLineRead is the largest file a `:N` is checked against (plan D19).
const maxLineRead = 16 << 20

// verbFindings checks every `fyr` command a chunk names against v (rule (a),
// D16): the program word is fyr, a path ending /bin/fyr, ./fyr or
// `go run ./cmd/fyr`, after a `$` prompt and VAR=value words. `;` separates
// commands in a fence only: in a span it is the punctuation of a quoted
// message (`…; fyr never removes them`, plan D28).
func verbFindings(where string, c chunk, v Vocabulary) []Finding {
	var out []Finding
	text := placeholderRe.ReplaceAllString(c.text, "")
	split := spanSplit
	if c.fence {
		split = fenceSplit
	}
	for _, seg := range strings.Split(split.Replace(text), "\n") {
		w := strings.Fields(seg)
		i := 0
		for i < len(w) && (w[i] == "$" || envWordRe.MatchString(w[i])) {
			i++
		}
		switch {
		case i < len(w) && (w[i] == "fyr" || w[i] == "./fyr" || strings.HasSuffix(w[i], "/bin/fyr")):
			i++
		case i+2 < len(w) && w[i] == "go" && w[i+1] == "run" && w[i+2] == "./cmd/fyr":
			i += 3
		default:
			continue
		}
		for i < len(w) {
			if w[i] == "--home" || w[i] == "--repo" {
				i += 2
			} else if strings.HasPrefix(w[i], "--home=") || strings.HasPrefix(w[i], "--repo=") {
				i++
			} else {
				break
			}
		}
		if i >= len(w) || !wordRe.MatchString(w[i]) {
			continue
		}
		name := w[i]
		spec, ok := v[name]
		if !ok {
			out = append(out, Finding{where, c.line, "verb", fmt.Sprintf("`fyr %s`: fyr has no verb %s", name, name)})
			continue
		}
		takes, cmd := spec.Flags, "fyr "+name
		if spec.Subverbs != nil {
			for _, s := range w[i+1:] {
				if strings.HasPrefix(s, "-") {
					continue
				}
				if wordRe.MatchString(s) {
					sub, ok := spec.Subverbs[s]
					if !ok {
						out = append(out, Finding{where, c.line, "subverb", fmt.Sprintf("`fyr %s %s`: %s has no subverb %s", name, s, name, s)})
						takes = nil
					} else {
						takes, cmd = sub, cmd+" "+s
					}
				}
				break
			}
		}
		if takes == nil {
			continue
		}
		for _, s := range w {
			if m := flagRe.FindStringSubmatch(s); m != nil && !takes[m[1]] {
				out = append(out, Finding{where, c.line, "flag", fmt.Sprintf("`--%s`: %s does not take it", m[1], cmd)})
			}
		}
	}
	return out
}

// candidate is a span that may name a path of the checkout: p without a
// leading ./ or a trailing /, and the :N or :N-M it carries (0 when none).
type candidate struct {
	where   string
	line    int
	token   string
	p       string
	n, m    int
	hasLine bool
}

// pathCandidate reads one span as a path candidate (rule (a), D11, D12), or
// false: one token, no placeholder or glob, no `..`, no URL, not absolute,
// not a Go identifier, anchored on a tracked top-level entry. The ignore
// check comes later, for every candidate at once.
func (k *checker) pathCandidate(where string, c chunk) (candidate, bool) {
	tok := c.text
	if tok == "" || strings.ContainsAny(tok, " \t<>*{}$~…") || strings.Contains(tok, "..") || strings.Contains(tok, "://") || strings.HasPrefix(tok, "/") {
		return candidate{}, false
	}
	cand := candidate{where: where, line: c.line, token: tok}
	p, n, m, hasLine := splitLine(tok)
	cand.n, cand.m, cand.hasLine = n, m, hasLine
	if p == "" || p == "." {
		return candidate{}, false
	}
	if goIdentRe.MatchString(p[strings.LastIndex(p, "/")+1:]) {
		return candidate{}, false
	}
	first, _, nested := strings.Cut(p, "/")
	if !(k.topDirs[first] || (!nested && k.topFiles[first])) {
		return candidate{}, false
	}
	cand.p = p
	return cand, true
}

// splitLine parts a path token from the :N or :N-M it may end in, and
// drops a leading ./ and every trailing /: a trailing slash after a symlink
// escapes os.Root on a Go before 1.25.12 (GO-2026-4970, security review
// 2026-10-25), so none reaches it.
func splitLine(tok string) (p string, n, m int, hasLine bool) {
	p = tok
	if sm := lineSuffixRe.FindStringSubmatch(tok); sm != nil {
		p = sm[1]
		n, _ = strconv.Atoi(sm[2])
		if sm[3] != "" {
			m, _ = strconv.Atoi(sm[3])
		}
		hasLine = true
	}
	return strings.TrimRight(strings.TrimPrefix(p, "./"), "/"), n, m, hasLine
}

// checkPath gives a candidate's finding, or none (D19): existence through
// the checkout's os.Root, which refuses a symlinked component that leaves
// it; a :N only on a regular file of at most 16 MiB with no NUL in its first
// 8 KiB.
func (k *checker) checkPath(c candidate) (Finding, bool) {
	name := filepath.FromSlash(c.p)
	fi, err := k.root.Stat(name)
	if err != nil {
		why := "does not exist"
		if !errors.Is(err, fs.ErrNotExist) {
			why = "does not resolve inside this checkout"
		}
		return Finding{c.where, c.line, "path", fmt.Sprintf("`%s`: %s %s", c.token, c.p, why)}, true
	}
	if !c.hasLine {
		return Finding{}, false
	}
	if fi.IsDir() {
		return Finding{c.where, c.line, "line", fmt.Sprintf("`%s`: %s is a directory", c.token, c.p)}, true
	}
	n, ok := k.lineCount(name)
	if !ok {
		return Finding{}, false
	}
	if c.n > n || c.m > n {
		return Finding{c.where, c.line, "line", fmt.Sprintf("`%s`: %s has %d lines", c.token, c.p, n)}, true
	}
	return Finding{}, false
}

// lineCount is a text file's line count, cached; false for a binary file
// (a NUL in the first 8 KiB) or one readFile refuses: a symlink, a FIFO or
// anything else not a regular file of at most maxLineRead bytes.
func (k *checker) lineCount(name string) (int, bool) {
	if n, ok := k.lines[name]; ok {
		return n, n >= 0
	}
	n := -1
	if data, err := k.readFile(name); err == nil && !bytes.Contains(data[:min(len(data), 8<<10)], []byte{0}) {
		n = bytes.Count(data, []byte("\n"))
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
	}
	k.lines[name] = n
	return n, n >= 0
}

// ignored is the subset of ps git ignores, asked over stdin so no path
// reaches git's argument parser. git dies on a path beyond a symbolic link
// (`.agents/skills/x` here), so such a path is not asked, and a batch that
// still dies is asked again one path at a time: one bad path must not leave
// every other candidate unignored. A path git cannot answer for is not
// ignored, so it is still checked.
func (k *checker) ignored(ps []string) map[string]bool {
	var ask []string
	seen := map[string]bool{}
	for _, p := range ps {
		if !seen[p] && !k.throughLink(p) {
			ask = append(ask, p)
		}
		seen[p] = true
	}
	out, ok := k.checkIgnore(ask)
	if !ok {
		for _, p := range ask {
			one, _ := k.checkIgnore([]string{p})
			for q := range one {
				out[q] = true
			}
		}
	}
	return out
}

// checkIgnore runs one `git check-ignore`; false when git failed (any exit
// but 0, some ignored, and 1, none).
func (k *checker) checkIgnore(ps []string) (map[string]bool, bool) {
	out := map[string]bool{}
	if len(ps) == 0 {
		return out, true
	}
	cmd := k.git("check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(ps, "\x00") + "\x00")
	data, err := cmd.Output()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) {
		return out, false
	}
	for _, p := range strings.Split(string(data), "\x00") {
		if p != "" {
			out[p] = true
		}
	}
	return out, true
}

// throughLink reports whether a parent directory of p is a symlink inside
// the checkout.
func (k *checker) throughLink(p string) bool {
	for d := path.Dir(p); d != "."; d = path.Dir(d) {
		link, ok := k.links[d]
		if !ok {
			fi, err := k.root.Lstat(filepath.FromSlash(d))
			link = err == nil && fi.Mode()&fs.ModeSymlink != 0
			k.links[d] = link
		}
		if link {
			return true
		}
	}
	return false
}
