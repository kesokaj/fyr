package creds

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
)

type credPattern struct {
	kind string
	re   *regexp.Regexp
}

const credPatternMask byte = 0xA5

type maskedCredSpec struct {
	kind    string
	pattern string
}

// The credential shapes of the preamble (:128-137, BUG-160), stored XOR-masked
// with 0xA5 and decoded lazily via sync.Once so compiled .rodata holds no
// printable vendor prefix pieces that the linker could coalesce (BUG-538).
var (
	credNames       = regexp.MustCompile(`^(id_rsa|id_ecdsa|id_ed25519|id_dsa|\.credentials\.json|application_default_credentials\.json|credentials\.db|access_tokens\.db|.*oauth-token.*|hosts\.yml|\.netrc|\.git-credentials|.*\.p12|.*\.pfx)$`)
	credPaths       = regexp.MustCompile(`/\.aws/credentials$|/\.docker/config\.json$`)
	maskedCredSpecs = []maskedCredSpec{
		{"github-token", "\xc2\xcd\xfe\xca\xd5\xd6\xd0\xd7\xf8\xfa\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xf8\xde\x96\x95\x89\xd8"},
		{"github-pat", "\xc2\xcc\xd1\xcd\xd0\xc7\xfa\xd5\xc4\xd1\xfa\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xfa\xf8\xde\x96\x95\x89\xd8"},
		{"private-key", "\x88\x88\x88\x88\x88\xe7\xe0\xe2\xec\xeb\x85\xfe\xe4\x88\xff\x85\xf8\x8f\xf5\xf7\xec\xf3\xe4\xf1\xe0\x85\xee\xe0\xfc\x88\x88\x88\x88\x88"},
		{"anthropic-key", "\xd6\xce\x88\xc4\xcb\xd1\x88\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xfa\x88\xf8\xde\x97\x95\x89\xd8"},
		{"openai-key", "\xd6\xce\x88\xd5\xd7\xca\xcf\x88\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xfa\x88\xf8\xde\x97\x95\x89\xd8"},
		{"refresh-token", "\x87\xd7\xc0\xc3\xd7\xc0\xd6\xcd\xfa\xd1\xca\xce\xc0\xcb\x87"},
		{"google-access-token", "\xdc\xc4\x97\x9c\xf9\x8b\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xfa\x88\xf8\xde\x97\x95\x89\xd8"},
		{"google-api-key", "\xe4\xec\xdf\xc4\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xfa\x88\xf8\xde\x96\x90\xd8"},
		{"gitlab-token", "\xc2\xc9\xd5\xc4\xd1\x88\xfe\xe4\x88\xff\xc4\x88\xdf\x95\x88\x9c\xfa\x88\xf8\xde\x97\x95\x89\xd8"},
	}
	maskedCredAWS        = "\xe4\xee\xec\xe4\xfe\xe4\x88\xff\x95\x88\x9c\xf8\xde\x94\x93\xd8"
	maskedCredExample    = "\xe4\xee\xec\xe4\xec\xea\xf6\xe3\xea\xe1\xeb\xeb\x92\xe0\xfd\xe4\xe8\xf5\xe9\xe0"
	maskedCredRefreshVal = "\xfb\xf9\xd6\x8f\x9f\xf9\xd6\x8f\x8d\x87\xfe\xfb\x87\xf9\xf9\xf9\xd7\xf9\xcb\xf8\xde\x95\x89\x97\x90\x93\xd8\x87\xd9\x82\xfe\xfb\x82\xf9\xf9\xf9\xd7\xf9\xcb\xf8\xde\x95\x89\x97\x90\x93\xd8\x82\xd9\xfe\xfb\xf9\xd6\x89\xd8\xf9\xf8\xf9\xd7\xf9\xcb\xc5\xf8\xde\x94\x89\x97\x90\x93\xd8\x8c"
	credPatternsMu       sync.Once
	credText             []credPattern
	credPrivBegin        *regexp.Regexp
	credAWS              *regexp.Regexp
	credExample          []byte
	credRefreshVal       *regexp.Regexp
)

func unmaskCredPattern(masked string) string {
	b := make([]byte, len(masked))
	for i := range len(masked) {
		b[i] = masked[i] ^ credPatternMask
	}
	return string(b)
}

func ensureCredPatterns() {
	credPatternsMu.Do(initCredPatterns)
}

func initCredPatterns() {
	credText = make([]credPattern, 0, len(maskedCredSpecs))
	for _, spec := range maskedCredSpecs {
		re := regexp.MustCompile(unmaskCredPattern(spec.pattern))
		if spec.kind == "private-key" {
			credPrivBegin = re
		}
		credText = append(credText, credPattern{kind: spec.kind, re: re})
	}
	credAWS = regexp.MustCompile(unmaskCredPattern(maskedCredAWS))
	credExample = []byte(unmaskCredPattern(maskedCredExample))
	credRefreshVal = regexp.MustCompile(unmaskCredPattern(maskedCredRefreshVal))
}

// Finding is what the scan saw at one path: counts, unparsed bare-marker
// counts, and SHA-256 token digests per credential kind, or a problem
// ("unreadable", "a link to a directory, not walked"), which fails closed.
type Finding struct {
	kinds    map[string]int
	unparsed map[string]int
	digests  map[string]map[[32]byte]int
	problem  string
	target   string
	outside  bool // a directory link that resolves outside the scanned tree
}

// Options customizes scan limits and regular-file opening. Zero fields use the
// package defaults.
type Options struct {
	MaxScan        int64
	MaxTestLogScan int64
	TestLogChunk   int
	TestLogOverlap int
	OpenRegular    func(path string, flag int) (*os.File, error)
}

// maxScan bounds the bytes read from one file: a larger file is a problem,
// not a read of its whole into memory.
var maxScan int64 = 64 << 20

func normalizeCredOptions(opts Options) Options {
	if opts.MaxScan <= 0 {
		opts.MaxScan = maxScan
	}
	if opts.MaxTestLogScan <= 0 {
		opts.MaxTestLogScan = maxTestLogScan
	}
	if opts.TestLogChunk <= 0 {
		opts.TestLogChunk = testLogChunk
	}
	if opts.TestLogOverlap <= 0 {
		opts.TestLogOverlap = testLogOverlap
	}
	if opts.OpenRegular == nil {
		opts.OpenRegular = openRegularCredFile
	}
	return opts
}

func openRegularCredFile(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}

// Scan walks dir with default options and returns its findings by path relative
// to dir. It never returns content.
func Scan(dir string) map[string]Finding {
	return ScanWith(dir, Options{})
}

// ScanWith walks dir using opts and returns its findings by path relative to
// dir. It never returns content. A dir that is itself a link is reported, never
// followed.
func ScanWith(dir string, opts Options) map[string]Finding {
	ensureCredPatterns()
	opts = normalizeCredOptions(opts)
	out := map[string]Finding{}
	st, err := os.Lstat(dir)
	if err != nil {
		out["."] = Finding{problem: "unreadable"}
		return out
	}
	if st.Mode()&fs.ModeSymlink != 0 {
		target, _ := os.Readlink(dir)
		out["."] = Finding{problem: "a link, not followed", target: target}
		return out
	}
	w := &credsWalker{root: dir, opts: opts, out: out}
	_ = filepath.WalkDir(dir, w.visit)
	return w.out
}

type credsWalker struct {
	root   string
	opts   Options
	out    map[string]Finding
	locked []string // installed node_modules trees, read by name only
}

func (w *credsWalker) visit(p string, d fs.DirEntry, err error) error {
	rel, _ := filepath.Rel(w.root, p)
	if err != nil {
		w.out[rel] = Finding{problem: "find could not read"}
		if d != nil && d.IsDir() {
			return fs.SkipDir
		}
		return nil
	}
	if d.IsDir() {
		if p != w.root && lockedModules(p, d) {
			w.locked = append(w.locked, p+string(filepath.Separator))
		}
		return nil
	}
	read := w.shouldRead(p, d)
	if f, ok := scanEntry(w.root, p, d, read, w.opts); ok {
		f.outside = f.problem == dirLink && !resolvesUnder(w.root, p)
		w.out[rel] = f
	}
	return nil
}

// shouldRead reports whether p's contents should be scanned. A link out of the
// tree is read even inside an install: what it leads to is not third-party code
// the walk reaches on its own.
func (w *credsWalker) shouldRead(p string, d fs.DirEntry) bool {
	link := d.Type()&fs.ModeSymlink != 0
	if link && !resolvesUnder(w.root, p) {
		return true
	}
	return !underAny(p, w.locked) && !goBuildCacheEntry(w.root, p, d, w.opts)
}

const dirLink = "a link to a directory, not walked"

// lockfiles are the package managers' lockfiles: a node_modules directory
// beside one is that manager's install, third-party code that carries
// credential-shaped strings of its own (prettier's flow plugin, an
// AWS-key-shaped string; BUG-185). Its files are scanned by name, not read.
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

var (
	goBuildCacheRel      = regexp.MustCompile(`^(?:home/)?\.cache/go-build/[0-9a-f]{2}/[0-9a-f]+-[ad]$`)
	goTmpBuildArchiveRel = regexp.MustCompile(`^tmp/go-build[0-9]+/b[0-9]+/_pkg_\.a$`)
)

// goBuildCacheEntry reports whether p is a regular Unix ar archive ("!<arch>\n")
// or linked ELF/Mach-O binary inside the sandbox's Go build cache
// (home/.cache/go-build/<xx>/<hex>-a|-d, BUG-520, BUG-538), or a framing-verified
// Go package archive (__.PKGDEF + "go object " + exact ar EOF accounting) left
// under tmp/go-build<n>/b<n>/_pkg_.a: compiled archives and -race test binaries
// can carry repo test patterns or exceed maxScan, while symlinks, unreadable
// files, credential-named files, bare magic prefixes under tmp/, and non-binary
// files fail closed.
func goBuildCacheEntry(root, p string, d fs.DirEntry, opts Options) bool {
	if d == nil || !d.Type().IsRegular() {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	slash := filepath.ToSlash(rel)
	isCache := goBuildCacheRel.MatchString(slash)
	isTmpPkg := !isCache && goTmpBuildArchiveRel.MatchString(slash)
	if (!isCache && !isTmpPkg) || !resolvesUnder(root, p) {
		return false
	}
	f, err := opts.OpenRegular(p, os.O_RDONLY)
	if err != nil {
		return false
	}
	defer f.Close()
	if isTmpPkg {
		return validGoPkgArchive(f)
	}
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
func scanEntry(root, p string, d fs.DirEntry, read bool, opts Options) (Finding, bool) {
	link := d.Type()&fs.ModeSymlink != 0
	st, err := os.Stat(p)
	if link {
		target, _ := os.Readlink(p)
		switch {
		case err == nil && st.IsDir():
			return Finding{problem: dirLink, target: target}, true
		case err != nil:
			return Finding{problem: "a dangling link", target: target}, true
		}
	}
	if err != nil {
		return Finding{problem: "unreadable"}, true
	}
	if credNames.MatchString(d.Name()) || credPaths.MatchString(filepath.ToSlash(p)) {
		return Finding{kinds: map[string]int{"name": 1}}, true
	}
	if !st.Mode().IsRegular() {
		return Finding{}, false // a device or a fifo is never read
	}
	if !read {
		return checkUnscannedReadable(p, link, opts)
	}
	return scanRegularContent(root, p, d, st.Size(), opts)
}

func checkUnscannedReadable(p string, link bool, opts Options) (Finding, bool) {
	var (
		f   *os.File
		err error
	)
	if !link {
		f, err = opts.OpenRegular(p, os.O_RDONLY)
	} else {
		f, err = os.Open(p)
	}
	if err != nil {
		return Finding{problem: "unreadable"}, true
	}
	f.Close()
	return Finding{}, false
}

func scanRegularContent(root, p string, d fs.DirEntry, size int64, opts Options) (Finding, bool) {
	if size > opts.MaxScan {
		if f, handled, ok := scanGoBuildTestLog(root, p, d, opts); handled {
			return f, ok
		}
		return Finding{problem: fmt.Sprintf("larger than %d bytes, not read", opts.MaxScan)}, true
	}
	b, err := readBoundedLimit(p, opts.MaxScan)
	if err != nil {
		return Finding{problem: "unreadable"}, true
	}
	if int64(len(b)) > opts.MaxScan {
		return Finding{problem: fmt.Sprintf("larger than %d bytes, not read", opts.MaxScan)}, true
	}
	if isExecutable(b) {
		return Finding{}, false // the installed fyr carries the redaction patterns as text
	}
	kinds, unparsed, digests := credKindsLimit(b, opts.MaxScan)
	if len(kinds) == 0 {
		return Finding{}, false
	}
	return Finding{kinds: kinds, unparsed: unparsed, digests: digests}, true
}

func readBoundedLimit(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit+1))
}

// credKinds counts each credential kind, unparsed bare markers, and SHA-256
// digests of matched credential tokens in b; AWS's documented example key is
// not one.
func credKindsLimit(b []byte, limit int64) (map[string]int, map[string]int, map[string]map[[32]byte]int) {
	ensureCredPatterns()
	kinds := map[string]int{}
	unparsed := map[string]int{}
	digests := map[string]map[[32]byte]int{}
	covered := scanSQLiteOverflow(b, limit, kinds, unparsed, digests)
	recordWindowCredsSkip(b, len(b), covered, kinds, unparsed, digests)
	return kinds, unparsed, digests
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

func baselineDigests(baseline map[string]Finding) map[string]map[[32]byte]bool {
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

// New lists the paths of after that carry a novel credential token or name
// finding against baseline, or a problem (links outside the tree, unreadable
// and oversized files always fail closed). Each line is path and reason only,
// never content.
func New(baseline, after map[string]Finding) []string {
	known := baselineDigests(baseline)
	var out []string
	for rel, f := range after {
		if f.problem != "" {
			if !sameBaselineProblem(baseline, rel, f) {
				out = append(out, rel+" ("+f.problem+")")
			}
			continue
		}
		if grew := grewCredKinds(f, baseline[rel], known); len(grew) > 0 {
			sort.Strings(grew)
			out = append(out, rel+" ("+strings.Join(grew, ", ")+")")
		}
	}
	sort.Strings(out)
	return out
}

func sameBaselineProblem(baseline map[string]Finding, rel string, f Finding) bool {
	b, ok := baseline[rel]
	return ok && f.target != "" && b.problem == f.problem && b.target == f.target && !f.outside
}

func grewCredKinds(f, b Finding, known map[string]map[[32]byte]bool) []string {
	var grew []string
	for k, n := range f.kinds {
		if f.unparsed[k] > b.unparsed[k] {
			grew = append(grew, k)
			continue
		}
		ds := f.digests[k]
		if len(ds) == 0 {
			if n > b.kinds[k] {
				grew = append(grew, k)
			}
			continue
		}
		if hasNovelDigest(ds, known[k]) {
			grew = append(grew, k)
		}
	}
	return grew
}

func hasNovelDigest(ds map[[32]byte]int, known map[[32]byte]bool) bool {
	for sum := range ds {
		if !known[sum] {
			return true
		}
	}
	return false
}
