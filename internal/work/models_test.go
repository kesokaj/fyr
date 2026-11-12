package work

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// modelKickoff writes the Kickoff name.md with the Files: line files and,
// when model is not "", the Model: line.
func (r *rig) modelKickoff(name, files, model string) string {
	p := filepath.Join(r.kick, name+".md")
	body := "Project: " + r.lead + ". Do the phase.\nFiles: " + files + "\n"
	if model != "" {
		body += "Model: " + model + "\n"
	}
	put(r.t, p, body+"Standing approvals: commit.\n")
	return p
}

// calls is how many times the fake agy was asked for its models.
func (r *rig) modelCalls() int { return strings.Count(r.read("models-calls"), "call\n") }

// TestRunModelRefusals is refusal 21 (§ Interfaces § A model per worker):
// each refuses the run with nothing made, after one agy models call.
func TestRunModelRefusals(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(r *rig, o *Options) string
		want  string
	}{
		{"unlisted-kickoff", func(r *rig, o *Options) string { return r.modelKickoff("a", "src/a.txt", "no-such") },
			"names the model no-such, which agy models does not list: gemini-x, m-a, m-b"},
		{"unlisted-flag", func(r *rig, o *Options) string {
			o.Model = "no-such"
			return r.modelKickoff("a", "src/a.txt", "m-a")
		}, "--model no-such: agy models does not list it: gemini-x, m-a, m-b"},
		{"call-fails", func(r *rig, o *Options) string {
			r.on("models-exit", "3")
			r.on("models-err", "⠋ loading\rnot logged in") // lastLine drops the spinner
			return r.modelKickoff("a", "src/a.txt", "m-a")
		}, "the model check: agy models: exit status 3: not logged in"},
		{"over-cap", func(r *rig, o *Options) string {
			r.on("models-big", "")
			return r.modelKickoff("a", "src/a.txt", "m-a")
		}, "the model check: agy models: its output is over 64 KiB"},
		{"empty-list", func(r *rig, o *Options) string {
			r.on("models-list", "Loading…\n")
			return r.modelKickoff("a", "src/a.txt", "m-a")
		}, "the model check: agy models listed no model"},
		{"timed-out", func(r *rig, o *Options) string {
			old := modelsTimeout
			modelsTimeout = 300 * time.Millisecond
			r.t.Cleanup(func() { modelsTimeout = old })
			r.on("models-sleep", "30")
			return r.modelKickoff("a", "src/a.txt", "m-a")
		}, "the model check: agy models: no answer in 300ms"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			o := r.opts()
			file := c.setup(r, &o)
			t0 := time.Now()
			_, err := runWith(o, file)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			// The deadline kills the group, the fake's sleep too, well before
			// WaitDelay's 5s would close the pipe.
			if d := time.Since(t0); d > 3*time.Second {
				t.Errorf("the refusal took %s", d)
			}
			if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
				t.Errorf("a refusal made the sandbox root: %v", err)
			}
			if n := r.modelCalls(); n != 1 || strings.TrimSpace(r.read("models-cwd")) != "/" {
				t.Errorf("%d agy models calls in %q, want 1 in / (D125)", n, r.read("models-cwd"))
			}
			if runtime.GOOS == "linux" && r.read("models-pwd") != "PWD=/\n" {
				t.Errorf("agy models was given %q, want PWD=/ alone", r.read("models-pwd"))
			}
		})
	}
}

// TestRunModelCheckOnlyWhenNamed: a run naming no model makes no agy
// models call, and nor does one an offline refusal stops; several Kickoffs
// naming models make one, whose stderr past the cap is dropped, not
// refused, and whose leftover helper is killed with its group.
func TestRunModelCheckOnlyWhenNamed(t *testing.T) {
	r := newRig(t)
	if res := r.run(r.kickoff("plain", "src/a.txt")); res.Verdict != "PASS" || r.modelCalls() != 0 {
		t.Fatalf("no model named: %s, %d agy models calls\n%s", res.Verdict, r.modelCalls(), r.out.String())
	}
	r1 := newRig(t)
	put(t, filepath.Join(r1.lead, "new.txt"), "x")
	if _, err := runWith(r1.opts(), r1.modelKickoff("a", "src/a.txt", "m-a")); err == nil || !strings.Contains(err.Error(), "uncommitted") || r1.modelCalls() != 0 {
		t.Fatalf("a dirty tree: %v, %d agy models calls", err, r1.modelCalls())
	}
	r2 := newRig(t)
	r2.on("file-two", "pkg/b.go")
	r2.on("models-bigerr", "")
	r2.on("models-helper", "")
	res, err := Run(context.Background(), r2.opts(), r2.modelKickoff("one", "src/a.txt", "m-a"), r2.modelKickoff("two", "pkg/b.go", "m-b"))
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range res {
		wantReasons(t, x)
	}
	if n := r2.modelCalls(); n != 1 {
		t.Errorf("%d agy models calls for two Kickoffs, want 1", n)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(r2.read("models-helper-pid")))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; syscall.Kill(pid, 0) == nil; i++ {
		if i == 20 {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the helper %d agy models left still runs", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestRunModelCheckEnds: the run's end stops the agy models call at once,
// its sleeping child too, and the run is refused with nothing made.
func TestRunModelCheckEnds(t *testing.T) {
	r := newRig(t)
	r.on("models-sleep", "30")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	_, err := first(Run(ctx, r.opts(), r.modelKickoff("a", "src/a.txt", "m-a")))
	if err == nil || !strings.Contains(err.Error(), "the model check: agy models: the run ended before it answered") {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(t0); d > 3*time.Second {
		t.Errorf("the refusal took %s", d)
	}
	if _, err := os.Lstat(sandbox.Root(r.home, "work")); !os.IsNotExist(err) {
		t.Errorf("a refusal made the sandbox root: %v", err)
	}
}

// TestRunModelPerWorker: a Kickoff's Model: overrides --model for its
// worker alone; the model reaches agy and the result line, and a worker
// with no model anywhere passes none and prints model default.
func TestRunModelPerWorker(t *testing.T) {
	r := newRig(t)
	r.on("file-two", "pkg/b.go")
	o := r.opts()
	o.Model = "m-b"
	res, err := Run(context.Background(), o, r.modelKickoff("one", "src/a.txt", "m-a"), r.modelKickoff("two", "pkg/b.go", ""))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"m-a", "m-b"} {
		wantReasons(t, res[i])
		name := res[i].Name
		if !strings.Contains(r.read("args-"+name), "--model\n"+want+"\n") || res[i].Model != want {
			t.Errorf("%s: model %q, agy args:\n%s", name, res[i].Model, r.read("args-"+name))
		}
		if !strings.Contains(r.out.String(), "\nwork: "+name+"  PASS  agy exit 0  ") || !strings.Contains(res[i].Block()[0], "s  model "+want+"  work/"+name+" ") {
			t.Errorf("%s: result line %q", name, res[i].Block()[0])
		}
	}
	r2 := newRig(t)
	res2 := r2.run(r2.kickoff("plain", "src/a.txt"))
	if strings.Contains(r2.read("args"), "--model") || res2.Model != "" || !strings.Contains(res2.Block()[0], "s  model default  work/plain ") {
		t.Errorf("no model: %q, args:\n%s", res2.Block()[0], r2.read("args"))
	}
}

// TestRunModelCheckDocker: with --runner docker the check runs the host's
// agy, so a named model with none on PATH is refused with nothing made; with
// one on PATH again, as TestRunSweepQuiet does (D129), the check runs and
// the worker passes.
func TestRunModelCheckDocker(t *testing.T) {
	linuxOnly(t)
	r, o := dockerRig(t)
	if _, err := runWith(o, r.modelKickoff("a", "src/a.txt", "m-a")); err == nil || !strings.Contains(err.Error(), "the model check runs the host's agy: agy not found on PATH") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Lstat(sandbox.Root(o.Home, "work")); !os.IsNotExist(err) {
		t.Errorf("a refusal made the sandbox root: %v", err)
	}
	r2, o2 := dockerRig(t)
	// A copy: the fake docker still runs the one off PATH in the container.
	b, err := os.ReadFile(filepath.Join(r2.tmp, "agy-off-path", "agy"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(r2.bin, "agy"), string(b))
	os.Chmod(filepath.Join(r2.bin, "agy"), 0o755)
	res, err := runWith(o2, r2.modelKickoff("a", "src/a.txt", "m-a"))
	if err != nil || res.Verdict != "PASS" || r2.modelCalls() != 1 || !strings.Contains(res.Block()[0], "  model m-a  ") {
		t.Fatalf("docker with a host agy: %v %+v, %d calls\n%s", err, res, r2.modelCalls(), r2.out.String())
	}
}

func TestResolveRoleModel(t *testing.T) {
	ctx := context.Background()
	oldUsage := usageProbeFunc
	oldList := listModelsFunc
	t.Cleanup(func() {
		usageProbeFunc = oldUsage
		listModelsFunc = oldList
	})

	listModelsFunc = func(ctx context.Context, agy string) ([]string, error) {
		return []string{
			"gemini-4-carbon-high",
			"gemini-3.8-flash-high",
			"claude-opus-5-5-high",
			"claude-sonnet-5-5-high",
		}, nil
	}

	// 1. Builder routes to listed Gemini models (Carbon for pro, Flash when no Carbon medium/low).
	m, note, err := ResolveRoleModel(ctx, "agy", "builder", "pro", "")
	if err != nil || m != "gemini-4-carbon-high" || !strings.Contains(note, "Gemini") {
		t.Fatalf("builder pro: got m=%q note=%q err=%v", m, note, err)
	}
	mFlash, _, err := ResolveRoleModel(ctx, "agy", "builder", "flash", "")
	if err != nil || mFlash != "gemini-3.8-flash-high" {
		t.Fatalf("builder flash: got %q err=%v, want gemini-3.8-flash-high", mFlash, err)
	}

	// 2. Judge/verifier/reviewer routes to listed Anthropic models when Claude pool quota >= 30% 5h and >= 20% weekly.
	usageProbeFunc = func(ctx context.Context, agy string) (string, error) {
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t45%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\t60%\tx\n", nil
	}
	jModel, jNote, err := ResolveRoleModel(ctx, "agy", "judge", "pro", "")
	if err != nil || jModel != "claude-opus-5-5-high" || !strings.Contains(jNote, "Anthropic") {
		t.Fatalf("judge healthy quota: got m=%q note=%q err=%v", jModel, jNote, err)
	}
	vModel, _, err := ResolveRoleModel(ctx, "agy", "verifier", "sonnet", "")
	if err != nil || vModel != "claude-sonnet-5-5-high" {
		t.Fatalf("verifier sonnet healthy quota: got %q err=%v, want claude-sonnet-5-5-high", vModel, err)
	}

	// 3. Judge falls back to listed Gemini when Claude pool weekly < 20% or 5h < 30% (or disabled).
	usageProbeFunc = func(ctx context.Context, agy string) (string, error) {
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t15%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\tdisabled\tx\n", nil
	}
	fbModel, fbNote, err := ResolveRoleModel(ctx, "agy", "judge", "pro", "")
	if err != nil || fbModel != "gemini-4-carbon-high" || !strings.Contains(fbNote, "fallback to Gemini") {
		t.Fatalf("judge low quota fallback: got m=%q note=%q err=%v", fbModel, fbNote, err)
	}
	fbFlash, fbFlashNote, err := ResolveRoleModel(ctx, "agy", "reviewer", "flash", "")
	if err != nil || fbFlash != "gemini-3.8-flash-high" || !strings.Contains(fbFlashNote, "fallback to Gemini") {
		t.Fatalf("reviewer flash low quota fallback: got m=%q note=%q err=%v", fbFlash, fbFlashNote, err)
	}

	// 4. Explicit model overrides routing.
	expModel, expNote, _ := ResolveRoleModel(ctx, "agy", "judge", "pro", "custom-model")
	if expModel != "custom-model" || !strings.Contains(expNote, "explicit") {
		t.Fatalf("explicit override: got m=%q note=%q", expModel, expNote)
	}
}

func TestResolveRoleModelPreservesFailingWorkPassUnderEvalEvidence(t *testing.T) {
	ctx := context.Background()
	oldUsage := usageProbeFunc
	oldList := listModelsFunc
	t.Cleanup(func() {
		usageProbeFunc = oldUsage
		listModelsFunc = oldList
	})

	home := t.TempDir()
	t.Setenv("HOME", home)

	parsed := ParseModelsOutput(
		"gemini-4-carbon-high\tGemini 4 Carbon (High)\n" +
			"gemini-4-carbon-medium\tGemini 4 Carbon (Medium)\n" +
			"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n",
	)
	cat := BuildCatalog(time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC), parsed, nil, map[string]ModelEvidence{
		"gemini-4-carbon-high": {WorkRuns: 16, WorkPass: 7, WorkMeanSeconds: 95},
		"gemini-4-carbon":      {EvalRuns: 35, EvalPass: 31},
		"claude-opus-5-5":      {EvalRuns: 8, EvalPass: 8},
	})
	if err := SaveCatalog(home, cat); err != nil {
		t.Fatal(err)
	}

	listModelsFunc = func(ctx context.Context, agy string) ([]string, error) {
		return []string{"gemini-4-carbon-high", "gemini-4-carbon-medium", "claude-opus-5-5-high"}, nil
	}
	usageProbeFunc = func(ctx context.Context, agy string) (string, error) {
		return "Gemini Models\tWeekly Limit Remaining\t80%\tx\n" +
			"Gemini Models\tFive Hour Limit Remaining\t90%\tx\n" +
			"Claude and GPT models\tWeekly Limit Remaining\t10%\tx\n" +
			"Claude and GPT models\tFive Hour Limit Remaining\t50%\tx\n", nil
	}

	bModel, _, err := ResolveRoleModel(ctx, "agy", "builder", "pro", "")
	if err != nil || bModel != "gemini-4-carbon-medium" {
		t.Fatalf("builder pro with 7/16 work + 31/35 eval: got %q err=%v, want gemini-4-carbon-medium", bModel, err)
	}
	jFallback, jNote, err := ResolveRoleModel(ctx, "agy", "judge", "pro", "")
	if err != nil || jFallback != "gemini-4-carbon-medium" || !strings.Contains(jNote, "fallback to Gemini") {
		t.Fatalf("judge low-quota fallback: got %q note=%q err=%v, want gemini-4-carbon-medium", jFallback, jNote, err)
	}
}
