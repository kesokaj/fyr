package check

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/kesokaj/fyr/internal/brain"
	"github.com/kesokaj/fyr/internal/trail"
)

const trailPath = ".agents/trail.jsonl"

var (
	// maxTrailBlob is the largest past trail.jsonl read, brain's cap on the
	// trail itself: a blob that compresses to a few MB in a crafted history
	// is refused, never loaded whole (security review 2026-10-31, finding 1).
	maxTrailBlob int64 = 256 << 20
	// maxMergeDepth bounds the descent into merged sides.
	maxMergeDepth = 64
)

// trailHistory finds the commit that adds each current spec's trail line
// (rule (b)) by bisection over the commits that changed trail.jsonl, each
// blob read once and indexed for every spec: about log2(n) reads per spec.
// `git log -G` diffed all n revisions once per spec, which at 399 revisions
// of a 13 MB file was 5 minutes of git CPU per run (2026-10-31).
type trailHistory struct {
	k       *checker
	stamps  map[string]string // spec ID -> its current "updated" stamp
	ids     map[string]bool   // the spec IDs, and the stamps, values keeps
	updates map[string]bool
	head    []string
	held    map[string]map[string]bool // commit -> the IDs whose current line its blob holds
	failed  map[string]error
	reads   int // blobs read: the work count TestSpecCommitBisects pins
}

func (k *checker) trailHistory(specs []brain.Record) *trailHistory {
	h := &trailHistory{k: k, stamps: map[string]string{}, ids: map[string]bool{}, updates: map[string]bool{}, held: map[string]map[string]bool{}, failed: map[string]error{}}
	for _, s := range specs {
		h.stamps[s.ID], h.ids[s.ID], h.updates[s.Updated] = s.Updated, true, true
	}
	return h
}

// git is git in the checkout without its replace refs, grafts or
// commit-graph, so the history read, and rule (b)'s count over it, is the
// one the commits record and cannot be made cyclic (security review
// 2026-10-31, finding 2, and round 2).
func (h *trailHistory) git(args ...string) *exec.Cmd {
	cmd := trail.Command(h.k.top, append([]string{"--no-replace-objects", "-c", "core.commitGraph=false"}, args...)...)
	cmd.Env = append(cmd.Env, "GIT_GRAFT_FILE="+os.DevNull)
	return cmd
}

// commitOf is the commit that adds r's current line, or "" when HEAD's
// trail does not hold it yet: the oldest of HEAD's first-parent trail
// commits from which every later one holds it, and, when that is a merge
// whose merged side holds the line too, the same search down that side.
// The bisection takes the line to stay once added; a line removed and added
// again is found at one of its additions (SPEC-fyr-check § Known limits).
func (h *trailHistory) commitOf(r brain.Record) (string, error) {
	if h.head == nil {
		// specsRule runs only where historyNote found the history readable;
		// checked again so no other caller can lazy-fetch.
		if !trail.Readable(h.k.top) {
			return "", errors.New("a partial clone this git cannot read without fetching")
		}
		cs, err := h.commits("HEAD")
		if err != nil {
			return "", err
		}
		h.head = cs
	}
	return h.search(h.head, r, 0)
}

func (h *trailHistory) search(cs []string, r brain.Record, depth int) (string, error) {
	if len(cs) == 0 {
		return "", nil
	}
	if ok, err := h.holds(cs[0], r); err != nil || !ok {
		return "", err
	}
	var ferr error
	// The first j whose cs[j+1] does not hold the line; cs[0] does.
	j := sort.Search(len(cs)-1, func(j int) bool {
		ok, err := h.holds(cs[j+1], r)
		if err != nil && ferr == nil {
			ferr = err
		}
		return err != nil || !ok
	})
	if ferr != nil {
		return "", ferr
	}
	return h.mergedSide(cs[j], r, depth)
}

// mergedSide is the commit on a merged side of found that adds r's line,
// or found itself when it is no merge or no merged side holds the line.
func (h *trailHistory) mergedSide(found string, r brain.Record, depth int) (string, error) {
	out, err := h.git("rev-parse", found+"^@").Output()
	if err != nil {
		return "", err
	}
	parents := strings.Fields(string(out))
	if len(parents) < 2 {
		return found, nil
	}
	// Without replace refs and grafts the history is acyclic, and each
	// level is older than the last; the bound caps a deep merge nesting.
	if depth >= maxMergeDepth {
		return "", fmt.Errorf("merges below %.12s nest past %d", found, maxMergeDepth)
	}
	for _, p := range parents[1:] {
		side, err := h.commits(p, "^"+parents[0])
		if err != nil {
			return "", err
		}
		if c, err := h.search(side, r, depth+1); err != nil || c != "" {
			return c, err
		}
	}
	return found, nil
}

// commits is the first-parent chain of revs, newest first, of the commits
// that changed trail.jsonl.
func (h *trailHistory) commits(revs ...string) ([]string, error) {
	args := append(append([]string{"rev-list", "--first-parent"}, revs...), "--", trailPath)
	out, err := h.git(args...).Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// holds reports whether trail.jsonl at commit c holds r's current line; a
// commit that deleted the file holds none. The blob is read once.
func (h *trailHistory) holds(c string, r brain.Record) (bool, error) {
	if err := h.failed[c]; err != nil {
		return false, err
	}
	held, ok := h.held[c]
	if !ok {
		var err error
		if held, err = h.read(c); err != nil {
			h.failed[c] = err
			return false, err
		}
		h.held[c] = held
	}
	return held[r.ID], nil
}

// read indexes trail.jsonl at commit c, refusing a blob over maxTrailBlob
// before it is read.
func (h *trailHistory) read(c string) (map[string]bool, error) {
	entry, err := h.git("ls-tree", "-l", c, "--", trailPath).Output()
	if err != nil || len(entry) == 0 {
		return map[string]bool{}, err
	}
	f := strings.Fields(string(entry))
	if len(f) < 4 || f[1] != "blob" {
		return map[string]bool{}, nil
	}
	if size, err := strconv.ParseInt(f[3], 10, 64); err != nil || size > maxTrailBlob {
		return nil, fmt.Errorf("%.12s:%s is over %d bytes", c, trailPath, maxTrailBlob)
	}
	data, err := h.blob(f[2])
	h.reads++
	if err != nil {
		return nil, err
	}
	return h.index(data), nil
}

// blob reads one blob, capped at maxTrailBlob however large it turns out.
func (h *trailHistory) blob(oid string) ([]byte, error) {
	cmd := h.git("cat-file", "blob", oid)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(out, maxTrailBlob+1))
	if err == nil && int64(len(data)) > maxTrailBlob {
		err = fmt.Errorf("blob %.12s is over %d bytes", oid, maxTrailBlob)
	}
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	return data, cmd.Wait()
}

// index is the IDs of the specs whose current line data holds: a line
// holding `"id":"<ID>"` and, after it, `"updated":"<stamp>"`, both literal,
// as `git log -G` matched `"id":"<ID>".*"updated":"<stamp>"`. Each line is
// scanned once, so a crafted line costs its length, not its length squared
// (security review 2026-10-31, round 2).
func (h *trailHistory) index(data []byte) map[string]bool {
	held := map[string]bool{}
	for len(data) > 0 {
		line := data
		if end := bytes.IndexByte(data, '\n'); end >= 0 {
			line, data = data[:end], data[end+1:]
		} else {
			data = nil
		}
		last := values(line, `"updated":"`, false, h.updates) // stamp -> where its last "updated" starts
		for id, at := range values(line, `"id":"`, true, h.ids) {
			if u, ok := last[h.stamps[id]]; ok && u >= at {
				held[id] = true
			}
		}
	}
	return held
}

// values maps each quoted value after key in line that keep holds to a
// position: with first, the end of its first `key value"`; else the start of
// its last. A value runs to the next quote, and the search goes on from that
// quote, which may open the next key, so one pass reads the line.
func values(line []byte, key string, first bool, keep map[string]bool) map[string]int {
	out := map[string]int{}
	k := []byte(key)
	for at := 0; ; {
		i := bytes.Index(line[at:], k)
		if i < 0 {
			return out
		}
		start := at + i
		q := bytes.IndexByte(line[start+len(k):], '"')
		if q < 0 {
			return out
		}
		quote := start + len(k) + q
		v := line[start+len(k) : quote]
		_, seen := out[string(v)]
		switch {
		case !keep[string(v)]:
		case !first:
			out[string(v)] = start
		case !seen:
			out[string(v)] = quote + 1
		}
		at = quote
	}
}
