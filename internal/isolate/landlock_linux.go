//go:build linux

package isolate

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	llCapSysAdmin          = 21
	llOPath                = 0o10000000
)

const (
	llExecute    uint64 = 1 << 0
	llWriteFile  uint64 = 1 << 1
	llReadFile   uint64 = 1 << 2
	llReadDir    uint64 = 1 << 3
	llRemoveDir  uint64 = 1 << 4
	llRemoveFile uint64 = 1 << 5
	llMakeChar   uint64 = 1 << 6
	llMakeDir    uint64 = 1 << 7
	llMakeReg    uint64 = 1 << 8
	llMakeSock   uint64 = 1 << 9
	llMakeFifo   uint64 = 1 << 10
	llMakeBlock  uint64 = 1 << 11
	llMakeSym    uint64 = 1 << 12
	llRefer      uint64 = 1 << 13
	llTruncate   uint64 = 1 << 14
	llIoctlDev   uint64 = 1 << 15

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

// MaybeRunInit completes confined child startup when FYR_ISOLATE_INIT=1.
// Call it at the start of main and TestMain before any work runs.
func MaybeRunInit() {
	if os.Getenv(IsolateInitEnv) != "1" {
		return
	}
	runtime.LockOSThread()
	pol, err := readInitPolicy()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fyr isolate: %v\n", err)
		os.Exit(126)
	}
	if err := mountIsolatedFS(); err != nil {
		fmt.Fprintf(os.Stderr, "fyr isolate: %v\n", err)
		os.Exit(126)
	}
	pol.RWPaths = append(pol.RWPaths, "/dev/pts")
	if err := applyLandlock(pol); err != nil {
		fmt.Fprintf(os.Stderr, "fyr isolate: %v\n", err)
		os.Exit(126)
	}
	if pol.Dir != "" {
		if err := os.Chdir(pol.Dir); err != nil {
			fmt.Fprintf(os.Stderr, "fyr isolate chdir %s: %v\n", pol.Dir, err)
			os.Exit(126)
		}
	}
	env := cleanIsolateEnv(pol.Env)
	if err := syscall.Exec(pol.Argv[0], pol.Argv, env); err != nil {
		fmt.Fprintf(os.Stderr, "fyr isolate exec %s: %v\n", pol.Argv[0], err)
		os.Exit(127)
	}
}

func mountIsolatedFS() error {
	if err := syscall.Mount("none", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return fmt.Errorf("mount / private: %w", err)
	}
	if err := syscall.Mount("proc", "/proc", "proc", syscall.MS_NOSUID|syscall.MS_NOEXEC|syscall.MS_NODEV, ""); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}
	if err := syscall.Mount("devpts", "/dev/pts", "devpts", syscall.MS_NOSUID|syscall.MS_NOEXEC, "newinstance,ptmxmode=0666,mode=0620"); err != nil {
		return fmt.Errorf("mount /dev/pts: %w", err)
	}
	if err := syscall.Mount("/dev/pts/ptmx", "/dev/ptmx", "", syscall.MS_BIND, ""); err != nil {
		return fmt.Errorf("bind /dev/ptmx: %w", err)
	}
	if _, _, errno := landlockSyscall6(syscall.SYS_PRCTL, llPrCapAmbient, llPrCapAmbientClearAll, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("clear ambient caps: %w", errno)
	}
	return nil
}

func readInitPolicy() (Policy, error) {
	f := os.NewFile(3, "isolate-policy")
	if f == nil {
		return Policy{}, errors.New("missing policy fd")
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		return Policy{}, err
	}
	var pol Policy
	if err := json.Unmarshal(data, &pol); err != nil {
		return Policy{}, err
	}
	if len(pol.Argv) == 0 || pol.Argv[0] == "" {
		return Policy{}, errors.New("empty argv in policy")
	}
	return pol, nil
}

func prepareIsolatedCmd(pol Policy) (*exec.Cmd, func(), error) {
	if err := checkOSAvailable(); err != nil {
		return nil, nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("isolate executable: %w", err)
	}
	payload, err := json.Marshal(pol)
	if err != nil {
		return nil, nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	uid, gid := os.Getuid(), os.Getgid()
	cmd := exec.Command(exe)
	cmd.Dir = pol.Dir
	cmd.Env = append(cleanIsolateEnv(pol.Env), IsolateInitEnv+"=1")
	cmd.ExtraFiles = []*os.File{pr}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
		UidMappings: []syscall.SysProcIDMap{{
			ContainerID: uid, HostID: uid, Size: 1,
		}},
		GidMappings: []syscall.SysProcIDMap{{
			ContainerID: gid, HostID: gid, Size: 1,
		}},
		GidMappingsEnableSetgroups: false,
		AmbientCaps:                []uintptr{llCapSysAdmin},
	}
	go func() {
		_, _ = pw.Write(payload)
		_ = pw.Close()
	}()
	return cmd, func() { _ = pr.Close() }, nil
}

func cleanIsolateEnv(env []string) []string {
	out := make([]string, 0, len(env))
	prefix := IsolateInitEnv + "="
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return out
}
