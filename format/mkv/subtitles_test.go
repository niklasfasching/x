package mkv

import (
	"io"
	"os"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func testSubWriter(t *testing.T, cued bool) {
	ensureTestMKV(t, "testdata/test.mkv")
	f, err := os.Open("testdata/test.mkv")
	if err != nil {
		t.Fatal("failed to open", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal("failed to stat", err)
	}
	c, nextSubs := make(chan []*Sub, 10), []*Sub{}
	go func() {
		for subs := range c {
			nextSubs = append(nextSubs, subs...)
		}
	}()
	m := &MKV{}
	if cued {
		m, err = Parse(f, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
	}
	r := NewSubWriter(m, c)
	if _, err := io.CopyN(r, f, fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	subs1, nextSubs := nextSubs[:], []*Sub{}
	if _, err := io.CopyN(r, f, fi.Size()/3); err != nil {
		t.Fatal(err)
	}
	subs2, nextSubs := nextSubs[:], []*Sub{}
	if _, err := io.Copy(r, f); err != nil {
		t.Fatal(err)
	}
	subs3 := nextSubs[:]
	snap.Snap(t, snap.JSON{}, map[string]any{
		"mkv": map[string]any{
			"info":   r.MKV.Segment.Info,
			"cues":   len(r.MKV.Segment.Cues.Points),
			"tracks": r.MKV.Tracks(),
		},
		"subs": []any{subs1, subs2, subs3},
	})
}

func TestSubWriter(t *testing.T) {
	t.Run("cued", func(t *testing.T) { testSubWriter(t, true) })
	t.Run("uncued", func(t *testing.T) { testSubWriter(t, false) })
}
