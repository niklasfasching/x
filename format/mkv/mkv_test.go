package mkv

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/niklasfasching/x/snap"
)

func TestMKV(t *testing.T) {
	ensureTestMKV(t, "testdata/test.mkv")
	f, err := os.Open("testdata/test.mkv")
	if err != nil {
		t.Fatal("failed to open", err)
	}
	defer f.Close()
	m, err := Parse(f, 0)
	if err != nil {
		t.Fatal("failed to parse", err)
	}
	for _, c := range m.Segment.Clusters {
		for i, b := range c.SimpleBlocks {
			c.SimpleBlocks[i] = fmt.Sprintf("len:%d", len(b))
		}
		for i, g := range c.BlockGroups {
			c.BlockGroups[i].Block = fmt.Sprintf("len:%d", len(g.Block))
		}
	}
	snap.Snap(t, snap.JSON{}, m)
}

func ensureTestMKV(t *testing.T, filename string) {
	t.Helper()
	if _, err := os.Stat(filename); err == nil {
		return
	}
	colors, l := []string{"red", "green", "blue"}, 20
	videoFilterParts, videoInputs, srt := []string{}, "", ""
	for i := range l {
		c := colors[i%len(colors)]
		videoFilterParts = append(videoFilterParts, fmt.Sprintf(
			"color=c=%s:s=480x240:d=1,format=yuv420p[c%d]",
			c, i,
		))
		videoInputs += fmt.Sprintf("[c%d]", i)
		srt += fmt.Sprintf("%d\n00:00:%02d,000 --> 00:00:%02d,000\n%s\n\n", i+1, i, i+1, c)
	}
	args := []string{
		"-i", "pipe:",
		"-filter_complex", fmt.Sprintf(
			"%s; %sconcat=n=%d:v=1:a=0[v_out]; sine=frequency=1000:duration=%d[a_out]",
			strings.Join(videoFilterParts, "; "),
			videoInputs, l, l),
		"-map", "[v_out]",
		"-map", "[a_out]",
		"-map", "0:s",
		"-c:v", "libx264",
		"-c:a", "aac",
		filename,
		"-y",
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdin, cmd.Stderr = strings.NewReader(srt), os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}
