package container

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Registry struct {
	TTL                                  time.Duration
	APIBaseURL, AuthBaseURL, AuthService string
}

type Layer struct{ MediaType, Digest string }

var DockerRegistry = Registry{
	TTL:         -1,
	APIBaseURL:  "https://registry-1.docker.io",
	AuthBaseURL: "https://auth.docker.io",
	AuthService: "registry.docker.io",
}

func (r *Registry) Pull(image, dir string) (bool, error) {
	imageFile, now := filepath.Join(dir, LayerInfoFile), time.Now()
	if f, err := os.Stat(imageFile); err == nil && (now.Sub(f.ModTime()) < r.TTL || r.TTL == -1) {
		return false, nil
	}
	ps := strings.SplitN(image, ":", 2)
	repo, tag := ps[0], ps[1]
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	} else if token, err := r.Authenticate(repo); err != nil {
		return false, err
		// TODO: pass env config
	} else if ls, err := r.GetLayers(token, repo, tag, runtime.GOARCH, runtime.GOOS); err != nil {
		return false, err
	} else if changed, err := r.Download(token, repo, dir, imageFile, ls); err != nil {
		return false, err
	} else {
		return changed, nil
	}
}

func (r *Registry) Download(token, repo, dir, imageFile string, ls []Layer) (bool, error) {
	if imageBS, err := json.MarshalIndent(ls, "", "  "); err != nil {
		return false, err
	} else if bs, _ := os.ReadFile(imageFile); string(bs) == string(imageBS) {
		return false, nil
	} else if err := os.RemoveAll(dir); err != nil {
		return false, err
	} else if err := os.MkdirAll(dir, 0755); err != nil {
		return false, err
	} else if err := os.WriteFile(imageFile, imageBS, 0644); err != nil {
		return false, err
	}
	for _, l := range ls {
		url := fmt.Sprintf("%s/v2/library/%s/blobs/%s", r.APIBaseURL, repo, l.Digest)
		body, err := r.Get(url, l.MediaType, token)
		if err != nil {
			return false, err
		} else if err := r.Extract(dir, body); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (r *Registry) Extract(dir string, rc io.ReadCloser) error {
	defer rc.Close()
	gr, err := gzip.NewReader(rc)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr, j := tar.NewReader(gr), filepath.Join
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.Mkdir(j(dir, h.Name), 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			f, err := os.OpenFile(j(dir, h.Name), os.O_RDWR|os.O_CREATE|os.O_TRUNC,
				fs.FileMode(h.Mode))
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			f.Close()
		case tar.TypeLink:
			if err := os.Link(j(dir, h.Linkname), j(dir, h.Name)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.Symlink(h.Linkname, j(dir, h.Name)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown type: %s %s", string(h.Typeflag), h.Name)
		}
	}
}

func (r *Registry) GetLayers(token, repo, tag, arch, os string) ([]Layer, error) {
	v := struct {
		Manifests []struct {
			Digest, MediaType string
			Platform          struct {
				Architecture, Os string
			}
		}
		Layers []Layer
	}{}
	url := fmt.Sprintf("%s/v2/library/%s/manifests/%s", r.APIBaseURL, repo, tag)
	if err := r.GetJSON(url, "vnd.docker.distribution.manifest.v2+json", token, &v); err != nil {
		return nil, err
	}
	if v.Layers != nil {
		return v.Layers, nil
	}
	digest := ""
	for _, m := range v.Manifests {
		if m.Platform.Architecture == arch && m.Platform.Os == os &&
			m.MediaType == "application/vnd.oci.image.manifest.v1+json" {
			digest = m.Digest
		}
	}
	if digest == "" {
		return nil, fmt.Errorf("manifest v1 sha not found: %v", v)
	}
	lV := struct {
		Layers []Layer
		Config struct{ Digest string }
	}{}
	lUrl := fmt.Sprintf("%s/v2/library/%s/manifests/%s", r.APIBaseURL, repo, digest)
	if err := r.GetJSON(lUrl, "vnd.oci.image.manifest.v1+json", token, &lV); err != nil {
		return nil, err
	}
	cV := struct {
		Config struct {
			Env []string
		}
	}{}
	if lV.Config.Digest != "" {
		url := fmt.Sprintf("%s/v2/library/%s/blobs/%s", r.APIBaseURL, repo, lV.Config.Digest)
		if err := r.GetJSON(url, "vnd.docker.distribution.manifest.v2+json", token, &cV); err != nil {
			return nil, err
		}
	}
	return lV.Layers, nil
}

func (r *Registry) Authenticate(repo string) (string, error) {
	url := fmt.Sprintf("%s/token?scope=repository:library/%s:pull&service=%s",
		r.AuthBaseURL, repo, r.AuthService)
	v := struct{ Token string }{}
	if err := r.GetJSON(url, "json", "", &v); err != nil {
		return "", err
	}
	return v.Token, nil
}

func (r *Registry) GetJSON(url, accept, token string, v any) error {
	body, err := r.Get(url, accept, token)
	if err != nil {
		return err
	}
	defer body.Close()
	if bs, err := io.ReadAll(body); err != nil {
		return err
	} else if err := json.Unmarshal(bs, &v); err != nil {
		return err
	}
	return nil
}

func (r *Registry) Get(url, accept, token string) (io.ReadCloser, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/"+accept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	} else if res.StatusCode >= 300 {
		return nil, fmt.Errorf("bad status: %v", res.StatusCode)
	}
	return res.Body, err
}
