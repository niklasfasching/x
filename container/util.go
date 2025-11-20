package container

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func overlay(lowerDir, dstDir, upperDir, workDir string) func() error {
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", lowerDir, upperDir, workDir)
	return mount("", dstDir, "overlay", 0, opts)
}

func mount(srcPath, dstPath, fsType string, flags uintptr, data string) func() error {
	try(syscall.Mount(srcPath, dstPath, fsType, flags, data),
		"mount: failed to mount %q:%q %q %v %q", srcPath, dstPath, fsType, flags, data)
	return func() error {
		if err := syscall.Unmount(dstPath, syscall.MNT_DETACH); err != nil {
			return fmt.Errorf("unmount: %q %q %q: %w", srcPath, dstPath, fsType, err)
		}
		return nil
	}
}

func touch(parts ...string) string {
	path := filepath.Join(parts...)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	try(os.WriteFile(path, nil, 0644), "failed to touch %q", path)
	return path
}

func mkdir(parts ...string) string {
	dir := filepath.Join(parts...)
	try(os.MkdirAll(dir, 0755), "failed to mkdir %q", dir)
	return dir
}

func copyDir(srcDir, dstDir string) {
	srcDir, dstDir = filepath.Clean(srcDir), filepath.Clean(dstDir)
	info, err := os.Stat(srcDir)
	if err != nil {
		panic(err)
	} else if !info.IsDir() {
		panic(fmt.Errorf("not a directory: %q", srcDir))
	} else if err := os.MkdirAll(dstDir, info.Mode()); err != nil {
		panic(err)
	}
	fs, err := os.ReadDir(srcDir)
	if err != nil {
		panic(err)
	}
	for _, f := range fs {
		srcPath := filepath.Join(srcDir, f.Name())
		dstPath := filepath.Join(dstDir, f.Name())
		if f.IsDir() {
			copyDir(srcPath, dstPath)
		} else {
			copyFile(srcPath, dstPath)
		}
	}
}

func copyFile(src, dst string) {
	in, err := os.Open(src)
	if err != nil {
		panic(err)
	}
	info, err := os.Stat(src)
	if err != nil {
		panic(err)
	} else if info.Mode()&os.ModeSymlink != 0 {
		panic(fmt.Errorf("symlinks not implemented: %q", src))
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		panic(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		panic(err)
	} else if err := out.Sync(); err != nil {
		panic(err)
	} else if err := os.Chmod(dst, info.Mode()); err != nil {
		panic(err)
	}
}

func hash(s ...string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join(s, "|")))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func hashDir(dir string) string {
	xs := []string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() || err != nil {
			return err
		}
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		hash := hashFile(path)
		xs = append(xs, fmt.Sprintf("%s %s %s", relPath, info.Mode(), hash))
		return nil
	})
	if err != nil {
		panic(err)
	}
	h := sha256.New()
	sort.Strings(xs)
	for _, s := range xs {
		h.Write([]byte(s))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func try(err error, tpl string, args ...any) {
	if err != nil {
		panic(fmt.Errorf(tpl+": %w", append(args, err)...))
	}
}
