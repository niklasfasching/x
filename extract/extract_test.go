package extract

import (
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/niklasfasching/x/snap"
	"github.com/niklasfasching/x/soup"
)

func TestDocument(t *testing.T) {
	m, urls := snap.TXT{Extension: ".html"}, []string{
		"https://de.wikipedia.org/wiki/Go_(Programmiersprache)",                                                                // wiki
		"https://www.nasa.gov/missions/roman-space-telescope/telescope-for-nasas-roman-mission-complete-delivered-to-goddard/", // wordpress
		"https://pmc.ncbi.nlm.nih.gov/articles/PMC4221854/",                                                                    // journal article
		"https://blog.bytebytego.com/p/storing-200-billion-entities-notions",                                                   // substack
		"https://android-developers.googleblog.com/2024/11/android-passkeys-spotlight-week-begins-november-18.html",            // blogspot
	}
	for _, url := range urls {
		k := regexp.MustCompile(`\W+`).ReplaceAllString(url, "_")
		t.Run(k, func(t *testing.T) {
			snap.Snap(t, m, "TestDocument", k, extractContent(t, url))
		})
	}
}

func extractContent(t *testing.T, url string) string {
	n, err := load(url)
	if err != nil {
		t.Fatal("load", err)
	}
	d := &Document{Node: n, URL: url}
	if err := d.Parse(); err != nil {
		t.Fatal("parse", err)
	}
	return format(t, "<h1>"+d.Title+"</h1>"+d.Content)
}

func load(url string) (*soup.Node, error) {
	t := soup.Transport{
		UserAgent:  "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0",
		RetryCount: 5,
		Cache:      &soup.FileCache{Root: "testdata/http"},
	}
	c, err := t.Client()
	if err != nil {
		return nil, err
	}
	return soup.Load(c, url)
}

func format(t *testing.T, html string) string {
	w := &strings.Builder{}
	decoder, encoder := xml.NewDecoder(strings.NewReader(html)), xml.NewEncoder(w)
	encoder.Indent("", "  ")
	for {
		if token, err := decoder.Token(); err == io.EOF {
			encoder.Flush()
			return w.String()
		} else if err != nil {
			t.Log("fmt decode", err)
			return html
		} else if err = encoder.EncodeToken(token); err != nil {
			t.Log("fmt encode", err)
			return html
		}
	}
}
