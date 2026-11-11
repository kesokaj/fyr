package work

import (
	"strings"
	"testing"
)

func TestWorkerPromptDisablesSubagentsForWorkOnly(t *testing.T) {
	r := newRig(t)
	res := r.run(r.kickoff("plain-worker", "src/a.txt"))
	wantReasons(t, res)
	plainArgs := r.read("args")
	if !strings.Contains(plainArgs, "invoke_subagent") {
		t.Fatalf("work prompt lacks no-subagent rule:\n%s", plainArgs)
	}
	if strings.Contains(plainArgs, "--model") || strings.Contains(plainArgs, "gofmt") {
		t.Fatalf("work prompt leaked --model or Go-only command into args:\n%s", plainArgs)
	}
	subIdx := strings.Index(plainArgs, "invoke_subagent")
	filesIdx := strings.Index(plainArgs, "Files: src/a.txt")
	if subIdx < 0 || filesIdx < 0 || subIdx > filesIdx {
		t.Fatalf("expected worker rule before Kickoff text (sub=%d, files=%d)", subIdx, filesIdx)
	}

	rAuto := newRig(t)
	rAuto.on("nocommit", "")
	rAuto.on("draft-names", "auto-drafts.jsonl\n")
	o := rAuto.opts()
	o.Auto = true
	resAuto, err := runWith(o, rAuto.kickoff("auto-worker", ""))
	if err != nil {
		t.Fatal(err)
	}
	wantReasons(t, resAuto)
	if strings.Contains(rAuto.read("args"), "self-reviewed in fyr work worker") {
		t.Fatalf("Auto worker unexpectedly received builder execution rule:\n%s", rAuto.read("args"))
	}
}
