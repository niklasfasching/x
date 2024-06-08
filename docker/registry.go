package docker

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
	"strings"
)

type DefaultRegistry struct{}

type Layer struct{ MediaType, Digest string }

func (r *DefaultRegistry) Pull(image, dir string) error {
	ps := strings.SplitN(image, ":", 2)
	repo, tag := ps[0], ps[1]
	token, err := r.Authenticate(repo)
	if err != nil {
		return err
	}
	ls, err := r.GetLayers(token, repo, tag, "amd64", "linux")
	if err != nil {
		return err
	}
	if err := r.Download(token, repo, dir, ls); err != nil {
		return err
	}
	return nil
}

func (r *DefaultRegistry) Download(token, repo, dir string, ls []Layer) error {
	for _, l := range ls {
		url := fmt.Sprintf("https://registry-1.docker.io/v2/library/%s/blobs/%s", repo, l.Digest)
		body, err := r.Get(url, l.MediaType, token)
		if err != nil {
			return err
		} else if err := r.Extract(dir, body); err != nil {
			return err
		}
	}
	return nil
}

func (r *DefaultRegistry) Extract(dir string, rc io.ReadCloser) error {
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
			f, err := os.OpenFile(j(dir, h.Name), os.O_RDWR|os.O_CREATE|os.O_TRUNC, fs.FileMode(h.Mode))
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

func (r *DefaultRegistry) GetLayers(token, repo, tag, arch, os string) ([]Layer, error) {
	v := struct {
		Manifests []struct {
			Digest, MediaType string
			Platform          struct {
				Architecture, Os string
			}
		}
		Layers []Layer
	}{}
	url := fmt.Sprintf("https://registry-1.docker.io/v2/library/%s/manifests/%s", repo, tag)
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
	lV := struct{ Layers []Layer }{}
	lUrl := fmt.Sprintf("https://registry-1.docker.io/v2/library/%s/manifests/%s", repo, digest)
	err := r.GetJSON(lUrl, "vnd.oci.image.manifest.v1+json", token, &lV)
	return lV.Layers, err
}

func (r *DefaultRegistry) Authenticate(repo string) (string, error) {
	tokenURL := "https://auth.docker.io/token?scope=repository:library/%s:pull&service=registry.docker.io"
	v := struct{ Token string }{}
	if err := r.GetJSON(fmt.Sprintf(tokenURL, repo), "json", "", &v); err != nil {
		return "", err
	}
	return v.Token, nil
}

func (r *DefaultRegistry) GetJSON(url, accept, token string, v any) error {
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

func (r *DefaultRegistry) Get(url, accept, token string) (io.ReadCloser, error) {
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
