package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/niklasfasching/x/soup"
)

func protectedAreas(dir string) error {
	if err := os.RemoveAll(dir); !os.IsNotExist(err) && err != nil {
		return err
	} else if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}
	t := soup.Transport{
		UserAgent:  "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:127.0) Gecko/20100101 Firefox/127.0",
		RetryCount: 5,
	}
	c, err := t.Client()
	if err != nil {
		return err
	}
	baseURL := "https://geodienste.bfn.de/ogc/wfs/schutzgebiet?SERVICE=WFS&VERSION=2.0.0"
	r, err := c.Get(baseURL + "&Request=GetCapabilities")
	if err != nil {
		return err
	}
	if r.StatusCode >= 400 {
		return fmt.Errorf("status: %v", r.Status)
	}
	defer r.Body.Close()
	x := struct {
		Types []string `xml:"FeatureTypeList>FeatureType>Name"`
	}{}
	if err := xml.NewDecoder(r.Body).Decode(&x); err != nil {
		return err
	}
	for _, t := range x.Types {
		// TODO: remove bbox, simplify EPSG reference
		url := baseURL + fmt.Sprintf("&Request=GetFeature&TYPENAMES=%s&outputFormat=geojson&srsName=urn:ogc:def:crs:EPSG::4326", t)
		log.Println("Fetching", t, url)
		r, err := c.Get(url)
		if err != nil {
			return err
		}
		defer r.Body.Close()
		bs, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, t+".json"), bs, 0644); err != nil {
			return err
		}
	}
	return nil
}
