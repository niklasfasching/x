package m3u

import (
	"fmt"
	"strings"
	"time"
)

type Entry struct {
	Name, URL, IMG string
	Duration       time.Duration
}

func New(entries []Entry) string {
	w := &strings.Builder{}
	w.WriteString("#EXTM3U\n")
	for _, e := range entries {
		d := -1
		if ed := e.Duration.Minutes(); ed != 0 {
			d = int(ed) * 60
		}
		fmt.Fprintf(w, "#EXTINF:%d,%s\n", d, e.Name)
		if e.IMG != "" {
			fmt.Fprintf(w, "#EXTIMG:%s\n", e.IMG)
		}
		fmt.Fprintf(w, "%s\n", e.URL)
	}
	return w.String()
}
