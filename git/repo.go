package git

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"maps"

	"golang.org/x/crypto/ssh"
)

type Remote struct {
	Client
	Repo string
}

type Client interface {
	Exec(string, ...string) (io.WriteCloser, io.ReadCloser, error)
	Close() error
}

type SSH struct{ *ssh.Client }
type Shell struct{ Dir string }
type readCloser struct {
	io.Reader
	close func() error
}

type Commit struct {
	Hash    string
	Meta    map[string]string
	Objects map[string][]byte
	FS      map[string]any
	Parent  *Commit
}

var commitHashRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// https://git-scm.com/docs/git-receive-pack/2.50.0#:~:text=0{40}
var emptyHash = strings.Repeat("0", 40)

func New(user, host, path string, key, hostKey []byte) (*Remote, error) {
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse ssh key: %w", err)
	}
	expectedHostKey, _, _, _, err := ssh.ParseAuthorizedKey(hostKey)
	if err != nil {
		return nil, fmt.Errorf("parse host key: %w", err)
	}
	client, err := ssh.Dial("tcp", host+":22", &ssh.ClientConfig{
		User:              user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   ssh.FixedHostKey(expectedHostKey),
		HostKeyAlgorithms: []string{expectedHostKey.Type()},
	})
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}
	return &Remote{&SSH{client}, path}, nil
}

func NewGitHub(path string, key []byte) (*Remote, error) {
	// ssh-keyscan github.com
	hostKey := "github.com ecdsa-sha2-nistp256 " +
		"AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZM" +
		"y7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg="
	return New("git", "github.com", path, key, []byte(hostKey))
}

func PushGitHub(path, ref string, key []byte, f func(*Commit) error, kvs ...string) error {
	parts := strings.SplitN(path, "/", 3)
	repo, dir := parts[0]+"/"+parts[1], "/"
	if len(parts) == 3 {
		dir = parts[2]
	}
	r, err := NewGitHub(repo, key)
	if err != nil {
		return err
	}
	c, err := r.NewCommit(ref, kvs...)
	if err != nil {
		return err
	} else if err := f(c); err != nil {
		return err
	}
	packData, isEmpty := c.PackData(dir)
	if isEmpty {
		log.Println("git push: already up to date")
		return nil
	}
	log.Printf("git push: objects=%d parent-objects=%d size=%.2fmb",
		len(c.Objects), len(c.Parent.Objects), float64(len(packData))/1e6)
	if err := r.Push(ref, c, packData); err != nil {
		return err
	}
	return nil
}

func (r *Remote) NewCommit(parentHashOrRef string, kvs ...string) (*Commit, error) {
	parent, err := r.Fetch(parentHashOrRef)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %q: %w", parentHashOrRef, err)
	}
	now := time.Now()
	m := map[string]string{
		"author":    "git <git@go>",
		"committer": "git <git@go>",
		"message":   now.Format(time.DateTime),
		"timestamp": fmt.Sprintf("%d %s", now.Unix(), now.Format("-0700")),
	}
	for i := 0; i+1 < len(kvs); i += 2 {
		m[kvs[i]] = kvs[i+1]
	}
	objects, fs := map[string][]byte{}, map[string]any{}
	return &Commit{emptyHash, m, objects, fs, parent}, nil
}

func (c *Commit) Add(path string, content []byte) (changed bool) {
	blobHash, blobObject := hashObject("blob", content)
	if _, unchanged := c.Parent.Objects[blobHash]; !unchanged {
		c.Objects[blobHash], changed = blobObject, true
	}
	lvl, parts := c.FS, strings.Split(strings.TrimLeft(path, "/"), "/")
	for i, name := range parts {
		if i == len(parts)-1 {
			lvl[name] = blobHash
		} else {
			if _, ok := lvl[name]; !ok {
				lvl[name] = map[string]any{}
			}
			lvl = lvl[name].(map[string]any)
		}
	}
	return changed
}

func (c *Commit) PackData(dir string) ([]byte, bool) {
	fs := c.FS
	if dir = strings.Trim(dir, "/"); dir != "" {
		fs = cloneFS(c.Parent.FS)
		current, dirFS, parts := fs, c.FS, strings.Split(dir, "/")
		for i, lvl := range parts {
			if i == len(parts)-1 {
				current[lvl] = dirFS[lvl]
			} else if lvlFS, ok := current[lvl].(map[string]any); ok {
				dirFS, _ = dirFS[lvl].(map[string]any)
				current = lvlFS
			} else {
				dirFS, _ = dirFS[lvl].(map[string]any)
				current[lvl] = map[string]any{}
				current = current[lvl].(map[string]any)
			}
		}
	}
	treeHash := c.buildTree(fs)
	c.Meta["tree"] = treeHash
	parentLine, isEmpty := "", treeHash == c.Parent.Meta["tree"]
	if c.Parent.Hash != emptyHash {
		parentLine = fmt.Sprintf("parent %s\n", c.Parent.Hash)
	}
	content := fmt.Sprintf("tree %s\n%sauthor %s %s\ncommitter %s %s\n\n%s\n",
		treeHash, parentLine, c.Meta["author"], c.Meta["timestamp"], c.Meta["committer"], c.Meta["timestamp"], c.Meta["message"])
	commitHash, commitObject := hashObject("commit", []byte(content))
	c.Objects[commitHash] = commitObject
	c.Hash = commitHash
	w, types := &bytes.Buffer{}, map[string]byte{"commit": 1, "tree": 2, "blob": 3}
	w.WriteString("PACK")
	binary.Write(w, binary.BigEndian, uint32(2))
	binary.Write(w, binary.BigEndian, uint32(len(c.Objects)))
	for _, bs := range c.Objects {
		typeStr, rest, _ := bytes.Cut(bs, []byte(" "))
		_, content, _ := bytes.Cut(rest, []byte("\x00"))
		size, b := len(content), (types[string(typeStr)]<<4)|byte(len(content)&0x0F)
		size >>= 4
		header := []byte{b}
		for size > 0 {
			header[len(header)-1] |= 0x80
			b = byte(size & 0x7F)
			size >>= 7
			header = append(header, b)
		}
		w.Write(header)
		zw := zlib.NewWriter(w)
		zw.Write(content)
		zw.Close()
	}
	sum := sha1.Sum(w.Bytes())
	w.Write(sum[:])
	return w.Bytes(), isEmpty
}

func (c *Commit) buildTree(fs map[string]any) string {
	tree := []byte{}
	for _, name := range slices.Sorted(maps.Keys(fs)) {
		v, entry := fs[name], []byte{}
		if dir, ok := v.(map[string]any); ok {
			subTreeHash := c.buildTree(dir)
			hashBytes, _ := hex.DecodeString(subTreeHash)
			entry = append([]byte(fmt.Sprintf("40000 %s\x00", name)), hashBytes...)
		} else if s, ok := v.(string); ok {
			hashBytes, _ := hex.DecodeString(s)
			entry = append([]byte(fmt.Sprintf("100644 %s\x00", name)), hashBytes...)
		}
		tree = append(tree, entry...)
	}
	treeHash, treeObject := hashObject("tree", tree)
	if _, unchanged := c.Parent.Objects[treeHash]; !unchanged {
		c.Objects[treeHash] = treeObject
	}
	return treeHash
}

func (r *Remote) Push(ref string, c *Commit, packData []byte) error {
	if !strings.Contains(ref, "/") {
		ref = "refs/heads/" + ref
	}
	stdin, stdout, err := r.Exec("git-receive-pack " + r.Repo)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer stdin.Close()
	defer stdout.Close()
	cmd := fmt.Sprintf("%s %s %s\x00report-status", c.Parent.Hash, c.Hash, ref)
	fmt.Fprintf(stdin, "%04x%s0000", len(cmd)+4, cmd)
	if _, err := stdin.Write(packData); err != nil {
		return fmt.Errorf("failed to write pack data: %w", err)
	} else if err := stdin.Close(); err != nil {
		return fmt.Errorf("close stdin: %w", err)
	} else if bs, err := io.ReadAll(stdout); err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	} else if !bytes.Contains(bs, []byte("unpack ok")) {
		return fmt.Errorf("failed to push: %q", bs)
	}
	return nil
}

func (r *Remote) ListRemote() (map[string]string, error) {
	stdin, stdout, err := r.Exec("git-receive-pack " + r.Repo)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer stdin.Close()
	defer stdout.Close()
	refs := make(map[string]string)
	for {
		line, err := r.readPktLine(stdout)
		if err != nil {
			return nil, fmt.Errorf("failed to read pkt line: %w", err)
		} else if line == nil {
			break
		} else if len(line) == 0 {
			continue
		}
		hashAndRef, _, _ := bytes.Cut(bytes.TrimSpace(line), []byte{'\x00'})
		if bytes.HasPrefix(hashAndRef, []byte("ERR")) {
			return nil, fmt.Errorf("remote error: %s", hashAndRef)
		} else if parts := bytes.Fields(hashAndRef); len(parts) >= 2 {
			refs[string(parts[1])] = string(parts[0])
		}
	}
	fmt.Fprintf(stdin, "0000")
	return refs, nil
}

func (r *Remote) Fetch(hashOrRef string) (*Commit, error) {
	hash := hashOrRef
	if !commitHashRe.MatchString(hashOrRef) {
		refs, err := r.ListRemote()
		if err != nil {
			return nil, fmt.Errorf("failed to list refs: %w", err)
		}
		hash = cmp.Or(refs[hashOrRef], refs["refs/heads/"+hashOrRef], emptyHash)
	}
	if hash == emptyHash {
		meta, objects := map[string]string{}, map[string][]byte{}
		return &Commit{hash, meta, objects, map[string]any{}, nil}, nil
	}
	stdin, stdout, err := r.Exec("git-upload-pack "+r.Repo,
		"GIT_PROTOCOL", "version=2")
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer stdout.Close()
	for {
		if line, err := r.readPktLine(stdout); err != nil {
			return nil, fmt.Errorf("failed to read server capabilities: %w", err)
		} else if line == nil {
			break
		}
	}
	// GIT_TRACE=1 GIT_TRACE_PACKET=1 git clone --filter=blob:none --no-checkout --depth 1 ...
	req := &bytes.Buffer{}
	fmt.Fprintf(req, "%04x%s", len("command=fetch")+4, "command=fetch")
	fmt.Fprintf(req, "%04x%s", len("object-format=sha1")+4, "object-format=sha1")
	req.WriteString("0001")
	fmt.Fprintf(req, "%04x%s", len("deepen 1")+4, "deepen 1")
	fmt.Fprintf(req, "%04x%s", len("filter blob:none")+4, "filter blob:none")
	fmt.Fprintf(req, "%04x%s", len("want "+hash)+4, "want "+hash)
	fmt.Fprintf(req, "%04x%s", len("done")+4, "done")
	req.WriteString("0000")
	if _, err := stdin.Write(req.Bytes()); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	} else if err := stdin.Close(); err != nil {
		return nil, fmt.Errorf("failed to close stdin: %w", err)
	}
	for {
		line, err := r.readPktLine(stdout)
		if err != nil {
			return nil, fmt.Errorf("failed to read packfile header: %w", err)
		} else if line == nil {
			return nil, fmt.Errorf("unexpected flush reading packfile header")
		} else if bytes.HasPrefix(line, []byte("packfile")) {
			break
		}
	}
	packData := &bytes.Buffer{}
	for {
		line, err := r.readPktLine(stdout)
		if err == io.EOF || line == nil {
			break
		} else if err != nil {
			return nil, fmt.Errorf("failed to read pack data: %w", err)
		} else if len(line) == 0 {
			continue
		}
		// https://git-scm.com/docs/protocol-capabilities/2.17.0#_side_band_side_band_64k
		switch line[0] {
		case 1:
			packData.Write(line[1:])
		case 2: // progress
		case 3:
			return nil, fmt.Errorf("fatal error: %s", string(line[1:]))
		default:
			log.Printf("unknown sideband: %d", line[0])
		}
	}
	objects, err := r.parsePack(packData.Bytes())
	if err != nil {
		return nil, fmt.Errorf("failed to parse pack: %w", err)
	}
	c, err := r.parseCommit(hash, objects)
	if err != nil {
		return nil, fmt.Errorf("failed to parse commit: %w", err)
	}
	c.FS = c.expandTreeBlobs(c.Objects[c.Meta["tree"]])
	return c, nil
}

func (c *Commit) expandTreeBlobs(bs []byte) map[string]any {
	fs := map[string]any{}
	// expand blobs in subdirectories to allow detecting existing unchanged blobs in Add
	for len(bs) > 0 {
		// https://git-scm.com/book/en/v2/Git-Internals-Git-Objects
		// <mode> <name>\x00<20-byte_hash>
		mode, rest, _ := bytes.Cut(bs, []byte{' '})
		name, rest, _ := bytes.Cut(rest, []byte{'\x00'})
		if len(rest) < 20 {
			break
		} else if hash := hex.EncodeToString(rest[:20]); string(mode) == "40000" {
			fs[string(name)] = c.expandTreeBlobs(c.Objects[hash])
		} else {
			c.Objects[hash] = nil
			fs[string(name)] = hash
		}
		bs = rest[20:]
	}
	return fs
}

func (c *Commit) parseTree(treeHash string) map[string]any {
	fs := map[string]any{}
	for bs := c.Objects[treeHash]; len(bs) > 0; {
		mode, rest, _ := bytes.Cut(bs, []byte{' '})
		name, rest, _ := bytes.Cut(rest, []byte{'\x00'})
		if len(rest) < 20 {
			break
		}
		hash := hex.EncodeToString(rest[:20])
		if string(mode) == "40000" {
			fs[string(name)] = c.parseTree(hash)
		} else {
			fs[string(name)] = hash
		}
		bs = rest[20:]
	}
	return fs
}

func (r *Remote) parsePack(data []byte) (map[string][]byte, error) {
	if len(data) < 12 || string(data[:4]) != "PACK" {
		return nil, fmt.Errorf("invalid pack: %q(%d)", string(data[:4]), len(data))
	}
	nObjects, rdr := binary.BigEndian.Uint32(data[8:12]), bytes.NewReader(data[12:])
	m, types := map[string][]byte{}, map[byte]string{
		1: "commit", 2: "tree", 3: "blob", 4: "tag", 7: "ref_delta",
	}
	for i := 0; i < int(nObjects); i++ {
		b, err := rdr.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("failed to read object type: %w", err)
		}
		t, size, shift := (b>>4)&7, int64(b&0x0F), 4
		if _, ok := types[t]; !ok {
			return nil, fmt.Errorf("unknown object type: %d", t)
		}
		for b&0x80 != 0 {
			if b, err = rdr.ReadByte(); err != nil {
				return nil, fmt.Errorf("failed to read object size: %w", err)
			}
			size |= int64(b&0x7F) << shift
			shift += 7
		}
		if t == 7 {
			// TODO: implement delta; just skip ref hash for now
			// https://git-scm.com/docs/pack-format#_pack_pack_files_have_the_following_format
			if _, err := io.CopyN(io.Discard, rdr, 20); err != nil {
				return nil, fmt.Errorf("failed to skip ref-delta hash: %w", err)
			}
		}
		zr, err := zlib.NewReader(rdr)
		if err != nil {
			return nil, fmt.Errorf("failed to open zlib reader: %w", err)
		}
		bs, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("failed to read zlib: %w", err)
		} else if err := zr.Close(); err != nil {
			return nil, fmt.Errorf("failed to close zlib: %w", err)
		} else if len(bs) != int(size) {
			return nil, fmt.Errorf("failed to read correct size: %d != %d", size, len(bs))
		}
		h := sha1.New()
		fmt.Fprintf(h, "%s %d\x00%s", types[t], size, bs)
		m[hex.EncodeToString(h.Sum(nil))] = bs
	}
	return m, nil
}

func (r *Remote) parseCommit(hash string, objects map[string][]byte) (*Commit, error) {
	m, bs := map[string]string{}, objects[hash]
	if len(bs) == 0 {
		return nil, fmt.Errorf("failed to lookup commit %q", hash)
	}
	hdr, msg, _ := bytes.Cut(bs, []byte("\n\n"))
	m["message"] = string(msg)
	s := bufio.NewScanner(bytes.NewReader(hdr))
	for s.Scan() {
		k, v, _ := strings.Cut(s.Text(), " ")
		m[k] = v
	}
	return &Commit{hash, m, objects, map[string]any{}, nil}, nil
}

func (r *Remote) readPktLine(in io.Reader) ([]byte, error) {
	lbs := make([]byte, 4)
	if _, err := io.ReadFull(in, lbs); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("failed to read length: %w", err)
	}
	l, err := strconv.ParseInt(string(lbs), 16, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse length %q: %w", lbs, err)
	} else if l == 0 {
		return nil, nil
	} else if l <= 4 {
		return []byte{}, nil
	}
	bs := make([]byte, l-4)
	if _, err := io.ReadFull(in, bs); err != nil {
		return nil, fmt.Errorf("failed to read payload: %w", err)
	}
	return bs, nil
}

func (c *SSH) Exec(cmd string, env ...string) (io.WriteCloser, io.ReadCloser, error) {
	session, err := c.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %w", err)
	} else if len(env)%2 != 0 {
		return nil, nil, fmt.Errorf("env kv pairs mismatched: %v", env)
	}
	for i := 0; i+1 < len(env); i += 2 {
		session.Setenv(env[i], env[i+1])
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open stdout: %w", err)
	} else if err := session.Start(cmd); err != nil {
		return nil, nil, fmt.Errorf("failed to start session: %w", err)
	}
	return stdin, &readCloser{stdout, session.Close}, nil
}

func (s *Shell) Exec(cmd string, env ...string) (io.WriteCloser, io.ReadCloser, error) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "git-") {
		return nil, nil, fmt.Errorf("bad command: %q", cmd)
	} else if len(env)%2 != 0 {
		return nil, nil, fmt.Errorf("env kv pairs mismatched: %v", env)
	}
	// https://git-scm.com/docs/git-config
	gitConfigFile := filepath.Join(s.Dir, "gitconfig")
	gitConfig := `[uploadpack] allowFilter = true
                  [receive] denyCurrentBranch = ignore`
	env = append(env, "GIT_CONFIG_SYSTEM", gitConfigFile)
	if err := os.WriteFile(gitConfigFile, []byte(gitConfig), 0644); err != nil {
		return nil, nil, fmt.Errorf("failed to write tmp git config: %w", err)
	}
	c := exec.Command(parts[0], parts[1:]...)
	for i := 0; i+1 < len(env); i += 2 {
		c.Env = append(c.Env, fmt.Sprintf("%s=%s", env[i], env[i+1]))
	}
	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open stdin: %w", err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open stdout: %w", err)
	}
	c.Stderr = os.Stderr
	if err := c.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start command: %w", err)
	}
	return stdin, &readCloser{stdout, c.Wait}, nil
}

func (c *Shell) Close() error { return nil }

func (rc *readCloser) Close() error { return rc.close() }

func hashObject(objType string, content []byte) (string, []byte) {
	object := append([]byte(fmt.Sprintf("%s %d\x00", objType, len(content))), content...)
	hash := sha1.Sum(object)
	return hex.EncodeToString(hash[:]), object
}

func cloneFS(fs map[string]any) map[string]any {
	fs = maps.Clone(fs)
	for k, v := range fs {
		if subFS, ok := v.(map[string]any); ok {
			fs[k] = cloneFS(subFS)
		}
	}
	return fs
}
