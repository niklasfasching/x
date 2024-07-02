package soup

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Cache interface {
	Init() error
	Key(*http.Request) (string, error)
	Get(string, *http.Request) (*http.Response, error)
	Set(string, *http.Request, *http.Response) error
}

type Transport struct {
	Transport   http.RoundTripper
	RetryCount  int
	RateLimiter <-chan time.Time
	Cache       Cache
	UserAgent   string
	OnReq       func(*http.Request)
}

type FileCache struct{ Root string }

var invalidFileNameChars = regexp.MustCompile(`[^-_0-9a-zA-Z]+`)

func (t Transport) Client() (*http.Client, error) {
	if t.Transport == nil {
		t.Transport = http.DefaultTransport
	}
	c := &http.Client{Transport: &t}
	if t.Cache == nil {
		return c, nil
	}
	return c, t.Cache.Init()
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	k := ""
	if t.Cache != nil {
		key, err := t.Cache.Key(req)
		if err != nil {
			return nil, err
		}
		k = key
		if res, err := t.Cache.Get(k, req); res != nil || (err != nil && !os.IsNotExist(err)) {
			return res, err
		}
	}
	if t.OnReq != nil {
		t.OnReq(req)
	}
	if t.UserAgent != "" {
		req.Header.Set("User-Agent", t.UserAgent)
	}
	if t.RateLimiter != nil {
		<-t.RateLimiter
	}
	res, err := t.Transport.RoundTrip(req)
	for i := 0; i < t.RetryCount && (err != nil || res.StatusCode >= 400); i++ {
		if t.RateLimiter != nil {
			<-t.RateLimiter
		}
		res, err = t.Transport.RoundTrip(req)
	}
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 400 && t.Cache != nil {
		if err := t.Cache.Set(k, req, res); err != nil {
			log.Println("ERROR: Cache.Set ", err)
		}
	}
	return res, nil
}

func (c *FileCache) Key(req *http.Request) (string, error) {
	key := fmt.Sprintf("%s_%s_%s", req.Method, req.URL.Host, req.URL.Path)
	key = invalidFileNameChars.ReplaceAllString(key, "_")
	if len(key) > 40 {
		key = key[:40]
	}
	hash := sha1.New()
	hash.Write([]byte(req.Method + "::" + req.URL.String()))
	if req.Body != nil {
		bs, err := io.ReadAll(req.Body)
		if err != nil {
			return "", err
		}
		req.Body.Close()
		req.Body = ioutil.NopCloser(bytes.NewBuffer(bs))
		hash.Write(bs)
	}
	return filepath.Join(c.Root, key+hex.EncodeToString(hash.Sum(nil))), nil
}

func (c *FileCache) Init() error { return os.MkdirAll(c.Root, os.ModePerm) }

func (c *FileCache) Get(k string, req *http.Request) (*http.Response, error) {
	bs, err := os.ReadFile(k)
	if err != nil {
		return nil, err
	}
	bs = bytes.SplitN(bs, []byte("\n"), 2)[1]
	res, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(bs)), req)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (c *FileCache) Set(k string, req *http.Request, res *http.Response) error {
	bs, err := httputil.DumpResponse(res, true)
	if err != nil {
		return err
	}
	u, err := url.PathUnescape(req.URL.String())
	if err != nil {
		u = req.URL.String()
	}
	bs = append([]byte(u+"\n"), bs...)
	return os.WriteFile(k, bs, os.ModePerm)
}
