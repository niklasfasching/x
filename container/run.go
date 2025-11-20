package container

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

var EnvKey = "RE_EXEC_NAMESPACED"

func RunInChroot(lowerDir, upperDir, workDir string, f func(), bindPaths ...string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(err, r.(error))
		}
	}()
	try(AwaitIDMapping(), "await")
	originalRoot, rootErr := syscall.Open("/", syscall.O_RDONLY, 0)
	originalPwd, pwdErr := os.Getwd()
	try(errors.Join(rootErr, pwdErr), "failed to capture root/pwd")
	fs := []func() error{}
	defer syscall.Close(originalRoot)
	defer func() {
		slices.Reverse(fs)
		try(syscall.Fchdir(originalRoot), "failed to fchdir to original root")
		try(syscall.Chroot("."), "failed to chroot to original root")
		try(syscall.Chdir(originalPwd), "failed to chdir to original pwd")
		errs := []error{}
		for _, f := range fs {
			errs = append(errs, f())
		}
		try(errors.Join(errs...), "reset")
	}()
	fs = append(fs, func() error { return os.RemoveAll(workDir) })
	overlayWorkDir, chrootDir := mkdir(workDir, "work"), mkdir(workDir, "chroot")
	fs = append(fs, overlay(lowerDir, chrootDir, upperDir, overlayWorkDir))
	fs = append(fs, mount("/dev", mkdir(chrootDir, "dev"), "", syscall.MS_BIND|syscall.MS_REC, ""))
	fs = append(fs, mount("/proc", mkdir(chrootDir, "proc"), "proc", 0, ""))
	fs = append(fs, mount("/tmp", mkdir(chrootDir, "tmp"), "tmpfs", 0, ""))
	for _, srcPath := range append(bindPaths, "/etc/resolv.conf") {
		xs := strings.SplitN(srcPath+"::", ":", 3)
		srcPath, dstPath := xs[0], filepath.Join(chrootDir, cmp.Or(xs[1], srcPath))
		flags := uintptr(syscall.MS_BIND)
		if kind := cmp.Or(xs[2], "ro"); kind == "rw" {
			flags = flags | syscall.MS_RDONLY
		}
		if f, err := os.Stat(srcPath); err != nil {
			panic(fmt.Errorf("failed to stat bind path %q: %w", srcPath, err))
		} else if f.IsDir() {
			mkdir(dstPath)
		} else {
			touch(dstPath)
		}
		fs = append(fs, mount(srcPath, dstPath, "", flags, ""))
	}
	try(syscall.Chroot(chrootDir), "failed to chroot")
	try(syscall.Chdir("/"), "failed to chdir")
	f()
	return nil
}

func ReExecNamespaced(v string) (string, error) {
	if v := os.Getenv(EnvKey); v != "" {
		return v, AwaitIDMapping()
	}
	allCaps := []uintptr{} // could also use DAC_OVERRIDE|SYS_ADMIN|SYS_CHROOT
	for i := 0; i <= unix.CAP_LAST_CAP; i++ {
		allCaps = append(allCaps, uintptr(i))
	}
	c := exec.Cmd{
		Path:   "/proc/self/exe",
		Args:   os.Args,
		Env:    append(os.Environ(), fmt.Sprintf("RE_EXEC_NAMESPACED=%s", v)),
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		SysProcAttr: &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
			AmbientCaps: allCaps,
		},
	}
	// https://github.com/golang/go/issues/50098
	uid, gid := fmt.Sprint(os.Getuid()), fmt.Sprint(os.Getgid())
	if uid == "0" {
		c.SysProcAttr.GidMappingsEnableSetgroups = true
		c.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{HostID: os.Getuid(), Size: 65536}}
		c.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{HostID: os.Getgid(), Size: 65536}}
	}
	if err := c.Start(); err != nil {
		return "", fmt.Errorf("failed to start child: %v", err)
	}
	defer c.Process.Kill()
	if childPid := fmt.Sprint(c.Process.Pid); uid != "0" {
		uidMappings := []string{childPid, "0", uid, "1", "1", "100000", "65536"}
		gidMappings := []string{childPid, "0", gid, "1", "1", "100000", "65536"}
		if err := exec.Command("newuidmap", uidMappings...).Run(); err != nil {
			return "", fmt.Errorf("failed to run newuidmap: %w", err)
		} else if err := exec.Command("newgidmap", gidMappings...).Run(); err != nil {
			return "", fmt.Errorf("failed to run newgidmap: %w", err)
		}
	}
	if err := c.Process.Signal(syscall.SIGUSR1); err != nil {
		return "", fmt.Errorf("failed to signal: %w", err)
	} else if err := c.Wait(); err != nil {
		return "", fmt.Errorf("failed to exec in ns: %w", err)
	}
	return "", nil
}

func AwaitIDMapping() error {
	if os.Getuid() != 0 || os.Getgid() != 0 {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGUSR1)
		<-ch
		signal.Stop(ch)
		close(ch)
		if uid, gid := os.Getuid(), os.Getgid(); uid != 0 || gid != 0 {
			return fmt.Errorf("uid / gid mapping failed: uid=%d gid=%d", uid, gid)
		}
	}
	return nil
}
