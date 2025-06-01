package mkv

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/niklasfasching/x/format/bits"
)

type SubWriter struct {
	MKV      *MKV
	tracks   map[uint]string
	tmp      bytes.Buffer
	elemSize int
	dropElem bool
	subs     chan []*Sub
}

type Sub struct {
	ID         int
	Start, End time.Duration
	Text, Lang string
}

var maxEBMLVarIntLen = 16

func NewSubWriter(m *MKV, subs chan []*Sub) *SubWriter {
	return &SubWriter{MKV: cmp.Or(m, &MKV{}), subs: subs}
}

func (w *SubWriter) Write(bs []byte) (int, error) {
	return len(bs), w.write(bs)
}

func (w *SubWriter) SeekTo(off int64) error {
	if lc, lt := len(w.MKV.Segment.Cues.Points), len(w.MKV.Segment.Tracks.Entries); lc == 0 || lt == 0 {
		return fmt.Errorf("must not be empty: cues=%d tracks=%d", lc, lt)
	}
	w.elemSize, w.dropElem = math.MaxInt64, true
	w.tmp.Reset()
	for _, cp := range w.MKV.Segment.Cues.Points {
		for _, p := range cp.Positions {
			i := w.MKV.Segment.DataOffset + int64(p.ClusterPosition)
			if i > off {
				w.elemSize, w.dropElem = int(i-off), true
				return nil
			}
		}
	}
	return nil
}

func (w *SubWriter) write(bs []byte) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = e.(error)
		}
	}()
	const segmentID, clusterID, tracksID, infoID, cuesID = "18538067", "1F43B675", "1654AE6B", "1549A966", "1C53BB6B"
	if c := min(len(bs), w.elemSize); w.elemSize > 0 && w.dropElem {
		bs = bs[c:]
		w.elemSize -= c
	}
	if w.tmp.Write(bs); w.tmp.Len() < max(2*maxEBMLVarIntLen, w.elemSize) {
		return nil
	}
	e := &EBML{R: bits.New(bytes.NewReader(w.tmp.Bytes()), binary.BigEndian, 0)}
	id, size, l := e.Next()
	// the elements we're interested in are direct children of the segment element.
	// when !seekable we start outside the segment and need to enter it;
	// once inside the segment, we just need to skip all irrelevant elements and parse
	// our way from element to element
	if id == segmentID {
		w.tmp.Next(int(l))
		return w.write(nil)
	} else if elemSize := int(l + size); w.tmp.Len() < elemSize {
		w.elemSize, w.dropElem = elemSize-w.tmp.Len(), id != clusterID && id != tracksID && id != infoID && id != cuesID
		if w.dropElem {
			w.tmp.Next(int(elemSize))
		}
		return nil
	}
	w.dropElem, w.elemSize = false, 0
	switch id {
	case infoID:
		parseElem(e, &w.MKV.Segment.Info, size, l)
		w.tmp.Next(int(e.Off))
	case tracksID:
		if len(w.MKV.Segment.Tracks.Entries) == 0 {
			parseElem(e, &w.MKV.Segment.Tracks, size, l)
		}
		w.tmp.Next(int(e.Off))
	case cuesID:
		if len(w.MKV.Segment.Cues.Points) == 0 {
			parseElem(e, &w.MKV.Segment.Cues, size, l)
		}
		w.tmp.Next(int(e.Off))
	case clusterID:
		w.parseCluster(e, size, l)
		w.tmp.Next(int(e.Off))
	default:
		w.tmp.Next(int(l + size))
	}
	return w.write(nil)
}

func (w *SubWriter) parseCluster(e *EBML, size, l int64) {
	c := parseElem(e, &Cluster{}, size, l)
	if w.tracks == nil {
		if len(w.MKV.Segment.Tracks.Entries) == 0 {
			panic(fmt.Errorf("invalid/empty mkv"))
		}
		w.tracks = map[uint]string{}
		for _, t := range w.MKV.Segment.Tracks.Entries {
			if t.Type == 17 {
				w.tracks[t.ID] = cmp.Or(t.Lang, t.LangBCP47, "en")
			}
		}
	}
	subs, timeScale := []*Sub{}, float64(w.MKV.Segment.Info.TimeScale)
	for _, sb := range c.SimpleBlocks {
		if sub := w.parseBlock([]byte(sb), float64(c.Time), 0, timeScale); sub != nil {
			subs = append(subs, sub)
		}
	}
	for _, bg := range c.BlockGroups {
		if sub := w.parseBlock([]byte(bg.Block), float64(c.Time), float64(bg.Duration), timeScale); sub != nil {
			subs = append(subs, sub)
		}
	}
	if len(subs) == 0 || w.subs == nil {
		return
	}
	select {
	case w.subs <- subs:
	default:
	}
}

func (w *SubWriter) parseBlock(bs []byte, t, dur, scale float64) *Sub {
	if len(bs) < maxEBMLVarIntLen {
		return nil
	}
	e := &EBML{R: bits.New(bytes.NewReader(bs), binary.BigEndian, 0)}
	trackID, _ := e.Size()
	if lang, ok := w.tracks[uint(trackID)]; ok {
		to := int16(binary.BigEndian.Uint16(e.Bytes(2)))
		tt := time.Duration((t + float64(to)) * scale)
		td := time.Duration(dur * scale)
		// TODO: parse the subtitle string rather than forwarding it raw
		return &Sub{int(trackID), tt, (tt + td), string(bs[e.Off+1:]), lang}
	}
	return nil
}

func parseElem[V any](e *EBML, v *V, size, l int64) *V {
	rv := reflect.ValueOf(v).Elem()
	e.Unmarshal("/", rv, e.Off+size, e.IDs("/", rv, l, e.Off+size))
	return v
}
