package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The final stage's one ENV PATH line is ImagePath, so the box's PATH for a
// container is the image's own with the sandbox's bin directory first, and
// its USER is not root (SPEC-fyr-work § --runner docker).
func TestImagePathPinned(t *testing.T) {
	lines := strings.Split(readFile(t, "../../docker/work.Dockerfile"), "\n")
	last := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "FROM ") {
			last = i
		}
	}
	if last < 0 {
		t.Fatal("no FROM line")
	}
	var path, users []string
	for i, l := range lines {
		if v, ok := strings.CutPrefix(l, "ENV PATH="); ok {
			if i < last {
				t.Fatalf("ENV PATH at line %d is in an earlier stage", i+1)
			}
			path = append(path, v)
		}
		if strings.HasPrefix(l, "USER ") && i > last {
			users = append(users, l)
		}
	}
	if len(path) != 1 || path[0] != ImagePath {
		t.Fatalf("ENV PATH lines %q, want exactly [%q]", path, ImagePath)
	}
	if !slices.Equal(users, []string{"USER 65532:65532"}) {
		t.Fatalf("the final stage's USER lines %q, want [USER 65532:65532]", users)
	}
}

// The final stage checks for tzdata as the build's root, after its ENV
// PATH and before its USER, so a base without it fails its build instead
// of running every worker in UTC whatever its TZ (workers D144).
func TestImageZoneGuard(t *testing.T) {
	const guard = "RUN test -f /usr/share/zoneinfo/Etc/UTC"
	lines := strings.Split(readFile(t, "../../docker/work.Dockerfile"), "\n")
	last, path, user := -1, -1, -1
	var at []int
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "FROM "):
			last = i
		case strings.HasPrefix(l, "ENV PATH="):
			path = i
		case strings.HasPrefix(l, "USER "):
			user = i
		case l == guard:
			at = append(at, i)
		}
	}
	if len(at) != 1 || at[0] < last || at[0] < path || at[0] > user {
		t.Fatalf("%q at lines %v; want once in the final stage (FROM at %d), after ENV PATH (%d), before USER (%d)", guard, at, last+1, path+1, user+1)
	}
}

// The build context is default-deny, and the build copies by name: the
// ignore file excludes everything, lets in only the module, the root
// package's .go files, cmd/, internal/ and the directories embed.go embeds,
// then excludes .gitignore's secret-shaped list under them; the Dockerfile
// copies exactly those paths, so even a builder that ignores the ignore file
// puts nothing outside the allow list in a layer (a secret-shaped file under
// an admitted directory is kept out by the ignore file alone). Read from embed.go and .gitignore, a sixth
// embedded directory or a new secret pattern fails here until both files
// follow.
func TestImageContextAllowList(t *testing.T) {
	emb := readFile(t, "../../embed.go")
	ms := regexp.MustCompile(`(?m)^//go:embed (.+)$`).FindAllStringSubmatch(emb, -1)
	if len(ms) == 0 {
		t.Fatal("no //go:embed line in embed.go")
	}
	dirs := []string{"cmd", "internal"}
	for _, m := range ms {
		for _, f := range strings.Fields(m[1]) {
			dirs = append(dirs, strings.TrimPrefix(f, "all:"))
		}
	}
	want := []string{"*", "!go.mod", "!go.sum", "!*.go"}
	for _, d := range dirs {
		want = append(want, "!"+d)
	}
	for _, p := range secretShapedIgnores(t) {
		want = append(want, "**/"+p)
	}
	if got := entries(readFile(t, "../../docker/work.Dockerfile.dockerignore")); !slices.Equal(got, want) {
		t.Fatalf("ignore file entries\n got %q\nwant %q", got, want)
	}
	wantCopy := []string{"COPY go.mod go.sum ./", "COPY *.go ./"}
	for _, d := range dirs {
		wantCopy = append(wantCopy, "COPY "+d+" "+d)
	}
	wantCopy = append(wantCopy,
		"COPY --from=build /out/fyr /usr/local/bin/fyr",
		"COPY --from="+nodeImagePin+" /usr/local/bin/node /usr/local/bin/node",
		"COPY --from="+nodeImagePin+" /usr/local/include/node /usr/local/include/node",
		"COPY --from="+nodeImagePin+" /usr/local/lib/node_modules /usr/local/lib/node_modules",
	)
	var gotCopy []string
	for _, l := range strings.Split(readFile(t, "../../docker/work.Dockerfile"), "\n") {
		if strings.HasPrefix(l, "COPY ") || strings.HasPrefix(l, "ADD ") {
			gotCopy = append(gotCopy, l)
		}
	}
	if !slices.Equal(gotCopy, wantCopy) {
		t.Fatalf("Dockerfile copies\n got %q\nwant %q", gotCopy, wantCopy)
	}
}

const nodeImagePin = "node:24-trixie-slim@sha256:0820eaa85e07d0490446dd01d14707e17e6108a36988cf374899cd057eb3812d"

// secretShapedIgnores is .gitignore's block under its "Secret-shaped paths"
// comment, up to the first blank line.
func secretShapedIgnores(t *testing.T) []string {
	lines := strings.Split(readFile(t, "../../.gitignore"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "# Secret-shaped paths") {
			var out []string
			for _, p := range lines[i+1:] {
				if p == "" {
					break
				}
				if !strings.HasPrefix(p, "#") {
					out = append(out, p)
				}
			}
			if len(out) == 0 {
				t.Fatal("an empty secret-shaped block in .gitignore")
			}
			return out
		}
	}
	t.Fatal("no secret-shaped block in .gitignore")
	return nil
}

// entries is an ignore file's non-blank, non-comment lines.
func entries(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestImageRaceStdlibPrebuilt pins D25: the final stage sets
// ENV GODEBUG=installgoroot=all and installs world-readable -race stdlib
// archives into $GOROOT/pkg/${GOOS}_${GOARCH}_race/*.a while purging
// /root/.cache before dropping to USER 65532:65532, and verifies Go's
// installgoroot=all target resolution behaviourally.
func TestImageRaceStdlibPrebuilt(t *testing.T) {
	const (
		envLine = "ENV GODEBUG=installgoroot=all"
		runLine = "RUN go install -race std && rm -rf /root/.cache"
	)
	lines := strings.Split(readFile(t, "../../docker/work.Dockerfile"), "\n")
	if err := checkImageRaceStdlib(lines); err != nil {
		t.Fatal(err)
	}

	// Mutation checks: missing ENV, missing cache purge, wrong order, or after USER all fail.
	withoutEnv := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return l == envLine })
	if checkImageRaceStdlib(withoutEnv) == nil {
		t.Fatal("accepted Dockerfile without ENV GODEBUG=installgoroot=all")
	}
	withoutPurge := slices.Clone(lines)
	for i, l := range withoutPurge {
		if l == runLine {
			withoutPurge[i] = "RUN go install -race std"
		}
	}
	if checkImageRaceStdlib(withoutPurge) == nil {
		t.Fatal("accepted Dockerfile without && rm -rf /root/.cache")
	}
	afterUser := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return l == runLine })
	afterUser = append(afterUser, runLine)
	if checkImageRaceStdlib(afterUser) == nil {
		t.Fatal("accepted RUN go install -race std placed after USER 65532:65532")
	}

	// Behavioural toolchain check: Go 1.20+ returns "" without installgoroot=all
	// and $GOROOT/pkg/<goos>_<goarch>_race/runtime.a with installgoroot=all.
	gorootOut, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	goroot := strings.TrimSpace(string(gorootOut))
	baseEnv := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "GODEBUG=")
	})

	unsetCmd := exec.Command("go", "list", "-race", "-f", "{{.Target}}", "runtime")
	unsetCmd.Env = append(slices.Clone(baseEnv), "GODEBUG=")
	unsetOut, err := unsetCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -race (GODEBUG=): %v\n%s", err, unsetOut)
	}
	if got := strings.TrimSpace(string(unsetOut)); got != "" {
		t.Fatalf("go list -race Target with GODEBUG= is %q, want empty", got)
	}

	setCmd := exec.Command("go", "list", "-race", "-f", "{{.Target}}", "runtime")
	setCmd.Env = append(slices.Clone(baseEnv), "GODEBUG=installgoroot=all")
	setOut, err := setCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -race (GODEBUG=installgoroot=all): %v\n%s", err, setOut)
	}
	got := strings.TrimSpace(string(setOut))
	wantPrefix := filepath.Join(goroot, "pkg") + string(filepath.Separator)
	wantSuffix := "_race" + string(filepath.Separator) + "runtime.a"
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("go list -race Target with GODEBUG=installgoroot=all = %q, want %s*...%s", got, wantPrefix, wantSuffix)
	}
}

func checkImageRaceStdlib(lines []string) error {
	const (
		envLine = "ENV GODEBUG=installgoroot=all"
		runLine = "RUN go install -race std && rm -rf /root/.cache"
	)
	lastFrom, pathAt, userAt := -1, -1, -1
	var envAt, runAt []int
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "FROM "):
			lastFrom = i
		case strings.HasPrefix(l, "ENV PATH="):
			pathAt = i
		case strings.HasPrefix(l, "USER ") && i > lastFrom:
			userAt = i
		case l == envLine:
			envAt = append(envAt, i)
		case l == runLine:
			runAt = append(runAt, i)
		}
	}
	if len(envAt) != 1 || envAt[0] <= lastFrom || envAt[0] <= pathAt || envAt[0] >= userAt {
		return fmt.Errorf("%q at lines %v; want once in final stage after ENV PATH (%d) and before USER (%d)", envLine, envAt, pathAt+1, userAt+1)
	}
	if len(runAt) != 1 || runAt[0] <= envAt[0] || runAt[0] >= userAt {
		return fmt.Errorf("%q at lines %v; want once after %q (%d) and before USER (%d)", runLine, runAt, envLine, envAt[0]+1, userAt+1)
	}
	return nil
}
