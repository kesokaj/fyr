package work

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// goEnv is the go tool's caches, read before any test moves HOME.
var goEnv []string

// TestMain drops every GIT_* variable before the package's tests run, as
// the other packages that run git in a fixture do, and reads the go tool's
// cache paths for the fyr the rig builds.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") {
			os.Unsetenv(k)
		}
	}
	for _, k := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		if out, err := exec.Command("go", "env", k).Output(); err == nil {
			goEnv = append(goEnv, k+"="+strings.TrimSpace(string(out)))
		}
	}
	code := m.Run()
	if fyrDir != "" {
		os.RemoveAll(fyrDir)
	}
	os.Exit(code)
}

var (
	fyrOnce sync.Once
	fyrDir  string
	fyrBin  string
	fyrErr  string
)

// realFyr is this checkout's fyr, built once: the fake agy writes its
// records through it, so they are the strict export the lead's next
// fyr record parses (D41).
func realFyr(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds fyr")
	}
	fyrOnce.Do(func() {
		root, err := filepath.Abs("../..")
		if err != nil {
			fyrErr = err.Error()
			return
		}
		if fyrDir, err = os.MkdirTemp("", "fyr-work-test-"); err != nil {
			fyrErr = err.Error()
			return
		}
		bin := filepath.Join(fyrDir, "fyr")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/fyr")
		cmd.Dir = root
		cmd.Env = append(append(os.Environ(), goEnv...), "GOPROXY=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			fyrErr = "go build: " + err.Error() + "\n" + string(out)
			return
		}
		fyrBin = bin
	})
	if fyrBin == "" {
		t.Fatalf("building fyr: %s", fyrErr)
	}
	return fyrBin
}
