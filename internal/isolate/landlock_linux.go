//go:build linux

package isolate

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	llCreateRulesetVersion = 1 << 0
	llRulePathBeneath      = 1
	llPrSetNoNewPrivs      = 38
	llPrCapAmbient         = 47
	llPrCapAmbientClearAll = 4
	llCapNetAdmin          = 12
	llCapSysAdmin          = 21
	llOPath                = 0o10000000
	siocGIFFlags           = 0x8913
	siocSIFFlags           = 0x8914
	iffUp                  = 0x1
)

const (
	llExecute   uint64 = 1 << 0
	llWriteFile uint64 = 1 << 1
	llReadFile  uint64 = 1 << 2
	llReadDir   uint64 = 1 << 3
	llTruncate  uint64 = 1 << 14
	llIoctlDev  uint64 = 1 << 15

	llFileMask uint64 = llExecute | llWriteFile | llReadFile | llTruncate | llIoctlDev
	llROMask   uint64 = llExecute | llReadFile | llReadDir
)

var (
	landlockSyscall  = syscall.Syscall
	landlockSyscall6 = syscall.Syscall6
	testSkipLandlock bool
)

func abiMask(abi int) uint64 {
	switch {
	case abi <= 0:
		return 0
	case abi == 1:
		return (1 << 13) - 1
	case abi == 2:
		return (1 << 14) - 1
	case abi == 3 || abi == 4:
		return (1 << 15) - 1
	default:
		return (1 << 16) - 1
	}
}

func landlockABI() (int, error) {
	if testSkipLandlock {
		return 0, errors.New("landlock disabled")
	}
	r1, _, errno := landlockSyscall(sysLandlockCreateRuleset, 0, 0, llCreateRulesetVersion)
	if errno != 0 {
		return 0, fmt.Errorf("landlock unavailable: %w", errno)
	}
	if r1 < 1 {
		return 0, fmt.Errorf("landlock ABI version %d unsupported", r1)
	}
	return int(r1), nil
}

func checkOSAvailable() error {
	if _, err := landlockABI(); err != nil {
		return err
	}
	return checkUserNSAvailable()
}

func checkUserNSAvailable() error {
	for _, path := range []string{"/proc/sys/user/max_user_namespaces", "/proc/sys/kernel/unprivileged_userns_clone"} {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) == "0" {
			return fmt.Errorf("user namespaces disabled (%s=0)", path)
		}
	}
	return nil
}

func applyLandlock(p Policy) error {
	abi, err := landlockABI()
	if err != nil {
		return err
	}
	handled := abiMask(abi)
	var attr [8]byte
	binary.NativeEndian.PutUint64(attr[:], handled)
	rsFD, _, errno := landlockSyscall(sysLandlockCreateRuleset, uintptr(unsafe.Pointer(&attr[0])), uintptr(len(attr)), 0)
	if errno != 0 {
		return fmt.Errorf("landlock create ruleset: %w", errno)
	}
	defer syscall.Close(int(rsFD))
	if err := addPathRules(int(rsFD), p.RWPaths, handled); err != nil {
		return err
	}
	if err := addPathRules(int(rsFD), p.ROPaths, llROMask&handled); err != nil {
		return err
	}
	_, _, errno = landlockSyscall6(syscall.SYS_PRCTL, llPrSetNoNewPrivs, 1, 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("prctl NO_NEW_PRIVS: %w", errno)
	}
	_, _, errno = landlockSyscall(sysLandlockRestrictSelf, uintptr(rsFD), 0, 0)
	if errno != 0 {
		return fmt.Errorf("landlock restrict self: %w", errno)
	}
	return nil
}

func addPathRules(rsFD int, paths []string, access uint64) error {
	for _, p := range paths {
		if err := addOnePathRule(rsFD, p, access); err != nil {
			return err
		}
	}
	return nil
}

func addOnePathRule(rsFD int, path string, access uint64) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("landlock resolve %s: %w", path, err)
	}
	fd, err := syscall.Open(resolved, llOPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("landlock open %s: %w", resolved, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("landlock fstat %s: %w", resolved, err)
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		access &= llFileMask
	}
	if access == 0 {
		return nil
	}
	var pb [12]byte
	binary.NativeEndian.PutUint64(pb[0:8], access)
	binary.NativeEndian.PutUint32(pb[8:12], uint32(fd))
	_, _, errno := landlockSyscall6(sysLandlockAddRule, uintptr(rsFD), llRulePathBeneath, uintptr(unsafe.Pointer(&pb[0])), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("landlock add rule %s: %w", resolved, errno)
	}
	return nil
}

func prepareIsolatedCmd(pol Policy) (*exec.Cmd, func(), error) {
	if err := checkOSAvailable(); err != nil {
		return nil, nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("isolate executable: %w", err)
	}
	var initEnv []string
	pol, initEnv = withCoverDirForInit(pol, append(cleanIsolateEnv(pol.Env), IsolateInitEnv+"=1"))
	payload, err := json.Marshal(pol)
	if err != nil {
		return nil, nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	uid, gid := os.Getuid(), os.Getgid()
	cloneFlags := uintptr(syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID)
	ambientCaps := []uintptr{llCapSysAdmin}
	if !pol.Network {
		cloneFlags |= syscall.CLONE_NEWNET
		ambientCaps = append(ambientCaps, llCapNetAdmin)
	}
	cmd := exec.Command(exe)
	cmd.Dir = pol.Dir
	cmd.Env = initEnv
	cmd.ExtraFiles = []*os.File{pr}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Cloneflags: cloneFlags,
		UidMappings: []syscall.SysProcIDMap{{
			ContainerID: uid, HostID: uid, Size: 1,
		}},
		GidMappings: []syscall.SysProcIDMap{{
			ContainerID: gid, HostID: gid, Size: 1,
		}},
		GidMappingsEnableSetgroups: false,
		AmbientCaps:                ambientCaps,
	}
	go func() {
		_, _ = pw.Write(payload)
		_ = pw.Close()
	}()
	return cmd, func() { _ = pr.Close() }, nil
}

func withCoverDirForInit(pol Policy, initEnv []string) (Policy, []string) {
	cov, fromPol := envValue(pol.Env, "GOCOVERDIR")
	if !fromPol {
		cov = os.Getenv("GOCOVERDIR")
	}
	if cov == "" {
		return pol, initEnv
	}
	resolved, ok := safeCoverDir(cov)
	if !ok {
		return pol, dropEnvKey(initEnv, "GOCOVERDIR")
	}
	initEnv = setEnvKey(initEnv, "GOCOVERDIR", resolved)
	pol.RWPaths = appendUniqueClean(pol.RWPaths, resolved)
	return pol, initEnv
}

var (
	coverBroadDirs  = []string{"/home", "/tmp", "/var", "/var/tmp", "/opt", "/srv"}
	coverSystemDirs = []string{"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/run", "/sbin", "/sys", "/usr"}
	coverHomeSubs   = []string{".ssh", ".gnupg", ".config", ".gemini", ".local"}
)

func safeCoverDir(raw string) (string, bool) {
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", false
	}
	resolved = filepath.Clean(resolved)
	fi, err := os.Stat(resolved)
	if err != nil || !fi.IsDir() || resolved == "/" || filepath.Dir(resolved) == resolved {
		return "", false
	}
	if isForbiddenCoverDir(resolved) {
		return "", false
	}
	return resolved, true
}

func isForbiddenCoverDir(resolved string) bool {
	tmp := filepath.Clean(os.TempDir())
	if resolved == tmp {
		return true
	}
	if tmpRes, err := filepath.EvalSymlinks(tmp); err == nil && resolved == filepath.Clean(tmpRes) {
		return true
	}
	for _, b := range coverBroadDirs {
		if resolved == b {
			return true
		}
	}
	for _, sys := range coverSystemDirs {
		if sameOrUnder(resolved, sys) {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	if hr, err := filepath.EvalSymlinks(home); err == nil {
		home = hr
	}
	home = filepath.Clean(home)
	if resolved == home || sameOrUnder(home, resolved) {
		return true
	}
	for _, sub := range coverHomeSubs {
		if sameOrUnder(resolved, filepath.Join(home, sub)) {
			return true
		}
	}
	return false
}

func sameOrUnder(path, base string) bool {
	p, b := filepath.Clean(path), filepath.Clean(base)
	if b == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == b || strings.HasPrefix(p, b+string(filepath.Separator))
}

func dropEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

func setEnvKey(env []string, key, val string) []string {
	prefix := key + "="
	entry := prefix + val
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			if !replaced {
				out = append(out, entry)
				replaced = true
			}
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, entry)
	}
	return out
}

func hasEnvKey(env []string, key string) bool {
	_, ok := envValue(env, key)
	return ok
}

func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if val, ok := strings.CutPrefix(env[i], prefix); ok {
			return val, true
		}
	}
	return "", false
}
