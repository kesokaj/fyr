package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAgyEnvZone(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	byPrefix := func(env []string, pfx string) []string {
		return slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return !strings.HasPrefix(kv, pfx) })
	}
	b := New(Config{Dir: "/w/s1/w1", Zone: "Europe/Stockholm"})
	if got := byPrefix(b.agyEnv(), "TZ="); !slices.Equal(got, []string{"TZ=Europe/Stockholm"}) {
		t.Errorf("agyEnv: TZ entries %q, want [TZ=Europe/Stockholm]", got)
	}
	if got := byPrefix(b.agyEnv(), "TMPDIR="); !slices.Equal(got, []string{"TMPDIR=" + filepath.Join(b.Dir(), "tmp")}) {
		t.Errorf("agyEnv TMPDIR %q, want %s/tmp", got, b.Dir())
	}
	b = New(Config{Dir: "/w/s1/w1"})
	if got := byPrefix(b.agyEnv(), "TZ="); len(got) != 0 {
		t.Errorf("agyEnv with no Zone: TZ entries %q, want none", got)
	}
}

func TestBaseSandboxEnvDisablesNodeCompileCache(t *testing.T) {
	// Even if the host environment disables the guard, sandbox envs must emit =1.
	t.Setenv("NODE_DISABLE_COMPILE_CACHE", "0")
	byPrefix := func(env []string, pfx string) []string {
		return slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return !strings.HasPrefix(kv, pfx) })
	}
	want := []string{"NODE_DISABLE_COMPILE_CACHE=1"}

	b := New(Config{Dir: "/w/s1/w1"})
	if got := byPrefix(b.agyEnv(), "NODE_DISABLE_COMPILE_CACHE="); !slices.Equal(got, want) {
		t.Errorf("agyEnv NODE_DISABLE_COMPILE_CACHE = %q, want %q", got, want)
	}

	tmp := t.TempDir()
	for _, unconfined := range []bool{false, true} {
		cmdEnv, _, _ := buildCommandEnv(CommandOpts{
			Dir:         tmp,
			ScratchHome: filepath.Join(tmp, "home"),
			TmpDir:      filepath.Join(tmp, "tmp"),
			Unconfined:  unconfined,
		})
		if got := byPrefix(cmdEnv, "NODE_DISABLE_COMPILE_CACHE="); !slices.Equal(got, want) {
			t.Errorf("buildCommandEnv(Unconfined=%v) NODE_DISABLE_COMPILE_CACHE = %q, want %q", unconfined, got, want)
		}
	}
}

func TestAgyEnvPATHFiltering(t *testing.T) {
	base := t.TempDir()
	osTmp := filepath.Join(base, "ostmp")
	t.Setenv("TMPDIR", osTmp)

	realHome := filepath.Join(base, "realhome")
	realHomeBin := filepath.Join(realHome, ".local", "bin")
	tmpBin := filepath.Join(osTmp, "tbin")
	sysDir := filepath.Join(base, "sysbin")
	roDir := filepath.Join(osTmp, "ro-allowed-bin")
	rwDir := filepath.Join(osTmp, "rw-allowed-bin")
	boxDir := filepath.Join(osTmp, "box1")
	sbBin := filepath.Join(boxDir, "home", "bin")
	sbTools := filepath.Join(boxDir, "home", "tools")
	appBin := filepath.Join(boxDir, "app", "bin")
	boxTmpBin := filepath.Join(boxDir, "tmp", "bin")
	for _, d := range []string{realHomeBin, tmpBin, sysDir, roDir, rwDir, sbBin, sbTools, appBin, boxTmpBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	linkToHome := filepath.Join(base, "ext-home-bin")
	linkToTmp := filepath.Join(base, "ext-tmp-bin")
	if err := os.Symlink(realHomeBin, linkToHome); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmpBin, linkToTmp); err != nil {
		t.Fatal(err)
	}

	rawEntries := []string{
		realHomeBin,
		linkToHome,
		tmpBin,
		linkToTmp,
		sysDir,
		sysDir + string(filepath.Separator),
		"",
		roDir,
		rwDir,
		sbTools,
		appBin,
		boxTmpBin,
		sbBin,
	}
	if goExe, err := exec.LookPath("go"); err == nil {
		rawEntries = append(rawEntries, filepath.Dir(goExe))
	}
	rawPATH := strings.Join(rawEntries, string(os.PathListSeparator))
	t.Setenv("PATH", rawPATH)

	b := New(Config{
		Dir:      boxDir,
		RealHome: realHome,
		TestRO:   []string{roDir},
		TestRW:   []string{rwDir},
	})
	gotPATH, ok := envToMap(b.agyEnv())["PATH"]
	if !ok {
		t.Fatal("agyEnv missing PATH")
	}
	parts := filepath.SplitList(gotPATH)
	if len(parts) == 0 || parts[0] != sbBin {
		t.Fatalf("confined PATH first entry = %v, want %q first", parts, sbBin)
	}
	if gr := resolveGoRoot(); gr != "" {
		goBin := filepath.Join(gr, "bin")
		if st, err := os.Stat(goBin); err == nil && st.IsDir() {
			if !slices.Contains(parts, goBin) {
				t.Errorf("confined PATH missing GOROOT/bin %q: %v", goBin, parts)
			}
		}
	}
	for _, want := range []string{sbBin, sysDir, roDir, rwDir, sbTools, appBin, boxTmpBin} {
		if !slices.Contains(parts, want) {
			t.Errorf("confined PATH missing %q: %v", want, parts)
		}
	}
	for _, blocked := range []string{realHomeBin, linkToHome, tmpBin, linkToTmp, ""} {
		if slices.Contains(parts, blocked) {
			t.Errorf("confined PATH leaked %q: %v", blocked, parts)
		}
	}
	for _, dedup := range []string{sbBin, sysDir} {
		count := 0
		for _, p := range parts {
			if p == dedup {
				count++
			}
		}
		if count != 1 {
			t.Errorf("confined PATH entry %q appeared %d times, want 1: %v", dedup, count, parts)
		}
	}

	unconfined := New(Config{
		Dir:            boxDir,
		RealHome:       realHome,
		TestRO:         []string{roDir},
		TestRW:         []string{rwDir},
		TestUnconfined: true,
	})
	wantUnconfined := sbBin + string(os.PathListSeparator) + rawPATH
	if got := envToMap(unconfined.agyEnv())["PATH"]; got != wantUnconfined {
		t.Errorf("unconfined agyEnv PATH = %q, want %q", got, wantUnconfined)
	}
}

func TestConfinedPATHHardening(t *testing.T) {
	base := t.TempDir()
	osTmp := filepath.Join(base, "ostmp")
	t.Setenv("TMPDIR", osTmp)

	realHome := filepath.Join(base, "realhome")
	t.Setenv("HOME", realHome)
	homeBin := filepath.Join(realHome, ".local", "bin")
	tmpBin := filepath.Join(osTmp, "tbin")
	sysDir := filepath.Join(base, "sysbin")
	roDir := filepath.Join(osTmp, "ro-bin")
	linkEscapeHome := filepath.Join(roDir, "escape-home")
	linkEscapeTmp := filepath.Join(roDir, "escape-tmp")
	homeLinkToRO := filepath.Join(realHome, "link-to-ro")
	for _, d := range []string{homeBin, tmpBin, sysDir, roDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(homeBin, linkEscapeHome); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tmpBin, linkEscapeTmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(roDir, homeLinkToRO); err != nil {
		t.Fatal(err)
	}
	resRODir, err := filepath.EvalSymlinks(roDir)
	if err != nil {
		t.Fatal(err)
	}

	rawEntries := []string{
		".",
		"relative-bin",
		filepath.Join("..", "tools"),
		sysDir,
		homeBin,
		homeLinkToRO,
		linkEscapeHome,
		linkEscapeTmp,
		roDir,
	}
	if goExe, err := exec.LookPath("go"); err == nil {
		rawEntries = append(rawEntries, filepath.Dir(goExe))
	}
	rawPATH := strings.Join(rawEntries, string(os.PathListSeparator))
	t.Setenv("PATH", rawPATH)

	// Empty RealHome must still fall back to HOME / user.Current and exclude homeBin.
	got := confinedPATH("", "", []string{roDir})
	parts := filepath.SplitList(got)
	for _, blocked := range []string{".", "relative-bin", filepath.Join("..", "tools"), homeBin, homeLinkToRO, linkEscapeHome, linkEscapeTmp} {
		if slices.Contains(parts, blocked) {
			t.Errorf("confinedPATH leaked %q in %v", blocked, parts)
		}
	}
	if !slices.Contains(parts, resRODir) {
		t.Errorf("confinedPATH missing resolved %q from symlink %q: %v", resRODir, homeLinkToRO, parts)
	}
	cwd, err := os.Getwd()
	if err == nil && slices.Contains(parts, filepath.Clean(cwd)) {
		t.Errorf("confinedPATH leaked host CWD %q from '.' in PATH: %v", cwd, parts)
	}
	roIdx := slices.Index(parts, roDir)
	sysIdx := slices.Index(parts, sysDir)
	if roIdx < 0 || sysIdx < 0 {
		t.Fatalf("confinedPATH missing roDir (%d) or sysDir (%d): %v", roIdx, sysIdx, parts)
	}
	if roIdx >= sysIdx {
		t.Errorf("expected explicit roDir (%d) before sysDir (%d): %v", roIdx, sysIdx, parts)
	}
	if gr := resolveGoRoot(); gr != "" {
		goBin := filepath.Join(gr, "bin")
		if st, err := os.Stat(goBin); err == nil && st.IsDir() {
			goIdx := slices.Index(parts, goBin)
			if goIdx < 0 || goIdx <= roIdx || goIdx >= sysIdx {
				t.Errorf("expected order roDir(%d) < GOROOT/bin(%d) < sysDir(%d): %v", roIdx, goIdx, sysIdx, parts)
			}
		}
	}

	safeDirs := appendSafePATHDirs(nil, "")
	for _, blocked := range []string{".", homeBin, tmpBin, linkEscapeHome, linkEscapeTmp} {
		if slices.Contains(safeDirs, blocked) {
			t.Errorf("appendSafePATHDirs(empty realHome) leaked %q: %v", blocked, safeDirs)
		}
	}
	if cwd != "" && slices.Contains(safeDirs, filepath.Clean(cwd)) {
		t.Errorf("appendSafePATHDirs leaked host CWD %q from relative PATH: %v", cwd, safeDirs)
	}
	if !slices.Contains(safeDirs, sysDir) {
		t.Errorf("appendSafePATHDirs missing safe sysDir %q: %v", sysDir, safeDirs)
	}
}

func TestResolveGoRoot(t *testing.T) {
	t.Run("real go binary on PATH", func(t *testing.T) {
		if _, err := exec.LookPath("go"); err != nil {
			t.Skip("go binary not on PATH")
		}
		got := resolveGoRoot()
		if got == "" || !filepath.IsAbs(got) {
			t.Fatalf("resolveGoRoot() = %q, want non-empty absolute path", got)
		}
		goBin := filepath.Join(got, "bin")
		if st, err := os.Stat(goBin); err != nil || !st.IsDir() {
			t.Fatalf("resolveGoRoot()/bin = %q is not a directory: %v", goBin, err)
		}
	})

	t.Run("caches per go binary", func(t *testing.T) {
		t.Setenv("GOROOT", "")
		binDir := t.TempDir()
		wantRoot := t.TempDir()
		countFile := filepath.Join(t.TempDir(), "calls")
		script := "#!/bin/sh\necho x >> " + ShellQuote(countFile) + "\nprintf '%s\\n' " + ShellQuote(wantRoot) + "\n"
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		if got := resolveGoRoot(); got != wantRoot {
			t.Fatalf("first resolveGoRoot() = %q, want %q", got, wantRoot)
		}
		if got := resolveGoRoot(); got != wantRoot {
			t.Fatalf("second resolveGoRoot() = %q, want %q", got, wantRoot)
		}
		raw, err := os.ReadFile(countFile)
		if err != nil {
			t.Fatal(err)
		}
		if lines := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); lines != 1 {
			t.Fatalf("fake go invoked %d times, want 1 (cached)", lines)
		}
	})

	t.Run("missing go on PATH returns empty", func(t *testing.T) {
		t.Setenv("GOROOT", "")
		t.Setenv("PATH", t.TempDir())
		if got := resolveGoRoot(); got != "" {
			t.Fatalf("resolveGoRoot() without go = %q, want empty", got)
		}
	})

	t.Run("failing go returns empty", func(t *testing.T) {
		t.Setenv("GOROOT", "")
		binDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		if got := resolveGoRoot(); got != "" {
			t.Fatalf("resolveGoRoot() on exit 1 = %q, want empty", got)
		}
	})

	t.Run("oversized output over 4096 bytes returns empty", func(t *testing.T) {
		t.Setenv("GOROOT", "")
		binDir := t.TempDir()
		big := "/" + strings.Repeat("a", 5000)
		script := "#!/bin/sh\nprintf '%s\\n' " + ShellQuote(big) + "\n"
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		if got := resolveGoRoot(); got != "" {
			t.Fatalf("resolveGoRoot() on >4096B output = %q, want empty", got)
		}
	})

	t.Run("relative path output returns empty", func(t *testing.T) {
		t.Setenv("GOROOT", "")
		binDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\necho relative/goroot\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		if got := resolveGoRoot(); got != "" {
			t.Fatalf("resolveGoRoot() on relative path = %q, want empty", got)
		}
	})
}
