package telemetry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kesokaj/fyr/internal/readledger"
	"github.com/kesokaj/fyr/internal/vet"
)

func TestPrintableAndOneLineErr(t *testing.T) {
	t.Parallel()
	in := "ok\x00\x1b[31m\x7f\u202e\u2028text"
	got := Printable(in)
	if strings.ContainsAny(got, "\x00\x1b\x7f\u202e\u2028") {
		t.Fatalf("Printable left unsafe control/format runes: %q", got)
	}
	if !strings.HasPrefix(got, "ok") || !strings.HasSuffix(got, "text") {
		t.Fatalf("Printable mangled safe text: %q", got)
	}
	if OneLineErr(nil) != "unknown error" {
		t.Fatalf("OneLineErr(nil) = %q", OneLineErr(nil))
	}
	longErr := errors.New("line1\n\t" + strings.Repeat("x", 150))
	one := OneLineErr(longErr)
	if strings.Contains(one, "\n") || !strings.HasSuffix(one, "...") || len(one) > 123 {
		t.Fatalf("OneLineErr long = %q (len=%d)", one, len(one))
	}
}

func TestLedgerNoticesAndSessionStartRender(t *testing.T) {
	t.Parallel()
	unch := FormatUnchangedLedgerNotice("internal/a.go", 1, 50, "abcdef123456", 2, "1-10 func A()")
	if !strings.Contains(unch, "[UNCHANGED]") || !strings.Contains(unch, "internal/a.go") {
		t.Fatalf("unexpected unchanged notice: %s", unch)
	}
	clamped := FormatClampedLedgerNotice("internal/b.go", 600, "123456abcdef", "1-20 func B()")
	if !strings.Contains(clamped, "[CLAMPED]") || !strings.Contains(clamped, "600") {
		t.Fatalf("unexpected clamped notice: %s", clamped)
	}
	mod := FormatModifiedLedgerNotice("internal/c.go", "aaaa", "bbbb", "")
	if !strings.Contains(mod, "[MODIFIED]") {
		t.Fatalf("unexpected modified notice: %s", mod)
	}

	capStr := RenderSessionStartCapsule(&SessionStartCapsule{
		Surface: "SESSION_START",
		Date:    "2026-11-28",
		Plan:    "PLAN-1: Demo",
		Status:  SessionGitSummary{Branch: "work/demo", DirtyGoCount: 2, DirtySample: []string{"a.go", "b.go"}},
	})
	if !strings.Contains(capStr, "work/demo") || !strings.Contains(capStr, "2 dirty Go") {
		t.Fatalf("unexpected session-start capsule: %s", capStr)
	}

	if hexVal, ok := ParseFenceHexToken("=====0123456789abcdef01====="); !ok || hexVal != "0123456789abcdef01" {
		t.Fatalf("ParseFenceHexToken valid failed: (%q, %v)", hexVal, ok)
	}
	if _, ok := ParseFenceHexToken("=====nothex"); ok {
		t.Fatal("ParseFenceHexToken should reject non-hex token")
	}
}

func TestBannerDirectiveAndModelResolution(t *testing.T) {
	t.Parallel()
	if d := PreInvocationDirective(vet.Result{Gate: vet.GatePassThrough}); d != "PASS_THROUGH" {
		t.Fatalf("directive = %q, want PASS_THROUGH", d)
	}
	if d := PreInvocationDirective(vet.Result{Gate: vet.GateVetFirst}); d != "VET_FIRST" {
		t.Fatalf("directive = %q, want VET_FIRST", d)
	}
	coordRes := vet.Result{
		Gate:     vet.GateEnrichAndProceed,
		Intent:   vet.IntentFeature,
		Subtasks: []vet.SubtaskSlice{{ID: "S1"}, {ID: "S2"}},
	}
	if d := PreInvocationDirective(coordRes); d != "COORDINATE_SUBTASKS" {
		t.Fatalf("directive = %q, want COORDINATE_SUBTASKS", d)
	}

	res := vet.Result{
		Gate:         vet.GateEnrichAndProceed,
		Intent:       vet.IntentTask,
		Score:        85,
		SessionModel: "claude-sonnet-4-6-high",
	}
	sysMsg, banner := BuildTurnEnvelopeWithMeta(&res, vet.SliceResult{}, vet.TurnBrainContext{}, TurnBannerMeta{SessionModelResolved: true})
	if sysMsg == "" || sysMsg != banner || res.FenceBoundary == "" {
		t.Fatalf("BuildTurnEnvelopeWithMeta produced incomplete envelope: fence=%q len=%d", res.FenceBoundary, len(sysMsg))
	}
	degraded := FormatStoreDegradedBanner(errors.New("db locked\nretry"))
	if !strings.Contains(degraded, "db locked retry") {
		t.Fatalf("unexpected degraded banner: %s", degraded)
	}

	if CleanRawModelID("auto") != "" {
		t.Fatal("auto must clean to empty")
	}
	id, _, think := ResolveModelDetails("", "claude-sonnet-4-6-high")
	if id != "claude-sonnet-4-6-high" || think != "HIGH" {
		t.Fatalf("ResolveModelDetails = (%q, %q), want high thinking", id, think)
	}
}

func TestProcessArtifactPathAtomicWriteAndProtoExtract(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	brainConv := filepath.Join(home, ".gemini", "antigravity-cli", "brain", "conv-123")
	got := ResolveProcessArtifactPathForHome(home, ConvInput{ArtifactDirectoryPath: brainConv})
	want := filepath.Join(brainConv, "fyr-process.md")
	if got != want {
		t.Fatalf("ResolveProcessArtifactPathForHome = %q, want %q", got, want)
	}
	if outside := ResolveProcessArtifactPathForHome(home, ConvInput{ArtifactDirectoryPath: t.TempDir()}); outside != "" {
		t.Fatalf("outside brain dir must be rejected, got %q", outside)
	}
	if err := WriteProcessArtifactAtomic(want, []byte("# Process\n")); err != nil {
		t.Fatalf("WriteProcessArtifactAtomic: %v", err)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "# Process\n" {
		t.Fatalf("read written artifact: %q, %v", string(data), err)
	}

	linkPath := filepath.Join(brainConv, "link-process.md")
	if err := os.Symlink(want, linkPath); err == nil {
		if err := WriteProcessArtifactAtomic(linkPath, []byte("x")); err == nil {
			t.Fatal("WriteProcessArtifactAtomic must refuse symlink target")
		}
	}

	seen := map[string]bool{}
	markOnce := func(_, _ int, key string) bool {
		if seen[key] {
			return false
		}
		seen[key] = true
		return true
	}
	est := 900_000
	state := readledger.SessionState{GenerationID: 1, TurnCount: 3, EstTokens: est}
	full1, _ := DedupContextKneeWarning(home, "conv-1", "gemini-2.5-pro", state, func(_ string, g, tn int, k string) bool {
		return markOnce(g, tn, k)
	})
	full2, _ := DedupContextKneeWarning(home, "conv-1", "gemini-2.5-pro", state, func(_ string, g, tn int, k string) bool {
		return markOnce(g, tn, k)
	})
	if !strings.Contains(full1, "[CONTEXT_KNEE_WARNING]") || full2 != "" {
		t.Fatalf("DedupContextKneeWarning dedup failed: first=%q second=%q", full1, full2)
	}

	fakeProto := func(_ []byte) []ProtoField {
		return []ProtoField{
			{Path: []int{1, 19}, Wire: 2, Bytes: []byte("auto")},
			{Path: []int{3, 28}, Wire: 2, Bytes: []byte("claude-opus-4-6")},
			{Path: []int{1, 21}, Wire: 2, Bytes: []byte("Claude Opus 4.6")},
		}
	}
	mid, disp := ExtractGenMetadataModelInfo([]byte{1}, fakeProto)
	if mid != "claude-opus-4-6" || disp != "Claude Opus 4.6" {
		t.Fatalf("ExtractGenMetadataModelInfo = (%q, %q)", mid, disp)
	}
	if mid2, disp2 := ExtractGenMetadataModelInfo([]byte{1}, nil); mid2 != "" || disp2 != "" {
		t.Fatalf("nil parser should return empty, got (%q, %q)", mid2, disp2)
	}
}
