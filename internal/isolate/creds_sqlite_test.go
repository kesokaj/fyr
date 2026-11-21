package isolate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/harness"
)

func assertSelfHostingSandbox(t *testing.T) {
	t.Helper()
	sb := t.TempDir()
	baseGH := "gh" + "p_" + "abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	baseHdr := `var hdr = "` + "-----" + "BEGIN OPENSSH " + "PRIVATE " + "KEY-----" + `"`
	basePEM := "-----" + "BEGIN RSA " + "PRIVATE " + "KEY-----\nMIIEpAIBAAKCAQEAsecretbody\n-----END RSA " + "PRIVATE " + "KEY-----"
	baseRef := `{"` + "refresh_" + `token": "x"}`
	fixture := strings.Join([]string{baseGH, baseHdr, basePEM, baseRef}, "\n") + "\n"
	put(t, filepath.Join(sb, "app", "internal", "fixture_test.go"), fixture, 0o644)
	base := scanCreds(sb)

	sdb := filepath.Join(sb, "app", ".agents", "scratch", "search.db")
	outTxt := filepath.Join(sb, "home", ".gemini", "antigravity-cli", "brain", "conv1", ".system_generated", "steps", "1", "output.txt")
	jsonl := filepath.Join(sb, "home", ".gemini", "antigravity-cli", "brain", "conv1", ".system_generated", "logs", "transcript.jsonl")
	put(t, sdb, "SQLite format 3\x00"+fixture, 0o600)
	put(t, outTxt, "1: "+baseGH+"\n2: "+baseHdr+"\n3: "+baseRef+"\n", 0o600)
	put(t, jsonl, `{"out":"`+strings.ReplaceAll(basePEM, "\n", `\n`)+`"}`+"\n", 0o600)
	hex64 := strings.Repeat("ab", 32)
	putSparse(t, filepath.Join(sb, "home", ".cache", "go-build", "ab", hex64+"-d"), "\x7fELF\x02\x01\x01\x00", maxScan+4096)
	putSparse(t, filepath.Join(sb, "home", ".cache", "go-build", "cd", hex64+"-d"), "\xcf\xfa\xed\xfe\x0c\x00\x00\x01", maxScan+4096)
	if got := newCreds(base, scanCreds(sb)); len(got) != 0 {
		t.Errorf("self-hosting sandbox post-baseline artifacts flagged: %v", got)
	}

	novelAnt := "sk-" + "ant-" + strings.Repeat("Q", 24)
	novelRef := `{"` + "refresh_" + `token": "1//0gNovelSecretRefreshToken"}`
	novelKey := "-----" + "BEGIN OPENSSH " + "PRIVATE " + "KEY-----\nComment: leaked\n" + strings.Repeat("Z", 32) + "\n-----END OPENSSH " + "PRIVATE " + "KEY-----"
	assertPlantedCredsFail(t, sb, base, []plantedCred{
		{sdb, "SQLite format 3\x00" + fixture + novelAnt, "anthropic-key"},
		{outTxt, "1: " + baseRef + "\n2: " + novelRef + "\n", "refresh-token"},
		{filepath.Join(sb, "app", "leaked.pem"), novelKey, "private-key"},
		{filepath.Join(sb, "home", ".cache", "go-build", "ef", hex64+"-d"), "not-elf " + novelAnt, "anthropic-key"},
	})

	outsideELF := filepath.Join(t.TempDir(), "outside-elf")
	putSparse(t, outsideELF, "\x7fELF\x02\x01\x01\x00", maxScan+4096)
	symPath := filepath.Join(sb, "home", ".cache", "go-build", "99", hex64+"-d")
	if err := os.MkdirAll(filepath.Dir(symPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideELF, symPath); err != nil {
		t.Fatal(err)
	}
	if got := newCreds(base, scanCreds(sb)); len(got) != 1 || !strings.Contains(got[0], "larger than") {
		t.Errorf("symlinked oversized ELF in go-build: got %v, want oversized refusal", got)
	}
}

type plantedCred struct{ path, payload, wantKind string }

func assertPlantedCredsFail(t *testing.T, sb string, base map[string]credFinding, cases []plantedCred) {
	t.Helper()
	for _, tc := range cases {
		prev, err := os.ReadFile(tc.path)
		existed := err == nil
		put(t, tc.path, tc.payload, 0o600)
		got := newCreds(base, scanCreds(sb))
		if !existed {
			_ = os.Remove(tc.path)
		} else if err := os.WriteFile(tc.path, prev, 0o600); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || !strings.Contains(got[0], tc.wantKind) {
			t.Errorf("planted %s in %s: got %v, want 1 hit containing %q", tc.wantKind, tc.path, got, tc.wantKind)
		}
	}
}

func seedRepoSandbox(t *testing.T, root, bin, app, home string) []string {
	t.Helper()
	tracked, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	for _, rawRel := range bytes.Split(tracked, []byte{0}) {
		rel := string(rawRel)
		if rel == "" {
			continue
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(app, filepath.FromSlash(rel)), string(b), 0o644)
	}
	put(t, filepath.Join(app, ".gitignore"), ".agents/scratch/*\n.agents/trail.jsonl\n", 0o644)
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", app}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	pk, sshPK, rt := "-----"+"BEGIN PRIVATE "+"KEY-----", "-----"+"BEGIN OPENSSH PRIVATE "+"KEY-----", "refresh_"+"token"
	trail1 := fmt.Sprintf("Patterns (`\"%s\"`, `%s`) > 64 MiB & steps/<n>/output.txt >= 1, span `\"%s\" names <field>`, and `pasted %s up to -----END below`.\n", rt, pk, rt, sshPK)
	var ovf strings.Builder
	for i := range 80 {
		fmt.Fprintf(&ovf, "- row %02d (<n> & >= 1): PKCS#8 (`%s`) — `` `json:\"%s\"` `` or `\"%s\"` prose padding\n", i, pk, rt, rt)
	}
	trails := []string{trail1, ovf.String()}
	for i, body := range trails {
		cmd := exec.Command(bin, "record", "add", "review", "--home", home, "-C", app, "--title", fmt.Sprintf("seed-%d", i))
		cmd.Stdin = strings.NewReader(body)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("seed trail %d: %v\n%s", i, err, out)
		}
	}
	return trails
}

func assertSelfHostedRepoIndex(t *testing.T, root, bin string) {
	t.Helper()
	sb := t.TempDir()
	app, home := filepath.Join(sb, "app"), filepath.Join(sb, "home")
	trails := seedRepoSandbox(t, root, bin, app, home)
	base := scanCreds(sb)

	for _, verb := range [][]string{{"index"}, {"record", "export"}} {
		if out, err := exec.Command(bin, append(verb, "--home", home, "-C", app)...).CombinedOutput(); err != nil {
			t.Fatalf("fyr %v: %v\n%s", verb, err, out)
		}
	}
	rawDB, err := os.ReadFile(filepath.Join(home, harness.StateDir, "brain.db"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(home, harness.StateDir, "brain.db-wal"), string(rawDB), 0o600)
	outTxt := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c1", ".system_generated", "steps", "1", "output.txt")
	jsonl := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "c1", ".system_generated", "logs", "transcript.jsonl")
	writeTranscriptFixtures(t, app, outTxt, jsonl, trails)
	if got := newCreds(base, scanCreds(sb)); len(got) != 0 {
		t.Errorf("BUG-544 self-hosted repo index/transcript flagged: %v", got)
	}
	assertSelfHostedPlantedFail(t, sb, app, outTxt, jsonl, base)
	ovfLeak := exec.Command(bin, "record", "add", "review", "--home", home, "-C", app, "--title", "ovf-leak")
	ovfLeak.Stdin = strings.NewReader(strings.Repeat("pad line\n", 520) + "leaked " + "sk-" + "ant-" + strings.Repeat("V", 24) + "\n")
	if out, err := ovfLeak.CombinedOutput(); err != nil {
		t.Fatalf("seed ovf-leak: %v\n%s", err, out)
	}
	if got := newCreds(base, scanCreds(sb)); len(got) == 0 || !strings.Contains(strings.Join(got, ","), "anthropic-key") {
		t.Errorf("planted secret in SQLite overflow cell: got %v, want anthropic-key hit", got)
	}
}

func writeTranscriptFixtures(t *testing.T, app, outTxt, jsonl string, extras []string) {
	t.Helper()
	var numbered, logs strings.Builder
	chunks := append([]string{}, extras...)
	for _, rel := range []string{"internal/redact/redact_test.go", "internal/isolate/creds_test.go", "internal/isolate/creds_table_test.go", "cmd/fyr/record_body_file_test.go", "internal/eval/run_proc_test.go"} {
		raw, err := os.ReadFile(filepath.Join(app, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		chunks = append(chunks, string(raw))
	}
	for _, s := range chunks {
		for i, line := range strings.Split(s, "\n") {
			fmt.Fprintf(&numbered, "%d: %s\n", i+1, line)
		}
		enc1, _ := json.Marshal(s)
		enc2, _ := json.Marshal(string(enc1))
		fmt.Fprintf(&logs, "{\"out\":%s,\"nested\":%s}\n", enc1, enc2)
	}
	put(t, outTxt, numbered.String(), 0o600)
	put(t, jsonl, logs.String(), 0o600)
}

func assertSelfHostedPlantedFail(t *testing.T, sb, app, outTxt, jsonl string, base map[string]credFinding) {
	t.Helper()
	hdr, endHdr := "-----"+"BEGIN OPENSSH PRIVATE "+"KEY-----", "-----"+"END OPENSSH PRIVATE "+"KEY-----"
	b64, b64Plus, rt := "b3BlbnNzaC1rZXktdjEAAAAABG5vbmU=", "MIIE+gIB+ADANBg==", "refresh_"+"token"
	longBody := strings.Repeat(b64, 56)
	assertPlantedCredsFail(t, sb, base, []plantedCred{
		{filepath.Join(app, "short.pem"), "-----" + "BEGIN PRIVATE " + "KEY-----\nMIIBogIBAA==\n-----" + "END PRIVATE " + "KEY-----\n", "private-key"},
		{jsonl, fmt.Sprintf("{\"out\":[%q,\"REALKEYBODY1\",\"REALKEYBODY2\",%q]}\n", hdr, endHdr), "private-key"},
		{jsonl, fmt.Sprintf("{\"out\":[%q,%q,%q]}\n", hdr, longBody, endHdr), "private-key"},
		{jsonl, fmt.Sprintf("[%q,%q]\n", hdr, b64), "private-key"},
		{jsonl, fmt.Sprintf("[\n  %q,\n  %q\n]\n", hdr, b64Plus), "private-key"},
		{jsonl, fmt.Sprintf("{\"ts\":1,\"line\":%q}\n{\"ts\":2,\"line\":%q}\n", hdr, b64Plus), "private-key"},
		{jsonl, fmt.Sprintf("[%q,\"Comment: leaked\",%q]\n", hdr, b64), "private-key"},
		{filepath.Join(app, "concat.go"), fmt.Sprintf("var k = %q +\n\t%q\n", hdr, b64Plus), "private-key"},
		{filepath.Join(app, "crlf.pem"), hdr + "\r\n" + b64Plus + "\r\n", "private-key"},
		{jsonl, fmt.Sprintf("{\"key\":\"%s \\\" %s \\\"\"}\n", hdr, b64Plus), "private-key"},
		{filepath.Join(app, "bare.pem"), hdr + "\n", "private-key"},
		{outTxt, "`\"" + rt + "\"`\n: \"1//0gNovelSecret\"\n", "refresh-token"},
		{outTxt, "| field | `\"" + rt + "\"` | `1//0gNovelSecret` |\n", "refresh-token"},
	})
}
