package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGit(t *testing.T) {
	workDir, gitDir := initRepo(t, map[string]string{
		"README.md": "hello world",
	})
	r := &Remote{Client: &Shell{gitDir}, Repo: gitDir}
	refs, err := r.ListRemote()
	if err != nil {
		t.Fatalf("ListRemote failed: %v", err)
	}
	main := refs["refs/heads/main"]
	if !commitHashRe.MatchString(main) {
		t.Fatalf("invalid commit hash for main: %v", refs)
	}
	mainCommit, err := r.Fetch("main")
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	} else if mainCommit.Hash != main {
		t.Fatalf("Mismatched hash: %s != %s", mainCommit.Hash, main)
	}
	c, err := r.NewCommit("main", "message", "yolo")
	if err != nil {
		t.Fatalf("NewCommit failed: %v", err)
	} else if c.Parent.Hash != mainCommit.Hash {
		t.Fatalf("Mismatched parent hash: %s != %s", c.Parent.Hash, mainCommit.Hash)
	} else if c.Meta["message"] != "yolo" {
		t.Fatalf("Mismatched commit message: %s", c.Meta)
	}

	// Unchanged should not be added
	if changed := c.Add("README.md", []byte("hello world")); changed {
		t.Fatalf("Unexpectedly treated as changed")
	} else if _, isEmpty := c.PackData(); !isEmpty {
		t.Fatalf("Unexpectedly reported not empty")
	}
	// Changed files should be added and pushed correctly
	if changed := c.Add("NOTES.md", []byte("hello world!!!")); !changed {
		t.Fatalf("Unexpectedly treated as unchanged")
	} else if packData, isEmpty := c.PackData(); isEmpty {
		t.Fatalf("Unexpectedly reported empty")
	} else if err := r.Push("main", c, packData); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}
	assertRepo(t, workDir, map[string]string{
		"README.md": "hello world",
		"NOTES.md":  "hello world!!!",
	})
}

func initRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "git-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("Failed to clean dir: %v", dir)
		}
	})
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", name, err)
		}
	}
	run(t, dir, `git init -b main && git add . -A && git commit -m "init"`)
	return dir, filepath.Join(dir, ".git")
}

func assertRepo(t *testing.T, dir string, files map[string]string) {
	run(t, dir, `git reset --hard`)
	fs, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("Failed to read git dir: %v", err)
	}
	for _, f := range fs {
		if f.Name() == ".git" {
			continue
		}
		expected := files[f.Name()]
		bs, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			t.Fatalf("Failed to read %q: %v", f.Name(), err)
		} else if string(bs) != expected {
			t.Fatalf("Mismatched %q: %q != %q", f.Name(), string(bs), expected)
		}
		delete(files, f.Name())
	}
	if len(files) != 0 {
		t.Fatalf("Missing: %v", files)
	}
}

func run(t *testing.T, dir, script string) {
	t.Helper()
	cmd := exec.Command("bash", "-c", "set -euo pipefail;\n"+script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=go",
		"GIT_AUTHOR_EMAIL=go@git",
		"GIT_COMMITTER_NAME=go",
		"GIT_COMMITTER_EMAIL=go@git",
	)
	if testing.Verbose() {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("Script failed: %v", err)
	}
}
