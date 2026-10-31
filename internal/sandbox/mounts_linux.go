package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MountsChecked reports whether a mount under the sandbox is seen on this
// system: not without /proc, as ProcsChecked.
const MountsChecked = true

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
	b, err := os.ReadFile(mountInfo)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, l := range strings.Split(string(b), "\n") {
		if l == "" {
			continue
		}
		// mountinfo(5): id, parent, major:minor, root, mount point, ...
		f := strings.Fields(l)
		if len(f) < 5 {
			return nil, fmt.Errorf("%s: a line of an unknown shape", mountInfo)
		}
		mp := unescapeMount(f[4])
		for _, r := range roots {
			if within(mp, r) {
				found = append(found, mp)
				break
			}
		}
	}
	return found, nil
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
