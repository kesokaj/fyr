package guard

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// GitIndexEntry represents one parsed entry from a git DIRC index file.
type GitIndexEntry struct {
	Path      string
	MTimeSec  uint32
	MTimeNsec uint32
	Mode      uint32
	Size      uint32
	Stage     uint16
	SHA1      [20]byte
}

// GoTreeState describes the current working-tree state of Go source and module files.
type GoTreeState struct {
	Branch      string
	DirtyGo     []string
	GoFileCount int
	TreeSHA256  string
}

// ResolveGitWorktreeDir returns the worktree gitdir path for root without spawning git.
func ResolveGitWorktreeDir(root string) string {
	dotGit := filepath.Join(root, ".git")
	if isRegularDirNoSymlink(dotGit) {
		return dotGit
	}
	target, ok := readDotGitFileTarget(dotGit, root)
	if !ok || !isRegularDirNoSymlink(target) {
		return ""
	}
	return target
}

func readGitBranchFast(root, gitDir string, reftableBranchFn func(string) string) string {
	if gitDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if branch, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		if branch == ".invalid" {
			if reftableBranchFn != nil {
				return reftableBranchFn(root)
			}
			return ""
		}
		return branch
	}
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return ref
	}
	if len(head) >= 8 {
		return head[:8]
	}
	return head
}

// ComputeGoTreeState inspects .git/HEAD, .git/index (DIRC v2/v3/v4), and Go files on disk,
// returning the dirty Go paths and a deterministic Go tree hash.
func ComputeGoTreeState(root string, reftableBranchFn func(string) string) (GoTreeState, error) {
	gitDir := ResolveGitWorktreeDir(root)
	state := GoTreeState{Branch: readGitBranchFast(root, gitDir, reftableBranchFn)}
	var (
		indexEntries []GitIndexEntry
		hasIndex     bool
		err          error
	)
	if gitDir != "" {
		indexEntries, hasIndex, err = ReadGitIndex(filepath.Join(gitDir, "index"))
		if err != nil {
			return state, err
		}
	}
	tracked, dirtySet := CheckTrackedGoEntries(root, indexEntries, hasIndex)
	diskFiles, untrackedDirty, err := walkGoFilesForState(root, tracked, gitDir != "")
	if err != nil {
		return state, err
	}
	for p := range untrackedDirty {
		dirtySet[p] = true
	}
	state.DirtyGo = slices.Sorted(maps.Keys(dirtySet))
	state.GoFileCount = len(diskFiles)
	state.TreeSHA256 = hashGoDiskFiles(root, diskFiles)
	return state, nil
}

// CheckTrackedGoEntries compares tracked git index entries against disk to find dirty Go paths.
func CheckTrackedGoEntries(root string, entries []GitIndexEntry, hasIndex bool) (map[string]bool, map[string]bool) {
	tracked := make(map[string]bool, len(entries))
	dirty := make(map[string]bool)
	if !hasIndex {
		return tracked, dirty
	}
	for _, e := range entries {
		if !filepath.IsLocal(filepath.FromSlash(e.Path)) {
			continue
		}
		tracked[e.Path] = true
		if !isGoRelevantPath(e.Path) || (e.Mode&0o170000) != 0o100000 {
			continue
		}
		if e.Stage != 0 || IsTrackedEntryDirty(root, e) {
			dirty[e.Path] = true
		}
	}
	return tracked, dirty
}

// IsTrackedEntryDirty reports whether a tracked index entry differs from the file on disk.
func IsTrackedEntryDirty(root string, e GitIndexEntry) bool {
	local := filepath.FromSlash(e.Path)
	if !filepath.IsLocal(local) {
		return false
	}
	abs := filepath.Join(root, local)
	fi, err := os.Lstat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return true
	}
	mt := fi.ModTime()
	if fi.Size() == int64(e.Size) && mt.Unix() == int64(e.MTimeSec) && uint32(mt.Nanosecond()) == e.MTimeNsec && e.MTimeSec != 0 {
		return false
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return true
	}
	sum := gitBlobSHA1(data)
	return !bytes.Equal(sum[:], e.SHA1[:])
}

func walkGoFilesForState(root string, tracked map[string]bool, isGit bool) ([]string, map[string]bool, error) {
	var files []string
	untracked := make(map[string]bool)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		return stepGoWalkEntry(root, p, d, walkErr, tracked, isGit, &files, untracked)
	})
	sort.Strings(files)
	return files, untracked, err
}

func stepGoWalkEntry(root, p string, d fs.DirEntry, walkErr error, tracked map[string]bool, isGit bool, files *[]string, untracked map[string]bool) error {
	if walkErr != nil {
		return nil
	}
	if d.IsDir() {
		if shouldSkipGoWalkDir(root, p, d.Name()) {
			return filepath.SkipDir
		}
		return nil
	}
	if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
		return nil
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if !isGoRelevantPath(rel) {
		return nil
	}
	*files = append(*files, rel)
	if isGit && !tracked[rel] {
		untracked[rel] = true
	}
	return nil
}

func shouldSkipGoWalkDir(root, p, name string) bool {
	return p != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules")
}

func isGoRelevantPath(rel string) bool {
	if strings.HasSuffix(rel, ".go") {
		return true
	}
	base := filepath.Base(rel)
	return base == "go.mod" || base == "go.sum"
}

func hashGoDiskFiles(root string, files []string) string {
	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(data))
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func gitBlobSHA1(data []byte) [20]byte {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	var out [20]byte
	copy(out[:], h.Sum(nil))
	return out
}

// ReadGitIndex reads and parses a git DIRC index file at path.
func ReadGitIndex(path string) ([]GitIndexEntry, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	entries, err := ParseGitIndex(data)
	return entries, err == nil, err
}

// ParseGitIndex parses raw git DIRC v2/v3/v4 index bytes into entries.
func ParseGitIndex(data []byte) ([]GitIndexEntry, error) {
	if len(data) < 12 || string(data[:4]) != "DIRC" {
		return nil, fmt.Errorf("invalid git index header")
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version < 2 || version > 4 {
		return nil, fmt.Errorf("unsupported git index version %d", version)
	}
	rawCount := binary.BigEndian.Uint32(data[8:12])
	maxEntries := uint32((len(data) - 12) / 62)
	if rawCount > maxEntries {
		return nil, fmt.Errorf("invalid git index entry count %d", rawCount)
	}
	count := int(rawCount)
	entries := make([]GitIndexEntry, 0, count)
	pos, prev := 12, ""
	for i := 0; i < count; i++ {
		e, nextPos, err := parseGitIndexEntry(data, pos, version, prev)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
		prev, pos = e.Path, nextPos
	}
	return entries, nil
}

func parseGitIndexEntry(data []byte, pos int, version uint32, prevPath string) (GitIndexEntry, int, error) {
	if pos+62 > len(data) {
		return GitIndexEntry{}, 0, fmt.Errorf("truncated git index entry")
	}
	flags := binary.BigEndian.Uint16(data[pos+60 : pos+62])
	nameStart := pos + 62
	if version >= 3 && (flags&0x4000) != 0 {
		nameStart += 2
	}
	if nameStart >= len(data) {
		return GitIndexEntry{}, 0, fmt.Errorf("truncated git index entry name")
	}
	e := GitIndexEntry{
		MTimeSec:  binary.BigEndian.Uint32(data[pos+8 : pos+12]),
		MTimeNsec: binary.BigEndian.Uint32(data[pos+12 : pos+16]),
		Mode:      binary.BigEndian.Uint32(data[pos+24 : pos+28]),
		Size:      binary.BigEndian.Uint32(data[pos+36 : pos+40]),
		Stage:     (flags >> 12) & 0x3,
	}
	copy(e.SHA1[:], data[pos+40:pos+60])
	path, nextPos, err := DecodeGitIndexPath(data, pos, nameStart, version, prevPath)
	if err != nil {
		return GitIndexEntry{}, 0, err
	}
	e.Path = path
	return e, nextPos, nil
}

// DecodeGitIndexPath decodes an entry path for DIRC v2/v3 or prefix-compressed v4.
func DecodeGitIndexPath(data []byte, entryStart, nameStart int, version uint32, prevPath string) (string, int, error) {
	if version >= 4 {
		strip, idx, ok := readGitVarint(data, nameStart)
		if !ok || strip < 0 || strip > len(prevPath) {
			return "", 0, fmt.Errorf("invalid git index v4 varint")
		}
		nul := bytes.IndexByte(data[idx:], 0)
		if nul < 0 {
			return "", 0, fmt.Errorf("unterminated git index v4 path")
		}
		path := prevPath[:len(prevPath)-strip] + string(data[idx:idx+nul])
		return path, idx + nul + 1, nil
	}
	nul := bytes.IndexByte(data[nameStart:], 0)
	if nul < 0 {
		return "", 0, fmt.Errorf("unterminated git index path")
	}
	nulPos := nameStart + nul
	nextPos := entryStart + ((nulPos - entryStart + 8) &^ 7)
	return string(data[nameStart:nulPos]), nextPos, nil
}

func readGitVarint(data []byte, idx int) (int, int, bool) {
	if idx >= len(data) {
		return 0, idx, false
	}
	b := data[idx]
	val := int(b & 0x7f)
	idx++
	const maxVarintVal = ((math.MaxInt - 127) >> 7) - 1
	for (b & 0x80) != 0 {
		if idx >= len(data) || val > maxVarintVal {
			return 0, idx, false
		}
		b = data[idx]
		idx++
		val = ((val + 1) << 7) | int(b&0x7f)
	}
	return val, idx, true
}
