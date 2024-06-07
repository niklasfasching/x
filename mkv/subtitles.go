package mkv

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"
)

type CueStreamer struct {
	*MKV
	Tracks   map[uint]string
	off      int64
	cluster  *Cluster
	tmp      bytes.Buffer
	cues     []Cue
	subs     map[string]func(Cue)
	subsLock sync.Mutex
}

type Cue struct {
	ID         int
	Start, End time.Duration
	Text       string
}

func NewCueStreamer(r SizedReaderAt) (*CueStreamer, error) {
	m, err := Parse(r, "Info", "Tracks", "Cues")
	if err != nil {
		return nil, err
	}
	ts := map[uint]string{}
	for _, t := range m.Segment.Tracks.Entries {
		if strings.HasPrefix(t.CodecID, "S_TEXT/") {
			ts[t.ID] = fmt.Sprintf("%s:%s", t.Name, t.Lang)
		}
	}
	return &CueStreamer{MKV: m, Tracks: ts, subs: map[string]func(Cue){}, cluster: &Cluster{}}, nil
}

func (r *CueStreamer) Sub(c context.Context, k string, f func(Cue)) {
	for _, c := range r.cues {
		f(c)
	}
	r.subsLock.Lock()
	r.subs[k] = f
	r.subsLock.Unlock()
	defer func() {
		r.subsLock.Lock()
		delete(r.subs, k)
		r.subsLock.Unlock()
	}()
	<-c.Done()
}

func (r *CueStreamer) Read(bs []byte) (int, error) {
	c, err := r.MKV.ReadAt(bs, r.off)
	if err := r.push(bs); err != nil {
		log.Println("cueReader push failed", r.off, err)
		r.cluster = nil
	}
	r.off += int64(c)
	return c, err
}

func (r *CueStreamer) push(bs []byte) error {
	if r.cluster == nil {
		return nil
	} else if err := r.findCluster(r.off); err != nil {
		return err
	} else if r.cluster.Offset > r.off+int64(len(bs)) {
		return nil
	}
	if _, err := r.tmp.Write(bs[max(0, r.cluster.Offset-r.off):]); err != nil {
		return err
	} else if r.cluster.Size == 0 && r.tmp.Len() > 100 {
		e := &EBML{SizedReaderAt: bytes.NewReader(r.tmp.Bytes())}
		id, size, l := e.Next()
		if r.tmp.Next(int(l)); id == "1F43B675" {
			r.cluster.Size = size
		} else {
			return fmt.Errorf("not a cluster: %s", id)
		}
	} else if r.cluster.Size != 0 && int64(r.tmp.Len()) >= r.cluster.Size {
		e, v := &EBML{SizedReaderAt: bytes.NewReader(r.tmp.Bytes())}, reflect.ValueOf(r.cluster).Elem()
		e.Unmarshal("/", v, r.cluster.Size, e.IDs("/", v, 0, r.cluster.Size))
		r.tmp.Next(int(r.cluster.Size))
		r.subsLock.Lock()
		fs := r.subs
		r.subsLock.Unlock()
		fmtBlock := func(bs []byte, t, dur, scale float64) {
			e := &EBML{SizedReaderAt: bytes.NewReader(bs)}
			trackID, _ := e.Size()
			if _, ok := r.Tracks[uint(trackID)]; ok {
				to := int16(binary.BigEndian.Uint16(e.Bytes(2)))
				tt := time.Duration((t + float64(to)) * scale)
				td := time.Duration(dur * scale)
				e.Bytes(1) // flag
				c := Cue{int(trackID), tt, (tt + td), string(bs[e.Off:])}
				r.subsLock.Lock()
				r.cues = append(r.cues, c)
				r.subsLock.Unlock()
				for _, f := range fs {
					f(c)
				}
			}
		}
		timeScale := float64(r.MKV.Segment.Info.TimeScale)
		for _, sb := range r.cluster.SimpleBlocks {
			fmtBlock([]byte(sb), float64(r.cluster.Time), 0, timeScale)
		}
		for _, bg := range r.cluster.BlockGroups {
			fmtBlock([]byte(bg.Block), float64(r.cluster.Time), float64(bg.Duration), timeScale)
		}
		r.cluster = &Cluster{Offset: r.cluster.Offset + r.cluster.Size}
	}
	return nil
}

func (r *CueStreamer) Seek(off int64, whence int) (int64, error) {
	if whence == io.SeekStart {
		r.off = off
	} else if whence == io.SeekCurrent {
		r.off += off
	} else if whence == io.SeekEnd {
		r.off = r.SizedReaderAt.Size() + off
	} else {
		return -1, fmt.Errorf("bad seek: %v", whence)
	}
	r.cluster, r.tmp = &Cluster{}, bytes.Buffer{}
	return r.off, nil
}

func (r *CueStreamer) findCluster(off int64) error {
	if r.cluster.Offset != 0 {
		return nil
	}
	cps := r.MKV.Segment.Cues.Points
cues:
	for _, c := range cps {
		for _, p := range c.Positions {
			i := r.MKV.Segment.DataOffset + int64(p.ClusterPosition)
			if _, ok := r.Tracks[p.TrackID]; ok && (r.cluster.Offset == 0 || i < off) {
				r.cluster.Offset = i
			} else if ok {
				break cues
			}
		}
	}

	if r.cluster.Offset == 0 {
		return fmt.Errorf("cluster start not found @%v (%v)", r.off, len(cps))
	} else if d, mb := r.off-r.cluster.Offset, int64(1e6); d > 50*mb {
		return fmt.Errorf("cluster start too far away: %v %v (%vmb)",
			r.off, r.cluster.Offset, (r.off-r.cluster.Offset)/mb)
	} else if d > 0 {
		bs := make([]byte, d)
		if _, err := r.MKV.ReadAt(bs, r.cluster.Offset); err != nil {
			return fmt.Errorf("failed to read from cluster start: %w", err)
		}
		r.tmp.Write(bs)
	}
	return nil
}
