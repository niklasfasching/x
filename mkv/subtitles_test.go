package mkv

import (
	"context"
	"io"
	"os"
	"testing"

	"github.com/niklasfasching/x/util"
)

func TestCueStreamer(t *testing.T) {
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
	r, err := NewCueStreamer(io.NewSectionReader(f, 0, end))
	if err != nil {
		t.Fatal("failed to create cue reader", err)
	}
	cues := []Cue{}
	ctx, c := context.WithCancel(context.Background())
	defer c()
	go func() {
		r.Sub(ctx, "_", func(c Cue) {
			cues = append(cues, c)
		})
	}()

	if _, err := io.CopyN(io.Discard, r, end/3); err != nil {
		t.Fatal("failed to discard", err)
	}
	cues1 := cues[:]
	if _, err := io.CopyN(io.Discard, r, end/10); err != nil {
		t.Fatal("failed to discard", err)
	}
	cues2 := cues[:]
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal("failed to discard", err)
	}
	cues3 := cues[:]
	util.Snapshot(t, []any{cues1, cues2, cues3})
}
