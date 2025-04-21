package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
)

type MultiPolygon struct {
	Properties map[string]any
	Polygons   [][]Point
	BBox       [2]Point
}

// geojson

type Collection struct {
	Type     string     `json:"type"`
	Features []*Feature `json:"features"`
}

type Feature struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
	Geometry   *Geometry      `json:"geometry"`
}

type Geometry struct {
	Type        string      `json:"type"`
	Coordinates [][][]Point `json:"coordinates"` // [lon,lat]
}

type Point [2]float64

func KML(verordnungenDir, areasDir, outFile string) error {
	w := &strings.Builder{}
	w.WriteString(`<?xml version="1.0" encoding="utf-8" ?>
       <kml xmlns="http://www.opengis.net/kml/2.2">
         <Document>`)

	w.WriteString(`<Schema name="x" id="x">
      <SimpleField name="name" type="string"></SimpleField>
    </Schema>`)
	w.WriteString(`<Folder><name>x</name>`)

	yMin, xMin := 12.415063, 52.147007
	yMax, xMax := 14.255730, 52.947342
	bbox := [2]Point{{xMin, yMin}, {xMax, yMax}}

	fs, err := os.ReadDir(verordnungenDir)
	if err != nil {
		return err
	}
	vm := map[string]string{}
	for _, f := range fs {
		bs, err := os.ReadFile(filepath.Join(verordnungenDir, f.Name()))
		if err != nil {
			return err
		}
		text := string(bs)
		header := strings.SplitN(text, "\n", 2)[0]
		if strings.Contains(header, "Verordnung über das Landschaftsschutzgebiet") {
			name := strings.Split(header, "Landschaftsschutzgebiet")[1]
			name = strings.Replace(name, `„`, "", -1)
			name = strings.Replace(name, `“`, "", -1)
			name = strings.TrimSpace(name)
			vm[name] = "https://bravors.brandenburg.de/de/" + f.Name()[:len(f.Name())-len(".txt")] + "\n" + text
		}
	}

	fs, err = os.ReadDir(areasDir)
	if err != nil {
		return err
	}
	for _, f := range fs {
		if strings.Contains(f.Name(), "Fauna") {
			continue
		}

		// colors are reverse wtf. ABGR...
		red, yellow, green := "553900C7", "5500C3FF", "55A6F7DA"
		kind, color := "", red
		switch strings.Split(f.Name(), ":")[1] {
		case "Biosphaerenreservate.json":
			kind, color = "Biosphaerenreservat", yellow
		case "Fauna_Flora_Habitat_Gebiete.json":
			kind, color = "Fauna-Flora-Habitat", yellow
		case "Landschaftsschutzgebiete.json":
			kind, color = "Landschaftsschutzgebiet", green
		case "Nationale_Naturmonumente.json":
			kind, color = "Naturmonument", red
		case "Nationalparke.json":
			kind, color = "Nationalpark", red
		case "Naturschutzgebiete.json":
			kind, color = "Naturschutzgebiet", red
		case "Vogelschutzgebiete.json":
			kind, color = "Vogelschutzgebiet", red
		default:
			log.Println("Skipping", f.Name())
			continue
		}

		bs, err := os.ReadFile(filepath.Join(areasDir, f.Name()))
		if err != nil {
			return err
		}
		c := Collection{}
		if err := json.Unmarshal(bs, &c); err != nil {
			return err
		}

		for i, f := range c.Features {
			if i > 1000 {
				break
			}
			m := f.ToMultiPolygon()
			if !(m.BBox[0][0] < bbox[1][0] && m.BBox[1][0] > bbox[0][0] &&
				m.BBox[0][1] < bbox[1][1] && m.BBox[1][1] > bbox[0][1]) {
				continue
			}
			w.WriteString(fmt.Sprintf(`<Placemark><Style><PolyStyle><color>%s</color><fill>1</fill><outline>1</outline></PolyStyle></Style>`, color))
			name := f.Properties["NAME"].(string)
			title := kind + ": " + name
			if kind == "Landschaftsschutzgebiet" {
				text, ok := vm[name]
				if ok {
					url := strings.Split(strings.SplitN(text, "\n", 1)[0], " ")[0]
					title = fmt.Sprintf("%s (%v) - %s", title, strings.Contains(text, "lagern"), url)
				}
			}
			fmt.Fprintf(w, `<name>%s</name>`, title)
			w.WriteString(`<Polygon><outerBoundaryIs><LinearRing><tessellate>1</tessellate><coordinates>`)
			for _, p := range m.Polygons[0] {
				fmt.Fprintf(w, "%f,%f ", p[0], p[1])
			}
			w.WriteString(`</coordinates></LinearRing></outerBoundaryIs></Polygon></Placemark>`)
		}
	}

	w.WriteString(`</Folder></Document></kml>`)
	return os.WriteFile(outFile, []byte(w.String()), 0644)
}

func (f *Feature) ToMultiPolygon() *MultiPolygon {
	xMin, xMax, yMin, yMax := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	m := &MultiPolygon{Properties: f.Properties}
	for i, ps := range f.Geometry.Coordinates {
		pg := []Point{}
		// simplify to stay below 5MB google maps limit
		// https://www.google.com/maps/d
		for _, p := range Simplify(ps[0], 0.000_1) {
			x, y := p[0], p[1]
			if i == 0 {
				xMin, xMax = math.Min(xMin, x), math.Max(xMax, x)
				yMin, yMax = math.Min(yMin, y), math.Max(yMax, y)
			}
			pg = append(pg, Point{y, x})
		}
		m.Polygons = append(m.Polygons, pg)
	}
	m.BBox = [2]Point{{xMin, yMin}, {xMax, yMax}}
	return m
}

// https://dyn4j.org/2021/06/2021-06-10-simple-polygon-simplification/
// Vertex Cluster Reduction
func Simplify(ps []Point, threshold float64) []Point {
	ps2, ref := []Point{ps[0]}, ps[0]
	for _, p := range ps[1:] {
		dx := p[0] - ref[0]
		dy := p[1] - ref[1]
		d := math.Hypot(dx, dy)
		if d > threshold {
			ref, ps2 = p, append(ps2, p)
		}
	}
	return ps2
}
