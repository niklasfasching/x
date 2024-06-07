package mkv

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/niklasfasching/x/util"
)

func TestMKV(t *testing.T) {
	f, err := os.Open("testdata/test.mkv")
	if err != nil {
		t.Fatal("failed to open", err)
	}
	defer f.Close()
	end, errEnd := f.Seek(0, io.SeekEnd)
	_, errStart := f.Seek(0, io.SeekStart)
	if errEnd != nil || errStart != nil {
		t.Fatalf("failed to seek: start(%s), end(%s)", errStart, errEnd)
	}
	m, err := Parse(io.NewSectionReader(f, 0, end))
	if err != nil {
		t.Fatal("failed to parse", err)
	}
	util.Snapshot(t, m)
}

func (m *MKV) MarshalSnap() (string, string, error) {
	for _, c := range m.Segment.Clusters {
		for i, b := range c.SimpleBlocks {
			c.SimpleBlocks[i] = fmt.Sprintf("len:%d", len(b))
		}
		for i, g := range c.BlockGroups {
			c.BlockGroups[i].Block = fmt.Sprintf("len:%d", len(g.Block))
		}
	}
	bs, err := json.MarshalIndent(m, "", "  ")
	return string(bs), ".json", err
}
