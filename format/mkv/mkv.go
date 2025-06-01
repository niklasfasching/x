package mkv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"

	"github.com/niklasfasching/x/format/bits"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

// https://datatracker.ietf.org/doc/html/rfc8794
// https://www.matroska.org/technical/diagram.html
// https://www.matroska.org/technical/elements.html

type MKV struct {
	Header struct {
		DocType string `ebml:"4282"`
	} `ebml:"1A45DFA3"`
	Segment struct {
		DataOffset int64
		SeekHead   struct {
			Seek []struct {
				DataOffset int64
				ID         string `ebml:"53AB"`
				Position   uint   `ebml:"53AC"`
			} `ebml:"4DBB"`
		} `ebml:"114D9B74"`
		Tracks struct {
			Entries []struct {
				ID        uint   `ebml:"D7"`
				Name      string `ebml:"536E"`
				Lang      string `ebml:"22B59C"`
				LangBCP47 string `ebml:"22B59D"`
				CodecID   string `ebml:"86"`
				Type      uint   `ebml:"83"`
			} `ebml:"AE"`
		} `ebml:"1654AE6B"`
		Info struct {
			TimeScale uint    `ebml:"2AD7B1"`
			Duration  float64 `ebml:"4489"`
		} `ebml:"1549A966"`
		Clusters []Cluster `ebml:"1F43B675"`
		Cues     struct {
			Points []struct {
				CueTime   uint `ebml:"B3"`
				Positions []struct {
					TrackID          uint `ebml:"F7"`
					ClusterPosition  uint `ebml:"F1"`
					BlockNumber      uint `ebml:"5378"`
					RelativePosition uint `ebml:"F0"`
				} `ebml:"B7"`
			} `ebml:"BB"`
		} `ebml:"1C53BB6B"`
	} `ebml:"18538067"`
}

type Cluster struct {
	Offset, DataOffset, Size int64
	Time                     uint `ebml:"E7"`
	BlockGroups              []struct {
		Duration uint   `ebml:"9B"`
		Block    string `ebml:"A1"`
	} `ebml:"A0"`
	SimpleBlocks []string `ebml:"A3"`
}

type EBML struct {
	*bits.R
	includePaths []string
}

var ebmlSig = []byte{0x1A, 0x45, 0xDF, 0xA3}

func Parse(r io.ReaderAt, bufferSize int, includeFields ...string) (m *MKV, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("mkv: %s", e)
		}
	}()
	m, e, bs := &MKV{}, &EBML{R: bits.New(r, binary.BigEndian, bufferSize)}, []byte{}
	bs, e.Off = e.Bytes(4), 0
	e.ThrowIf(!bytes.Equal(bs, ebmlSig), "invalid ebml: %X != %X (%v)", bs, ebmlSig, err)
	if len(includeFields) == 0 {
		v := reflect.ValueOf(m).Elem()
		e.Unmarshal("/", v, -1, e.IDs("/", v, -1, -1))
	} else {
		m.seekUnmarshal(e, includeFields)
	}
	return m, nil
}

func (m *MKV) Tracks() map[string][]string {
	if m == nil {
		return nil
	}
	ts := map[string][]string{}
	for _, t := range m.Segment.Tracks.Entries {
		switch t.Type {
		case 1:
			ts["video"] = append(ts["video"], t.CodecID)
		case 2:
			ts["audio"] = append(ts["audio"], fmt.Sprintf("%s::%s%s", t.CodecID, t.Lang, t.LangBCP47))
		case 17:
			ts["text"] = append(ts["text"], fmt.Sprintf("%s::%s%s", t.CodecID, t.Lang, t.LangBCP47))
		}
	}
	return ts
}

func (m *MKV) Duration() time.Duration {
	si := m.Segment.Info
	return time.Duration(si.Duration * float64(si.TimeScale))
}

func (m *MKV) SeekHead() map[string]int64 {
	ids := map[string]int64{}
	for _, s := range m.Segment.SeekHead.Seek {
		ids[fmt.Sprintf("%X", s.ID)] = m.Segment.DataOffset + int64(s.Position)
	}
	return ids
}

func (e *EBML) Unmarshal(path string, v reflect.Value, end int64, ids map[string]int) {
	for (end == -1 || e.Off < end) && len(ids) != 0 {
		id, size, l := e.Next()
		if i, ok := ids[id]; !ok {
			e.Off += size
		} else if path := path + id + "/"; ok {
			switch f := v.Field(i); f.Kind() {
			case reflect.Slice:
				f.Set(reflect.Append(f, reflect.Zero(f.Type().Elem())))
				if f = f.Index(f.Len() - 1); f.Kind() == reflect.Struct {
					e.Unmarshal(path, f, e.Off+size, e.IDs(path, f, l, e.Off+size))
				} else {
					e.set(f, size)
				}
			case reflect.Struct:
				e.Unmarshal(path, f, e.Off+size, e.IDs(path, f, l, e.Off+size))
				delete(ids, id)
			default:
				e.set(f, size)
				delete(ids, id)
			}
		}
	}
	e.Off = end
}

func (m *MKV) unmarshalSeekHead(e *EBML, path string, v reflect.Value, fields map[string]int, offsets map[string]int64, off int64) {
	e.Off, e.includePaths = off, []string{"/18538067/114D9B74/"}
	e.Unmarshal(path, v, -1, e.IDs(path, v, -1, -1))
	for _, s := range m.Segment.SeekHead.Seek {
		offsets[fmt.Sprintf("%X", s.ID)] = m.Segment.DataOffset + int64(s.Position)
	}
	if len(slices.DeleteFunc(maps.Keys(fields), func(id string) bool { return offsets[id] != 0 })) == 0 {
		return
	}
	for _, s := range m.Segment.SeekHead.Seek {
		if fmt.Sprintf("%X", s.ID) == "114D9B74" && path == "/" {
			v := reflect.ValueOf(&m.Segment).Elem()
			m.unmarshalSeekHead(e, "/18538067/", v, fields, offsets, m.Segment.DataOffset+int64(s.Position))
		}
	}
}

func (m *MKV) seekUnmarshal(e *EBML, includeFields []string) {
	v, fields := reflect.ValueOf(&m.Segment).Elem(), map[string]int{}
	for i, n := 0, v.Type().NumField(); i < n; i++ {
		f := v.Type().Field(i)
		if id := f.Tag.Get("ebml"); slices.Contains(includeFields, f.Name) {
			fields[id] = i
		}
	}
	offsets := map[string]int64{}
	m.unmarshalSeekHead(e, "/", reflect.ValueOf(m).Elem(), fields, offsets, e.Off)
	for id, i := range fields {
		f, path := v.Type().Field(i), "/18538067/"
		off, ok := offsets[id]
		e.ThrowIf(!ok, "unseekable field: %s: %s (%v)", f.Name, id, offsets)
		e.Off, e.includePaths = off, []string{}
		actualID, size, l := e.Next()
		e.ThrowIf(actualID != id, "bad seek: %s != %s", actualID, id)
		e.Unmarshal(path, v.Field(i), e.Off+size, e.IDs(path, v.Field(i), e.Off+size, l))
	}
}

func (e *EBML) set(v reflect.Value, size int64) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(string(e.Bytes(int(size))))
	case reflect.Uint:
		v.SetUint(e.Uint64(size))
	case reflect.Int64:
		x, _ := e.Varint()
		v.SetInt(int64(x))
	case reflect.Float64:
		v.SetFloat(e.Float64(size))
	default:
		panic(v.Kind().String())
	}
}

func (e *EBML) IDs(path string, v reflect.Value, tagLen, end int64) map[string]int {
	ids, t := map[string]int{}, v.Type()
	for i, n := 0, v.Type().NumField(); i < n; i++ {
		f := t.Field(i)
		if id := f.Tag.Get("ebml"); id != "" && e.isIncluded(path+id+"/") {
			ids[id] = i
		} else if f.Name == "DataOffset" {
			v.Field(i).SetInt(e.Off)
		} else if f.Name == "Offset" && tagLen != -1 {
			v.Field(i).SetInt(e.Off - tagLen)
		} else if f.Name == "Size" && end != -1 {
			v.Field(i).SetInt(end - e.Off)
		}
	}
	return ids
}

func (e *EBML) isIncluded(path string) bool {
	return len(e.includePaths) == 0 || slices.ContainsFunc(e.includePaths,
		func(p string) bool { return strings.HasPrefix(path, p) || strings.HasPrefix(p, path) })
}

func (e *EBML) Size() (uint64, int64) {
	size, l2 := e.Varint()
	size &^= 1 << (l2 * 7) // clear MSB
	return size, l2
}

func (e *EBML) Uint64(size int64) uint64 {
	bs := make([]byte, 8)
	copy(bs[8-size:], e.Bytes(int(size)))
	return binary.BigEndian.Uint64(bs)
}

func (e *EBML) Float64(size int64) float64 {
	if size == 4 {
		return float64(math.Float32frombits(uint32(e.Uint64(size))))
	}
	return math.Float64frombits(e.Uint64(size))
}

func (e *EBML) Next() (string, int64, int64) {
	id, l1 := e.Varint()
	size, l2 := e.Size()
	return fmt.Sprintf("%X", id), int64(size), l1 + l2
}
