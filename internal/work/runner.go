package work

import (
	"fmt"
	"strings"
)

// The runners of --runner (SPEC-fyr-work § --runner): auto, the verb's
// default, is docker when the docker preflight passes, else the host,
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

// Check is fyr work --check: the runner o's flags pick and why, read only:
// no image is built and no sandbox, network or slot is made. A missing or
// stale default image that a run builds first (canBuildImage) is predicted,
// not built (buildPredict, ADR-504a): when the build's own checks
// (predictBuild) and every other refusal pass for the architecture the
// build gives, it reads as docker, the reason naming the image a run
// builds first (builtFirst); a build that refuses there refuses Check in
// the run's words, and a run whose build then fails is refused, or under
// auto the host, saying why. RunnerAuto is ChooseRunner with that
// preflight; RunnerDocker returns its refusal as the error; RunnerHost and
// "" are the host, o.RunnerReason or "--runner host".
func Check(o Options) (runner, reason string, err error) {
	o.build = buildPredict
	var p prepared
	switch o.Runner {
	case RunnerAuto:
		if runner, reason = ChooseRunner(&o, p.dockerPreflight); runner == RunnerDocker {
			reason = builtFirst(p.toBuild)
		}
		return runner, reason, nil
	case RunnerDocker:
		if err := p.dockerPreflight(o); err != nil {
			return "", "", err
		}
		return RunnerDocker, builtFirst(p.toBuild), nil
	case RunnerHost, "":
		return RunnerHost, hostReason(o.RunnerReason), nil
	}
	return "", "", CheckRunner(o.Runner)
}

// builtFirst is Check's reason for the docker runner: the image a run
// builds first, or "" when there is none to build.
func builtFirst(image string) string {
	if image == "" {
		return ""
	}
	return image + " will be built first"
}

// RunnerLabel is "runner docker" for RunnerDocker, with Check's reason in
// parentheses when there is one (builtFirst; a run's docker reason is ""),
// and "runner host (<reason>)" for the host runner (default "--runner
// host").
func RunnerLabel(runner, reason string) string {
	if runner == RunnerDocker {
		if r := trimDockerErr(reason); r != "" {
			return fmt.Sprintf("runner %s (%s)", RunnerDocker, r)
		}
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
