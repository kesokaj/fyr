package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// mountInfo is this process's mount table; a test points it at a fixture.
var mountInfo = "/proc/self/mountinfo"

// mountsUnder lists the mount points at or below any of roots. An agent runs
// as the operator's uid and can mount a file system of its own (FUSE:
// fusermount3 is setuid), and every step after a session walks these paths;
// their EvalSymlinks and os.Root see nothing of a mount (PLAN.md item 50). A
// table it cannot read is an error, for the caller to fail closed on. A
// mount inside a namespace of the agent's own is not listed, and is not on
// the host's paths either.
func mountsUnder(roots ...string) ([]string, error) {
	table, err := readMounts()
	if err != nil {
		return nil, err
	}
	var found []string
	for _, m := range table {
		for _, r := range roots {
			if within(m.point, r) {
				found = append(found, m.point)
				break
			}
		}
	}
	return found, nil
}

// mountChains is, for each of roots, every mount above it, id and point, in
// the table's order. Intact compares them, because a file system mounted on
// any parent of the sandbox hides it while its paths still resolve as
// approved; the longest mount point alone stays the same under a mount above
// it (P12b's security review, S2 and round 2's F2). A remount keeps its id.
func mountChains(roots ...string) (map[string]string, error) {
	table, err := readMounts()
	if err != nil {
		return nil, err
	}
	chains := map[string]string{}
	for _, r := range roots {
		var chain []string
		for _, m := range table {
			if within(r, m.point) {
				chain = append(chain, m.id+" "+m.point)
			}
		}
		chains[r] = strings.Join(chain, "\n")
	}
	return chains, nil
}

type mount struct{ id, point string }

// readMounts is the mount table. Its fields are split on one ASCII space:
// the kernel escapes that and not a Unicode space, which strings.Fields
// would split on.
func readMounts() ([]mount, error) {
	b, err := os.ReadFile(mountInfo)
	if err != nil {
		return nil, err
	}
	var table []mount
	for _, l := range strings.Split(string(b), "\n") {
		if l == "" {
			continue
		}
		// mountinfo(5): id, parent, major:minor, root, mount point, ...
		f := strings.Split(l, " ")
		if len(f) < 5 {
			return nil, fmt.Errorf("%s: a line of an unknown shape", mountInfo)
		}
		table = append(table, mount{f[0], unescapeMount(f[4])})
	}
	return table, nil
}

// unescapeMount undoes the kernel's \ooo escapes (space, tab, newline, \).
func unescapeMount(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
