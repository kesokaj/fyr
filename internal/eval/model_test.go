package eval

import (
	"fmt"
	"strings"
	"testing"
)

// TestEvalModelPlumbing verifies that Options.Model is threaded through newRow
// into the sandbox isolate.Config so agy receives --model on every session when
// set, and no --model flag when unset.
func TestEvalModelPlumbing(t *testing.T) {
	cases := []struct {
		name  string
		model string
	}{
		{name: "explicit model", model: "gemini-3-pro"},
		{name: "unset model keeps default", model: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			o := r.opts()
			o.Model = tc.model
			row := Row{ID: "m", Prompt: "turn 1", Prompt2: "turn 2", Check: "true", Grants: "named"}
			res, err := Run(t.Context(), o, []Row{row})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			r.clean(res)
			wantVerdict(t, one(t, res), StatusPass)
			if got := strings.TrimSpace(r.read("agy.n")); got != "2" {
				t.Fatalf("agy ran %q sessions, want 2", got)
			}
			for session := 1; session <= 2; session++ {
				raw := r.read(fmt.Sprintf("agy.args.%d", session))
				args := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
				assertModelArg(t, session, args, tc.model)
			}
		})
	}
}

func assertModelArg(t *testing.T, session int, args []string, want string) {
	t.Helper()
	var got []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--model" {
			if i+1 >= len(args) {
				t.Fatalf("session %d: dangling --model flag in argv %q", session, args)
			}
			got = append(got, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(args[i], "--model=") {
			got = append(got, strings.TrimPrefix(args[i], "--model="))
		}
	}
	if want == "" {
		if len(got) != 0 {
			t.Errorf("session %d: unexpected --model %q in argv %q", session, got, args)
		}
		return
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("session %d: --model values = %q, want [%q] (argv=%q)", session, got, want, args)
	}
}
