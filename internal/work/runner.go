package work

import (
	"fmt"
	"strings"
)

// The runners of --runner (SPEC-fyr-work § --runner): auto, the verb's
// default, is docker when DockerPreflight passes and the host otherwise,
// saying why (PLAN-2026-11-02-1806-fyr-speed item 14, D5, ADR-504); host and
// docker are the explicit choices, and an explicit docker refuses rather
// than fall back. Options.Runner "" is the host, for a library caller.
const (
	RunnerAuto   = "auto"
	RunnerHost   = "host"
	RunnerDocker = "docker"
)

// CheckRunner refuses a --runner value that is none of the three.
func CheckRunner(r string) error {
	switch r {
	case RunnerAuto, RunnerHost, RunnerDocker:
		return nil
	}
	return fmt.Errorf("--runner %q: give %s, %s or %s", r, RunnerAuto, RunnerHost, RunnerDocker)
}

// ChooseRunner is the one runner choice of fyr work and fyr auto (D5): for
// o.Runner RunnerAuto it is RunnerDocker when preflight passes, and
// RunnerHost with the refusal as the reason when it does not, set on
// o.Runner and o.RunnerReason and returned; preflight nil is
// DockerPreflight. Any other o.Runner is returned as it is and preflight is
// never called, so an explicit --runner docker refuses rather than fall
// back.
func ChooseRunner(o *Options, preflight func(Options) error) (runner, reason string) {
	if o.Runner != RunnerAuto {
		return o.Runner, o.RunnerReason
	}
	if preflight == nil {
		preflight = DockerPreflight
	}
	if err := preflight(*o); err != nil {
		if reason = trimDockerErr(err.Error()); reason == "" {
			reason = "the docker preflight failed"
		}
		o.Runner, o.RunnerReason = RunnerHost, reason
		return RunnerHost, reason
	}
	o.Runner, o.RunnerReason = RunnerDocker, ""
	return RunnerDocker, ""
}

// Check is fyr work --check: the runner o's flags pick and, for the host,
// why, read only: no image is built and no sandbox, network or slot is
// made, so a missing or stale default image that a run may build first
// (canBuildImage) reads as host. RunnerAuto is ChooseRunner with
// DockerPreflight; RunnerDocker returns DockerPreflight's refusal as the
// error; RunnerHost and "" are the host, o.RunnerReason or "--runner host".
func Check(o Options) (runner, reason string, err error) {
	o.autoBuild = false
	switch o.Runner {
	case RunnerAuto:
		runner, reason = ChooseRunner(&o, DockerPreflight)
		return runner, reason, nil
	case RunnerDocker:
		if err := DockerPreflight(o); err != nil {
			return "", "", err
		}
		return RunnerDocker, "", nil
	case RunnerHost, "":
		return RunnerHost, hostReason(o.RunnerReason), nil
	}
	return "", "", CheckRunner(o.Runner)
}

// RunnerLabel is "runner docker" for RunnerDocker, and
// "runner host (<reason>)" for the host runner (default "--runner host").
func RunnerLabel(runner, reason string) string {
	if runner == RunnerDocker {
		return "runner " + RunnerDocker
	}
	return fmt.Sprintf("runner %s (%s)", RunnerHost, hostReason(reason))
}

// hostReason is why a run is on the host, on one line: the reason given,
// or "--runner host" when there is none.
func hostReason(reason string) string {
	if r := trimDockerErr(reason); r != "" {
		return r
	}
	return "--runner " + RunnerHost
}

// trimDockerErr is a docker refusal on one line, without its
// "--runner docker: " prefix, so it reads as the reason after "host (".
func trimDockerErr(s string) string {
	return strings.TrimPrefix(strings.Join(strings.Fields(s), " "), "--runner docker: ")
}
