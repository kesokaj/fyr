package work

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

var nodeDigestRe = regexp.MustCompile(`^node:24-trixie-slim@sha256:[0-9a-f]{64}$`)

const nodeVersionGuard = "RUN node --version && nodejs --version && npm --version && npx --version && corepack --version"

var wantNodeLinks = []string{
	"RUN ln -s /usr/local/bin/node /usr/local/bin/nodejs && \\",
	"    ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm && \\",
	"    ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx && \\",
	"    ln -s ../lib/node_modules/corepack/dist/corepack.js /usr/local/bin/corepack",
}

var wantNodeCopies = []string{
	" /usr/local/bin/node /usr/local/bin/node",
	" /usr/local/include/node /usr/local/include/node",
	" /usr/local/lib/node_modules /usr/local/lib/node_modules",
}

// TestImageNodePinned pins Item 13 of PLAN-2026-11-02-1806-fyr-speed: the
// final stage of docker/work.Dockerfile copies node, the Node headers and
// node_modules from a single digest-pinned official Node 24 LTS trixie-slim
// image, links nodejs, npm, npx and corepack, and runs their --version checks
// after ENV PATH and before dropping root.
func TestImageNodePinned(t *testing.T) {
	b, err := os.ReadFile("../../docker/work.Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	real := string(b)
	if err := checkDockerfileNode(real); err != nil {
		t.Fatal(err)
	}
	const pin = "node:24-trixie-slim@sha256:0820eaa85e07d0490446dd01d14707e17e6108a36988cf374899cd057eb3812d"
	const other = "node:24-trixie-slim@sha256:1111111111111111111111111111111111111111111111111111111111111111"
	guard := nodeVersionGuard + "\n"
	for _, tc := range []struct{ name, df, want string }{
		{"unpinned-tag", strings.Replace(real, pin, "node:24-trixie-slim", 1), "not a digest-pinned"},
		{"mismatched-digests", strings.Replace(real, pin, other, 1), "different image pins"},
		{"missing-copy", strings.Replace(real, "COPY --from="+pin+" /usr/local/include/node /usr/local/include/node\n", "", 1), "want 3"},
		{"missing-symlink", strings.Replace(real, wantNodeLinks[2], "    true && \\", 1), "Node symlinks"},
		{"missing-path", strings.Replace(real, "ENV PATH=", "# ENV PATH=", 1), "before USER"},
		{"missing-version-guard", strings.Replace(real, guard, "", 1), nodeVersionGuard},
		{"guard-after-user", strings.Replace(real, guard, "", 1) + "\n" + guard, "before USER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkDockerfileNode(tc.df); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func checkDockerfileNode(dockerfile string) error {
	lines := strings.Split(dockerfile, "\n")
	lastFrom, pathLine, userLine, checkLine := -1, -1, -1, -1
	var nodeCopies []int
	var pins []string
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "FROM "):
			lastFrom = i
		case strings.HasPrefix(l, "ENV PATH=") && i > lastFrom:
			pathLine = i
		case strings.HasPrefix(l, "USER ") && i > lastFrom:
			userLine = i
		case l == nodeVersionGuard:
			checkLine = i
		case strings.HasPrefix(l, "COPY --from=") && l != "COPY --from=build /out/fyr /usr/local/bin/fyr":
			from, _, _ := strings.Cut(strings.TrimPrefix(l, "COPY --from="), " ")
			if !nodeDigestRe.MatchString(from) {
				return fmt.Errorf("line %d: COPY --from=%q is not a digest-pinned node:24-trixie-slim@sha256:<64-hex>", i+1, from)
			}
			nodeCopies, pins = append(nodeCopies, i), append(pins, from)
		}
	}
	if len(nodeCopies) != len(wantNodeCopies) {
		return fmt.Errorf("found %d Node COPY --from lines, want %d", len(nodeCopies), len(wantNodeCopies))
	}
	for idx, lineIdx := range nodeCopies {
		if pins[idx] != pins[0] {
			return fmt.Errorf("Node COPY --from lines use different image pins: %q vs %q", pins[0], pins[idx])
		}
		if lineIdx < lastFrom || lines[lineIdx] != "COPY --from="+pins[0]+wantNodeCopies[idx] {
			return fmt.Errorf("line %d: unexpected Node COPY %q", lineIdx+1, lines[lineIdx])
		}
	}
	lastCopy := nodeCopies[len(nodeCopies)-1]
	if lastFrom < 0 || pathLine < lastFrom || checkLine < pathLine || checkLine < lastCopy || checkLine > userLine {
		return fmt.Errorf("%s at line %d; want after final FROM (%d), Node COPY (%d) and ENV PATH (%d), before USER (%d)",
			nodeVersionGuard, checkLine+1, lastFrom+1, lastCopy+1, pathLine+1, userLine+1)
	}
	if !hasBlockBetween(lines, wantNodeLinks, lastCopy, checkLine) {
		return fmt.Errorf("missing or misordered Node symlinks block between line %d and %d", lastCopy+1, checkLine+1)
	}
	return nil
}

func hasBlockBetween(lines, block []string, after, before int) bool {
	for i := after + 1; i >= 0 && i+len(block) <= before && i+len(block) <= len(lines); i++ {
		if strings.Join(lines[i:i+len(block)], "\n") == strings.Join(block, "\n") {
			return true
		}
	}
	return false
}
