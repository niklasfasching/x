package mkv

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"math/bits"
	"reflect"
	"runtime"
	"strings"
	"time"

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
				ID       string `ebml:"53AB"`
				Position uint   `ebml:"53AC"`
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
	*EBML
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
	SizedReaderAt
	includePaths []string
	Off          int64
}

type SizedReaderAt interface {
	io.ReaderAt
	Size() int64
}

var ebmlSig = []byte{0x1A, 0x45, 0xDF, 0xA3}

func Parse(r SizedReaderAt, includeFields ...string) (m *MKV, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("mkv: %s", e)
		}
	}()
	bs := make([]byte, 4)
	_, err = r.ReadAt(bs, 0)
	throwIf(!bytes.Equal(bs, ebmlSig), "invalid ebml: %X != %X (%v)", bs, ebmlSig, err)
	m = &MKV{EBML: &EBML{r, nil, 0}}
	if len(includeFields) == 0 {
		v := reflect.ValueOf(m).Elem()
		m.Unmarshal("/", v, r.Size(), m.IDs("/", v, 0, r.Size()))
	} else {
		m.seekUnmarshal(includeFields)
	}
	return m, nil
}

func (m *MKV) Duration() time.Duration {
	si := m.Segment.Info
	return time.Duration(si.Duration * float64(si.TimeScale))
}

func (e *EBML) Unmarshal(path string, v reflect.Value, end int64, ids map[string]int) {
	for e.Off < end && len(ids) != 0 {
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

func (m *MKV) unmarshalSeekHead(path string, v reflect.Value, off int64) {
	m.Off, m.includePaths = off, []string{"/18538067/114D9B74/"}
	m.Unmarshal(path, v, m.SizedReaderAt.Size(), m.IDs(path, v, 0, -1))
	for _, s := range m.Segment.SeekHead.Seek {
		if fmt.Sprintf("%X", s.ID) == "114D9B74" && path == "/" {
			v := reflect.ValueOf(&m.Segment).Elem()
			m.unmarshalSeekHead("/18538067/", v, m.Segment.DataOffset+int64(s.Position))
		}
	}
}

func (m *MKV) seekUnmarshal(includeFields []string) {
	m.unmarshalSeekHead("/", reflect.ValueOf(m).Elem(), m.Off)
	v, ids := reflect.ValueOf(&m.Segment).Elem(), map[string]int64{}
	for _, s := range m.Segment.SeekHead.Seek {
		ids[fmt.Sprintf("%X", s.ID)] = m.Segment.DataOffset + int64(s.Position)
	}
	for i, n, path := 0, v.Type().NumField(), "/18538067/"; i < n; i++ {
		f := v.Type().Field(i)
		if id := f.Tag.Get("ebml"); slices.Contains(includeFields, f.Name) {
			off, ok := ids[id]
			throwIf(!ok, "unseekable field: %s: %s (%v)", f.Name, id, ids)
			m.Off, m.includePaths, path = off, []string{}, path+id+"/"
			_id, size, l := m.Next()
			throwIf(_id != id, "bad seek: %s != %s", _id, id)
			m.Unmarshal(path, v.Field(i), m.Off+size, m.IDs(path, v.Field(i), m.Off+size, l))
			delete(ids, id)
		}
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
		v.SetFloat(e.float64(size))
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
		} else if f.Name == "Offset" && end != -1 {
			v.Field(i).SetInt(e.Off - tagLen)
		} else if f.Name == "DataOffset" && end != -1 {
			v.Field(i).SetInt(e.Off)
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

func (e *EBML) float64(size int64) float64 {
	if size == 4 {
		return float64(math.Float32frombits(uint32(e.Uint64(size))))
	}
	return math.Float64frombits(e.Uint64(size))
}

func (e *EBML) Uint64(size int64) uint64 {
	bs := make([]byte, 8)
	copy(bs[8-size:], e.Bytes(int(size)))
	return binary.BigEndian.Uint64(bs)
}

func (e *EBML) Bytes(n int) []byte {
	bs := make([]byte, n)
	n, err := e.ReadAt(bs, int64(e.Off))
	e.Off += int64(n)
	throwIf(n != len(bs), "bytes: read: %d < %d (%v)", n, len(bs), err)
	return bs
}

func (e *EBML) Varint() (uint64, int64) {
	b := e.Bytes(1)
	v, l := uint64(b[0]), bits.LeadingZeros8(uint8(b[0]))
	for i := 0; i < l; i++ {
		v = (v << 8) | uint64(e.Bytes(1)[0])
	}
	return v, int64(l + 1)
}

func (e *EBML) Size() (uint64, int64) {
	size, l2 := e.Varint()
	size &^= 1 << (l2 * 7) // clear MSB
	return size, l2
}

func (e *EBML) Next() (string, int64, int64) {
	id, l1 := e.Varint()
	size, l2 := e.Size()
	return fmt.Sprintf("%X", id), int64(size), l1 + l2
}

func throwIf(cond bool, tpl string, xs ...any) {
	if cond {
		stack, b := make([]uintptr, 50), &strings.Builder{}
		l := runtime.Callers(2, stack[:])
		for _, ptr := range stack[:l-2] {
			x := runtime.FuncForPC(ptr)
			file, line := x.FileLine(ptr)
			fmt.Fprintf(b, "\n\t%s:%d %s", file, line, x.Name())
		}
		panic(fmt.Errorf(tpl+" %s", append(xs, b.String())...))
	}
}
