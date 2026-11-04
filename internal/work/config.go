package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kesokaj/fyr/internal/sandbox"
)

// Default and bound constants for worker queue and sandbox container limits
// (D10 of PLAN-2026-11-02-1806-fyr-speed, ADR-503).
const (
	DefaultRAMGB = 8
	MinRAMGB     = 2
	MaxRAMGB     = 64

	DefaultCPUs = 2
	MinCPUs     = 1
	MaxCPUs     = 16

	DefaultDiskGB = 10
	MinDiskGB     = 2
	MaxDiskGB     = 100

	ReasonDiskLimit = "sandbox disk limit exceeded"
)

// Config holds the persisted worker queue and sandbox resource limits stored at
// <home>/.config/fyr/work/config.json (D10, ADR-503).
type Config struct {
	Workers int `json:"workers"`
	RAMGB   int `json:"ram_gb"`
	CPUs    int `json:"cpus"`
	DiskGB  int `json:"disk_gb"`
}

// DefaultConfig returns the default worker queue and container resource limits.
func DefaultConfig() Config {
	return Config{
		Workers: DefaultMaxWorkers,
		RAMGB:   DefaultRAMGB,
		CPUs:    DefaultCPUs,
		DiskGB:  DefaultDiskGB,
	}
}

// ConfigPath returns the path to work's config.json under home.
func ConfigPath(home string) string {
	return filepath.Join(home, filepath.FromSlash(sandbox.WorkConfigRel))
}

// Validate checks that every field of c lies within its allowed bounds.
func (c Config) Validate() error {
	if c.Workers < 1 || c.Workers > MaxMaxWorkers {
		return fmt.Errorf("--max-workers %d: give 1 to %d", c.Workers, MaxMaxWorkers)
	}
	if c.RAMGB < MinRAMGB || c.RAMGB > MaxRAMGB {
		return fmt.Errorf("--ram %d: give %d to %d", c.RAMGB, MinRAMGB, MaxRAMGB)
	}
	if c.CPUs < MinCPUs || c.CPUs > MaxCPUs {
		return fmt.Errorf("--cpu %d: give %d to %d", c.CPUs, MinCPUs, MaxCPUs)
	}
	if c.DiskGB < MinDiskGB || c.DiskGB > MaxDiskGB {
		return fmt.Errorf("--disk %d: give %d to %d", c.DiskGB, MinDiskGB, MaxDiskGB)
	}
	return nil
}

// String formats c for `fyr work config`.
func (c Config) String() string {
	return fmt.Sprintf("work config: workers=%d  ram=%dg  cpu=%d  disk=%dg", c.Workers, c.RAMGB, c.CPUs, c.DiskGB)
}

// LoadConfig reads ConfigPath(home), returning DefaultConfig() when the file
// does not exist, and refusing symlinks or out-of-range values.
func LoadConfig(home string) (Config, error) {
	cfg := DefaultConfig()
	if home == "" {
		return cfg, nil
	}
	p := ConfigPath(home)
	st, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if !st.Mode().IsRegular() {
		return cfg, fmt.Errorf("%s is not a regular file", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %v", p, err)
	}
	return cfg, cfg.Validate()
}

// SaveConfig validates cfg and writes it atomically to ConfigPath(home) with
// 0600 permissions inside a 0700 work/ directory.
func SaveConfig(home string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	workDir, err := sandbox.EnsureWorkDir(home)
	if err != nil {
		return err
	}
	p := ConfigPath(home)
	if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", p)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(workDir, ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, p); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// SandboxRoot is where work makes its sandboxes for project root under home:
// <home>/.config/fyr/sandbox/<project> (D9 of PLAN-2026-11-02-1806-fyr-speed).
func SandboxRoot(home, root string) string {
	return sandbox.ProjectRoot(home, "sandbox", root)
}

func (o *Options) fillConfig() error {
	cfg := DefaultConfig()
	if o.Home != "" {
		loaded, err := LoadConfig(o.Home)
		if err != nil {
			return err
		}
		cfg = loaded
	}
	if o.MaxWorkers == 0 {
		o.MaxWorkers = cfg.Workers
	}
	if o.RAMGB == 0 {
		o.RAMGB = cfg.RAMGB
	}
	if o.CPUs == 0 {
		o.CPUs = cfg.CPUs
	}
	if o.DiskGB == 0 {
		o.DiskGB = cfg.DiskGB
	}
	return (Config{Workers: o.MaxWorkers, RAMGB: o.RAMGB, CPUs: o.CPUs, DiskGB: o.DiskGB}).Validate()
}
