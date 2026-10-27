package search

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// indexStat is what git's index records of a file's stat.
type indexStat struct{ size, sec, nsec int64 }

// indexStats reads the size and mtime git's index holds for each entry
// (`ls-files -s --debug`, which refreshes nothing and runs no filter or hook)
// and the index file's own mtime. On any error it returns nothing, and every
// tracked file is hashed.
//
// The output is read line by line without -z: git quotes a name holding a
// newline, a tab, a quote or a backslash, so a header line can never carry
// another entry's lines, and a quoted name is skipped (its file is hashed).
// With -z a name holding a newline could forge another file's stat lines
// (security review of BUG-165's fix, finding 2).
func indexStats(root string) (map[string]indexStat, time.Time) {
	out, err := git(root, nil, "ls-files", "-s", "--debug")
	if err != nil {
		return nil, time.Time{}
	}
	idx, err := git(root, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, time.Time{}
	}
	ip := strings.TrimSpace(string(idx))
	if !filepath.IsAbs(ip) {
		ip = filepath.Join(root, ip)
	}
	ifi, err := os.Stat(ip)
	if err != nil {
		return nil, time.Time{}
	}
	return parseIndexStats(out), ifi.ModTime()
}

// parseIndexStats reads `ls-files -s --debug` output: an entry's stat
// counts only when both its mtime and its size parsed.
func parseIndexStats(out []byte) map[string]indexStat {
	stats := map[string]indexStat{}
	cur, seen := "", 0
	var st indexStat
	flush := func() {
		if cur != "" && seen == 2 {
			stats[cur] = st
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, " ") {
			// A header: "<mode> <oid> <stage>\t<path>".
			flush()
			cur, seen, st = "", 0, indexStat{}
			if _, p, ok := strings.Cut(line, "\t"); ok && !strings.HasPrefix(p, `"`) {
				cur = p
			}
			continue
		}
		f := strings.Fields(strings.ReplaceAll(line, "\t", " "))
		switch {
		case len(f) >= 2 && f[0] == "mtime:":
			if sec, nsec, ok := strings.Cut(f[1], ":"); ok {
				a, e1 := strconv.ParseInt(sec, 10, 64)
				b, e2 := strconv.ParseInt(nsec, 10, 64)
				if e1 == nil && e2 == nil {
					st.sec, st.nsec = a, b
					seen++
				}
			}
		case len(f) >= 2 && f[0] == "size:":
			if v, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				st.size = v
				seen++
			}
		}
	}
	flush()
	return stats
}
