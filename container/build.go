package container

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/exp/slices"
)

type Runner interface {
	Run(cmd, layerDir, lowerDirs string) error
}

type Builder struct {
	Registry
	LayersDir string
	Force     bool
	Mounts    []string
}

type File struct {
	BaseID, Base, Exec, CtxDir string
	Layers                     []CMD
	Exposes                    []string
}

type CMD struct {
	ID, K, V    string
	WorkDir     string
	Env, Mounts []string
}

var LayerInfoFile = ".layer.txt"

func (b *Builder) Build(f *File, ctxDir string) (err error) {
	ctxDir = cmp.Or(ctxDir, f.CtxDir)
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(err, r.(error))
		}
	}()
	try(os.Chdir(ctxDir), "chdir")
	baseDir := filepath.Join(b.LayersDir, f.BaseID)
	log.Printf("=== FROM %s (0/%d)\n\t%s", f.Base, len(f.Layers), f.BaseID)
	changed, err := b.Registry.Pull(f.Base, baseDir)
	if err != nil {
		return fmt.Errorf("pull: %w", err)
	}
	lowerDirs, changed := []string{baseDir}, changed || b.Force
	for i, c := range f.Layers {
		log.Printf("=== %s %s (%d/%d)\n\t%s", c.K, c.V, i+1, len(f.Layers), c.ID)
		layerDir := filepath.Join(b.LayersDir, c.ID)
		try(os.MkdirAll(layerDir, 0755), "mkdir layerDir")
		switch c.K {
		case "COPY":
			changed = b.Copy(c, ctxDir, layerDir) || changed
		case "RUN":
			changed = b.run(c, ctxDir, layerDir, lowerDirs, changed) || changed
		default:
			return fmt.Errorf("unsupported cmd: %v", c)
		}
		b.FinalizeSysExtDir(layerDir)
		lowerDirs = append(lowerDirs, layerDir)
	}
	return nil
}

func (b *Builder) run(c CMD, contextDir, layerDir string, lowerDirs []string, changed bool) bool {
	lowerDir, workDir := strings.Join(lowerDirs, ":"), layerDir+".tmp"
	layerInfoFile := filepath.Join(layerDir, LayerInfoFile)
	lowerDirBS, _ := os.ReadFile(layerInfoFile)
	// NOTE: We abuse volume as a bind mount and must rebuild any command
	// with (non-builder-lvl) bind mounts since we can't tell what changed
	if lowerDir == string(lowerDirBS) && !changed && len(c.Mounts) == 0 {
		return false
	}
	try(errors.Join(os.RemoveAll(layerDir), os.RemoveAll(workDir)), "rm layer/work dir")
	try(errors.Join(os.MkdirAll(layerDir, 0755), os.MkdirAll(workDir, 0755)), "mkdir layer/work dir")
	defer os.RemoveAll(workDir)
	err := RunInChroot(lowerDir, layerDir, workDir, func() {
		if err := os.MkdirAll(c.WorkDir, 0755); err != nil {
			panic(err)
		}
		cmd := exec.Command("sh", "-c", c.V)
		cmd.Dir, cmd.Stdin, cmd.Stdout, cmd.Stderr = c.WorkDir, os.Stdin, os.Stdout, os.Stderr
		try(cmd.Run(), "failed to exec cmd %q", c.V)
	}, append(slices.Clone(b.Mounts), c.Mounts...)...)
	if err != nil {
		panic(fmt.Errorf("run: %w", err))
	}
	try(os.WriteFile(layerInfoFile, []byte(lowerDir), 0644), "write layerInfo")
	return true
}

func (b *Builder) Copy(c CMD, contextDir, layerDir string) bool {
	ps := strings.Fields(c.V)
	if len(ps) < 2 {
		panic(fmt.Errorf("invalid COPY: %q", c.V))
	}
	srcs, dst := ps[:len(ps)-1], ps[len(ps)-1]
	dstDir := filepath.Clean(filepath.Join(layerDir, c.WorkDir, dst))
	h := sha256.New()
	for _, src := range srcs {
		srcPath := filepath.Join(contextDir, src)
		if info, err := os.Stat(srcPath); err != nil {
			panic(err)
		} else if info.IsDir() {
			h.Write([]byte(hashDir(srcPath)))
		} else {
			h.Write([]byte(hashFile(srcPath)))
		}
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))
	layerHash, _ := os.ReadFile(filepath.Join(layerDir, LayerInfoFile))
	if hash == string(layerHash) {
		return false
	}
	try(errors.Join(os.RemoveAll(dstDir), os.MkdirAll(dstDir, 0755)), "rm/mkdir dstDir")
	for _, src := range srcs {
		srcPath := filepath.Join(contextDir, src)
		dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
		if info, err := os.Stat(srcPath); err != nil {
			panic(err)
		} else if info.IsDir() {
			copyDir(srcPath, dstPath)
		} else {
			copyFile(srcPath, dstPath)
		}
	}
	try(os.WriteFile(filepath.Join(layerDir, LayerInfoFile), []byte(hash), 0644), "write layerInfo")
	return true
}

func (b *Builder) FinalizeSysExtDir(layerDir string) {
	name := filepath.Base(layerDir)
	extDir := filepath.Join(layerDir, "usr", "lib", "extension-release.d")
	extFile := filepath.Join(extDir, "extension-release."+name)
	try(os.MkdirAll(extDir, 0755), "mkdir extDir")
	try(os.WriteFile(extFile, []byte("ID=_any"), 0644), "write extFile")
}

func (b *Builder) Prune(excludedDockerfiles [][2]string) error {
	m := map[string]bool{}
	for _, x := range excludedDockerfiles {
		d, err := Parse(x[0], x[1])
		if err != nil {
			return err
		}
		m[d.BaseID] = true
		for _, c := range d.Layers {
			m[c.ID] = true
		}
	}
	fs, err := os.ReadDir(b.LayersDir)
	if err != nil {
		return err
	}
	for _, f := range fs {
		if m[f.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(b.LayersDir, f.Name())); err != nil {
			return err
		}
	}
	return nil
}

func FileArgs(dockerfilePathOrContent, ctxDir string) [2]string {
	if strings.Contains(dockerfilePathOrContent, "FROM ") {
		return [2]string{dockerfilePathOrContent, ctxDir}
	}
	return [2]string{dockerfilePathOrContent, ctxDir}
}

func Parse(dockerfilePathOrContent, ctxDir string) (*File, error) {
	content := dockerfilePathOrContent
	if !strings.Contains(dockerfilePathOrContent, "FROM ") {
		path := dockerfilePathOrContent
		if !filepath.IsAbs(path) {
			path = filepath.Join(ctxDir, path)
		}
		bs, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content = string(bs)
	}
	return ParseString(content, ctxDir)
}

func ParseString(content, ctxDir string) (*File, error) {
	ctxDir, err := filepath.Abs(ctxDir)
	if err != nil {
		return nil, err
	}
	s, cs := bufio.NewScanner(strings.NewReader(content)), []CMD{}
	for v := ""; s.Scan(); {
		if l := strings.TrimSpace(s.Text()); l == "" || strings.HasPrefix(l, "#") {
			continue
		} else if l, ok := strings.CutSuffix(l, "\\"); ok {
			v += l + " "
		} else {
			ps := strings.SplitN(v+l, " ", 2)
			cs, v = append(cs, CMD{K: strings.ToUpper(ps[0]), V: ps[1]}), ""
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	} else if len(cs) == 0 || cs[0].K != "FROM" {
		return nil, fmt.Errorf("must start with FROM %v", cs)
	}
	base := strings.TrimSpace(strings.SplitN(cs[0].V, "#", 2)[0])
	d := &File{BaseID: hash(base), Base: base, CtxDir: cmp.Or(ctxDir, ".")}
	workDir, env, mnts := "/", []string{}, []string{}
	for i, c := range cs[1:] {
		switch c.K {
		case "CMD":
			d.Exec = c.V
		case "WORKDIR":
			workDir = c.V
		case "ENV":
			env = append(env, c.V)
		case "VOLUME":
			mnts = append(mnts, c.V)
		case "EXPOSE":
			d.Exposes = append(d.Exposes, c.V)
		case "RUN", "COPY":
			c.ID = hash(d.CtxDir, d.Base, fmt.Sprint(i), fmt.Sprint(c))
			c.WorkDir, c.Env, c.Mounts = workDir, slices.Clone(env), slices.Clone(mnts)
			d.Layers = append(d.Layers, c)
		default:
			return nil, fmt.Errorf("unsupported cmd: %v", c)
		}
	}
	return d, nil
}
