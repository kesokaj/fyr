package dream

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// proc is one fake /proc entry: its real uid, its argv and its exe link.
type proc struct {
	uid  int
	args []string
	exe  string
}

// fakeProc points procRoot at a tree holding procs (pids from 100), a
// non-numeric entry and a process whose status cannot be read.
func fakeProc(t *testing.T, procs ...proc) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "self"), 0o755)
	os.WriteFile(filepath.Join(root, "self", "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "99"), 0o755) // gone mid-read: no status, no cmdline
	os.WriteFile(filepath.Join(root, "99", "cmdline"), []byte("agy\x00"), 0o644)
	for i, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(100+i))
		os.MkdirAll(dir, 0o755)
		// The effective uid differs from the real one: only the first field counts.
		status := "Name:\tx\nUid:\t" + strconv.Itoa(p.uid) + "\t4242\t4242\t4242\n"
		os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o644)
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(p.args, "\x00")+"\x00"), 0o644)
		if p.exe != "" {
			os.Symlink(p.exe, filepath.Join(dir, "exe"))
		}
	}
	old := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = old })
}

func TestAgySessionLinux(t *testing.T) {
	const me = 1000
	cases := []struct {
		name  string
		procs []proc
		want  bool
	}{
		{"an interactive agy", []proc{{me, []string{"/home/u/.local/bin/agy"}, ""}}, true},
		{"an interactive agy with flags", []proc{{me, []string{"agy", "--model", "m", "-i", "hello"}, ""}}, true},
		{"agy -p, the dream's own child", []proc{{me, []string{"agy", "--mode", "plan", "-p", ""}, ""}}, false},
		{"agy --print=x", []proc{{me, []string{"agy", "--print=x"}, ""}}, false},
		{"agy -prompt x", []proc{{me, []string{"agy", "-prompt", "x"}, ""}}, false},
		{"agy remote-control start", []proc{{me, []string{"agy", "remote-control", "start"}, ""}}, false},
		{"agy mcp list", []proc{{me, []string{"agy", "mcp", "list"}, ""}}, false},
		{"agy replaced by agy update", []proc{{me, []string{"agy-old"}, "/home/u/.local/bin/agy (deleted)"}}, true},
		{"another uid's agy", []proc{{me + 1, []string{"agy"}, ""}}, false},
		{"a node process", []proc{{me, []string{"node", "agy.js"}, "/usr/bin/node"}}, false},
		{"one session among others", []proc{{me + 1, []string{"agy"}, ""}, {me, []string{"agy", "-p", "x"}, ""}, {me, []string{"agy"}, ""}}, true},
		{"none", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fakeProc(t, c.procs...)
			got, err := agySession("linux", me)
			if err != nil || got != c.want {
				t.Errorf("agySession = %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestAgySessionErrors(t *testing.T) {
	old := procRoot
	procRoot = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { procRoot = old })
	if _, err := agySession("linux", 1000); err == nil {
		t.Error("an unreadable /proc was no error")
	}
	if _, err := agySession("plan9", 1000); err == nil {
		t.Error("a system with no check was no error")
	}
}

func TestAgySessionMac(t *testing.T) {
	old := psArgs
	t.Cleanup(func() { psArgs = old })
	for out, want := range map[string]bool{
		"/usr/bin/login -fp u\n-zsh\n/Users/u/.local/bin/agy --model m\n": true,
		"agy -p x\nagy remote-control start\n":                            false,
		"":                                                                false,
	} {
		psArgs = func(int) ([]byte, error) { return []byte(out), nil }
		if got, err := agySession("darwin", 501); err != nil || got != want {
			t.Errorf("ps %q: %v, %v; want %v", out, got, err, want)
		}
	}
	psArgs = func(int) ([]byte, error) { return nil, errors.New("no ps") }
	if _, err := agySession("darwin", 501); err == nil {
		t.Error("a failed ps was no error")
	}
}
