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
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

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
	if err := mountIsolatedFS(!pol.Network); err != nil {
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
	os.Exit(runInitChild(pol))
}

// runInitChild spawns the sandboxed command as PID 2 while PID 1 stays alive as
// the CLONE_NEWPID init process so child workers never observe PPID == 1 (BUG-549).
func runInitChild(pol Policy) int {
	cmd := exec.Command(pol.Argv[0], pol.Argv[1:]...)
	cmd.Env = cleanIsolateEnv(pol.Env)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	if err := cmd.Start(); err != nil {
		signal.Stop(sigCh)
		fmt.Fprintf(os.Stderr, "fyr isolate exec %s: %v\n", pol.Argv[0], err)
		return 127
	}
	_ = os.Stdin.Close()
	_ = os.Stdout.Close()
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigCh:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	signal.Stop(sigCh)
	close(done)
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

func mountIsolatedFS(bringLoopback bool) error {
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
	if bringLoopback {
		if err := bringUpLoopback(); err != nil {
			return err
		}
	}
	if _, _, errno := landlockSyscall6(syscall.SYS_PRCTL, llPrCapAmbient, llPrCapAmbientClearAll, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("clear ambient caps: %w", errno)
	}
	return nil
}

func bringUpLoopback() error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("loopback socket: %w", err)
	}
	defer syscall.Close(fd)
	var ifr [40]byte
	copy(ifr[:16], "lo")
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), siocGIFFlags, uintptr(unsafe.Pointer(&ifr[0]))); errno != 0 {
		return fmt.Errorf("loopback SIOCGIFFlags: %w", errno)
	}
	binary.NativeEndian.PutUint16(ifr[16:18], binary.NativeEndian.Uint16(ifr[16:18])|iffUp)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), siocSIFFlags, uintptr(unsafe.Pointer(&ifr[0]))); errno != 0 {
		return fmt.Errorf("loopback SIOCSIFFlags: %w", errno)
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
