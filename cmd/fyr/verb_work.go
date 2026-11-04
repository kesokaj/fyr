package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kesokaj/fyr/internal/harness"
	"github.com/kesokaj/fyr/internal/sandbox"
	"github.com/kesokaj/fyr/internal/search"
	"github.com/kesokaj/fyr/internal/trail"
	"github.com/kesokaj/fyr/internal/work"
)

// workSelf is the fyr each sandbox installs; a test replaces it, since the
// test binary is not a fyr.
var workSelf = os.Executable

func init() {
	verbs["work"] = verb{flags: func(c *ctx) {
		c.fs.DurationVar(&c.workTimeout, "timeout", work.DefaultTimeout, "kill the worker's agy after `D`, whole minutes, at least 10m")
		c.fs.StringVar(&c.workModel, "model", "", "the agy model `M` a worker without a Model: line runs on, checked against agy models (default: agy's own)")
		c.fs.BoolVar(&c.workKeep, "keep", false, "keep the sandbox of a fetched worker too")
		c.fs.IntVar(&c.workMax, "max-workers", work.DefaultMaxWorkers, "run at most `N` workers at once across the machine, 1 to 32")
		c.fs.IntVar(&c.workMax, "workers", work.DefaultMaxWorkers, "alias for --max-workers `N`, 1 to 32")
		c.fs.IntVar(&c.workRAM, "ram", work.DefaultRAMGB, "sandbox memory limit in `GB`, 2 to 64 (default 8)")
		c.fs.IntVar(&c.workCPU, "cpu", work.DefaultCPUs, "sandbox CPU limit in `N` cores, 1 to 16 (default 2)")
		c.fs.IntVar(&c.workDisk, "disk", work.DefaultDiskGB, "sandbox disk limit in `GB`, 2 to 100 (default 10)")
		c.fs.StringVar(&c.workKill, "kill", "", "end the running fyr work whose stamp line printed `STAMP`")
		c.fs.StringVar(&c.workRunner, "runner", work.RunnerAuto, "where each worker's session runs: `R` is auto, the default (docker when its preflight passes, else the host, saying why), host, or docker (refused when its preflight fails)")
		c.fs.StringVar(&c.workImage, "image", work.DefaultImage, "the docker runner's image `REF`, built by SPEC-fyr-work's build line")
		c.fs.StringVar(&c.workAgyLinux, "agy-linux", "", "the docker runner's linux agy `PATH` (default: ~/"+sandbox.WorkAgyRel+", or the host's agy on linux)")
		c.fs.BoolVar(&c.check, "check", false, "print the runner a run with these flags picks and why, and exit 0 (with --runner docker, its refusal and exit 2); no image is built: a missing or stale default image a run builds first reads as docker, saying so")
	}, positional: true, readsRepo: true, run: runWork}
}

// runWork serves `fyr work` for one or more Kickoff files, `config`, --check,
// or --kill (SPEC-fyr-work, plan PLAN-2026-10-29-workers Phases 3-4). Every
// refusal exits 2 before anything is made (D34).
func runWork(c *ctx) error {
	files := c.fs.Args()
	if c.workKill != "" {
		return c.workKillRun(files)
	}
	if c.check {
		return c.workCheckRun(files)
	}
	if len(files) > 0 && files[0] == "config" {
		return c.workConfigRun(files[1:])
	}
	maxW, ram, cpu, disk, imageSet, err := c.workResourceFlags()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return usageErr(c.out, "work", "")
	}
	if err := c.workRunnerFlags(imageSet); err != nil {
		return err
	}
	if c.workTimeout <= 0 {
		return c.workRefuse("--timeout %s: give whole minutes, at least 10m", c.workTimeout)
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	_, root, err := trail.Roots(wd)
	if err != nil {
		return c.workRefuse("%s is not a git checkout", wd)
	}
	src, err := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	if err != nil {
		return c.workRefuse("%v", err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		return c.workRefuse("no home directory: %v", err)
	}
	self, err := workSelf()
	if err != nil {
		return c.workRefuse("the running fyr: %v", err)
	}
	return c.workRun(work.Options{Root: root, Home: c.home, RealHome: realHome, Self: self, Repo: src.Dir,
		Timeout: c.workTimeout, Model: c.workModel, Keep: c.workKeep, MaxWorkers: maxW,
		RAMGB: ram, CPUs: cpu, DiskGB: disk, Out: c.out,
		Runner: c.workRunner, Image: c.workImage, AgyLinux: c.workAgyLinux, Version: buildVersion()}, files)
}

// workRun runs the files under the verb's signals and maps the results to
// the exit status: 0 every worker passed, 1 any failed, 2 a refusal.
func (c *ctx) workRun(o work.Options, files []string) error {
	// A hangup too, as eval, and a quit: either default would kill fyr
	// before the token link is removed, and agy, in its own process group,
	// would run on with the wide grant (the Phase 3 security review).
	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer stop()
	res, err := work.Run(sig, o, files...)
	if err != nil {
		return c.workRefuse("%v", err)
	}
	for _, r := range res {
		if r.Verdict != "PASS" {
			return exitCode(1)
		}
	}
	return nil
}

// workRunnerFlags is the runner refusals a run and --check share: --runner
// is auto, host or docker, and --image and --agy-linux, the docker runner's,
// are refused with --runner host, which never uses them; --runner auto may
// use them, so it takes them (ADR-504).
func (c *ctx) workRunnerFlags(imageSet bool) error {
	if err := work.CheckRunner(c.workRunner); err != nil {
		return c.workRefuse("%v", err)
	}
	if c.workRunner == work.RunnerHost && (c.workAgyLinux != "" || imageSet) {
		return c.workRefuse("--image and --agy-linux need --runner %s or %s", work.RunnerAuto, work.RunnerDocker)
	}
	return nil
}

// workCheckRun is --check: work.Check with the flags a run would pass. It
// prints work: runner docker, work: runner docker (<image> will be built
// first) or work: runner host (<reason>), the runner a run picks, and exits
// 0, building no image and making no sandbox, network or slot: a missing or
// stale default image a run builds first is predicted, not built
// (ADR-504a); --runner docker never falls back: its refusal, and exit 2.
func (c *ctx) workCheckRun(files []string) error {
	_, _, _, _, imageSet, err := c.workResourceFlags()
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return c.workRefuse("--check takes no Kickoff file")
	}
	if err := c.workRunnerFlags(imageSet); err != nil {
		return err
	}
	if _, err := work.LoadConfig(c.home); err != nil {
		return c.workRefuse("%v", err)
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		return c.workRefuse("no home directory: %v", err)
	}
	var root string
	if wd, err := os.Getwd(); err == nil {
		_, root, _ = trail.Roots(wd)
	}
	src, _ := harness.ResolveSource(c.repoFlag, c.home, buildVersion())
	got, reason, err := work.Check(work.Options{
		Root: root, Home: c.home, RealHome: realHome, Repo: src.Dir, Runner: c.workRunner,
		Image: c.workImage, AgyLinux: c.workAgyLinux, Version: buildVersion(),
	})
	if err != nil {
		return c.workRefuse("%v", err)
	}
	fmt.Fprintln(c.out, search.Printable("work: "+work.RunnerLabel(got, reason)))
	return nil
}

// workKillRun is --kill: SIGTERM to the run the stamp names, which then
// prints its own result contract. It takes no Kickoff file.
func (c *ctx) workKillRun(files []string) error {
	if c.check {
		return c.workRefuse("--check and --kill cannot be used together")
	}
	if len(files) > 0 {
		return c.workRefuse("--kill takes a stamp and no Kickoff file")
	}
	pid, err := work.Kill(c.home, c.workKill)
	if err != nil {
		return c.workRefuse("%v", err)
	}
	fmt.Fprintln(c.out, search.Printable(fmt.Sprintf("work: SIGTERM sent to %d, the run %s; it prints its result contract", pid, c.workKill)))
	return nil
}

// workRefuse prints a work refusal and returns exit 2.
func (c *ctx) workRefuse(format string, a ...any) error {
	fmt.Fprintln(c.out, search.Printable("work: "+fmt.Sprintf(format, a...)))
	return exitCode(2)
}

func (c *ctx) workConfigVisited() bool {
	visited := false
	c.fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "max-workers", "workers", "ram", "cpu", "disk":
			visited = true
		}
	})
	return visited
}

func (c *ctx) workResourceFlags() (maxW, ram, cpu, disk int, imageSet bool, err error) {
	seen := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	imageSet = seen["image"]
	cfg := work.DefaultConfig()
	if seen["max-workers"] || seen["workers"] {
		maxW, cfg.Workers = c.workMax, c.workMax
	}
	if seen["ram"] {
		ram, cfg.RAMGB = c.workRAM, c.workRAM
	}
	if seen["cpu"] {
		cpu, cfg.CPUs = c.workCPU, c.workCPU
	}
	if seen["disk"] {
		disk, cfg.DiskGB = c.workDisk, c.workDisk
	}
	if verr := cfg.Validate(); verr != nil {
		return 0, 0, 0, 0, false, c.workRefuse("%v", verr)
	}
	return maxW, ram, cpu, disk, imageSet, nil
}

func (c *ctx) workConfigRun(rest []string) error {
	if len(rest) > 0 {
		return c.workRefuse("config takes no arguments: %q", rest)
	}
	seen := map[string]bool{}
	c.fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	for k := range seen {
		switch k {
		case "home", "repo", "max-workers", "workers", "ram", "cpu", "disk":
		default:
			return c.workRefuse("config takes only --workers (--max-workers), --ram, --cpu and --disk")
		}
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		return c.workRefuse("no home directory: %v", err)
	}
	if err := sandbox.CheckRoot(realHome, "", sandbox.Root(c.home, "work")); err != nil {
		return c.workRefuse("%v", err)
	}
	cfg, err := work.LoadConfig(c.home)
	if err != nil {
		return c.workRefuse("%v", err)
	}
	if seen["max-workers"] || seen["workers"] {
		cfg.Workers = c.workMax
	}
	if seen["ram"] {
		cfg.RAMGB = c.workRAM
	}
	if seen["cpu"] {
		cfg.CPUs = c.workCPU
	}
	if seen["disk"] {
		cfg.DiskGB = c.workDisk
	}
	if c.workConfigVisited() {
		if err := work.SaveConfig(c.home, cfg); err != nil {
			return c.workRefuse("%v", err)
		}
	}
	fmt.Fprintln(c.out, search.Printable(cfg.String()))
	return nil
}
