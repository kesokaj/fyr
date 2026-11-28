package harness

import (
	"strings"
	"testing"
)

// The names a v3.0.0 release publishes and a release binary fetches: one
// asset set, one repository (plan fyr D5a, Interfaces § The crossing).
func TestReleaseNames(t *testing.T) {
	t.Parallel()
	if got := assetName("linux", "amd64"); got != "fyr-linux-amd64" {
		t.Errorf("assetName = %s, want fyr-linux-amd64", got)
	}
	if ReleaseRepo != "github.com/kesokaj/fyr" {
		t.Errorf("ReleaseRepo = %s, want github.com/kesokaj/fyr", ReleaseRepo)
	}
	for sums, want := range map[string]string{
		"# fyr v3.0.0\n":             "v3.0.0",
		"#fyr v3.0.0 \n":             "v3.0.0",
		"# styrman-harness v3.0.0\n": "",
		"# fyrx v3.0.0\n":            "",
	} {
		got := ""
		if m := sumsVersionMarker.FindStringSubmatch(sums); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("marker of %q = %q, want %q", sums, got, want)
		}
	}
}

func TestParseSums(t *testing.T) {
	t.Parallel()
	line := strings.Repeat("a", 64) + "  fyr-linux-amd64\n"
	tag, err := parseSums([]byte("# fyr v1.2.3\n"+line), "release v1.2.3")
	if err != nil || tag != "v1.2.3" {
		t.Fatalf("parseSums valid = (%q, %v), want (v1.2.3, nil)", tag, err)
	}
	if _, err := parseSums([]byte(line), "release v1.2.3"); err == nil || !strings.Contains(err.Error(), "names no release version") {
		t.Errorf("parseSums missing marker = %v, want missing-marker refusal", err)
	}
	if _, err := parseSums([]byte("# fyr v1.2.3\n"+line+"# fyr v9.9.9\n"), "release v1.2.3"); err == nil || !strings.Contains(err.Error(), "two releases") {
		t.Errorf("parseSums two markers = %v, want two-releases refusal", err)
	}
}

func TestChecksumFor(t *testing.T) {
	t.Parallel()
	sums := "# fyr v1.2.3\n" +
		strings.Repeat("a", 64) + "  fyr-linux-amd64\n" +
		strings.Repeat("B", 64) + " *fyr-darwin-arm64\n" +
		"not-a-line\n" +
		"deadbeef  fyr-linux-arm64\n" +
		strings.Repeat("c", 64) + "  twice\n" +
		strings.Repeat("d", 64) + "  twice\n" +
		strings.Repeat("e", 64) + "  same\n" +
		strings.Repeat("e", 64) + "  same\n"
	for _, tc := range []struct {
		name, want, wantErr string
	}{
		{name: "fyr-linux-amd64", want: strings.Repeat("a", 64)},
		// A binary-mode '*' and an upper-case hash are both sha256sum's own
		// spellings.
		{name: "fyr-darwin-arm64", want: strings.Repeat("b", 64)},
		{name: "fyr-linux-arm64", wantErr: "does not carry a SHA-256"},
		{name: "fyr-windows-amd64", wantErr: "no line names"},
		// A file that says two things about one name is a reason to stop, not
		// to take the first.
		{name: "twice", wantErr: "two different hashes"},
		{name: "same", want: strings.Repeat("e", 64)},
	} {
		got, err := checksumFor(sums, tc.name)
		switch {
		case tc.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("checksumFor(%q) err = %v, want %q", tc.name, err, tc.wantErr)
			}
		case err != nil:
			t.Errorf("checksumFor(%q): %v", tc.name, err)
		case got != tc.want:
			t.Errorf("checksumFor(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCompareRelease(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.1", "v1.0.0", 1},
		{"v1.1.0", "v1.0.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"v1.0.0", "v1.0.1", -1},
		{"v1.9.0", "v1.10.0", -1}, // not a string comparison
		{"v0.9.0", "v1.0.0", -1},
	} {
		if got := compareRelease(tc.a, tc.b); got != tc.want {
			t.Errorf("compareRelease(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
