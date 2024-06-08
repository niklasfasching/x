package docker

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type Registry interface {
	Pull(image, dir string) error
}

type Builder struct {
	Registry
}

type CMD struct{ K, V string }

func (b *Builder) Build(dockerFile, dir string) error {
	cs, sha, err := Parse(strings.NewReader(dockerFile))
	if err != nil {
		return err
	}
	shaFile := filepath.Join(dir, ".dockerfile_sha")
	prevShaBS, _ := os.ReadFile(shaFile)
	if sha == string(prevShaBS) {
		log.Println("Already up to date")
		return nil
	} else if err := os.RemoveAll(dir); err != nil {
		return err
	} else if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	wd := "/"
	for i, c := range cs {
		if (i == 0 && c.K != "FROM") || i != 0 && c.K == "FROM" {
			return fmt.Errorf("FROM must be first cmd: %v", cs)
		}
		log.Printf("STEP %v: %q", i, c.K+" "+c.V)
		switch c.K {
		case "FROM":
			image := strings.TrimSpace(strings.SplitN(c.V, "#", 2)[0])
			if err := b.Pull(image, dir); err != nil {
				return err
			}
		case "WORKDIR":
			wd = c.V
		case "RUN":
			if err := RunNS(dir, wd, c.V); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported cmd: %v", c)
		}
	}
	return os.WriteFile(shaFile, []byte(sha), 0600)
}

func Parse(f io.Reader) ([]CMD, string, error) {
	s, h, cs := bufio.NewScanner(f), sha256.New(), []CMD{}
	for c := ""; s.Scan(); {
		if l := strings.TrimSpace(s.Text()); l == "" || strings.HasPrefix(l, "#") {
			continue
		} else if strings.HasSuffix(l, "\\") {
			c += strings.TrimSuffix(l, "\\") + " "
		} else {
			c += l
			h.Write([]byte(c))
			ps := strings.SplitN(c, " ", 2)
			cs, c = append(cs, CMD{strings.ToUpper(ps[0]), ps[1]}), ""
		}
	}
	return cs, fmt.Sprintf("%x", h.Sum(nil)), s.Err()
}

func RunNS(dir, wd, cmd string) error {
	c := exec.Cmd{
		Path:   "/bin/bash",
		Args:   []string{"/bin/bash", "-c", cmd},
		Dir:    wd,
		Env:    []string{},
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		SysProcAttr: &syscall.SysProcAttr{
			Chroot:       dir,
			Unshareflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
			UidMappings:  []syscall.SysProcIDMap{{HostID: os.Getuid(), Size: 1}},
			GidMappings:  []syscall.SysProcIDMap{{HostID: os.Getgid(), Size: 1}},
		},
	}
	return c.Run()
}
