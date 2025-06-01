package mp4

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"io"
	"strings"

	"github.com/niklasfasching/x/format/bits"
)

// https://xhelmboyx.tripod.com/formats/mp4-layout.txt

type MP4 struct {
	*bits.R
	MajorBrand       string
	MinorVersion     uint32
	Duration         float64 // seconds
	CompatibleBrands []string
	RawTracks        []*Track
	Off              int64
}

type Track struct {
	Type, Handler, Lang string
	Duration            float64 // seconds
	Codec               string
	CodecTags           []string
}

func Parse(r io.ReaderAt, bufferSize int) (m *MP4, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("mkv: %s", e)
		}
	}()
	m = &MP4{R: bits.New(r, binary.BigEndian, bufferSize)}
	for moovEnd := int64(0); moovEnd == 0 || m.Off < moovEnd; {
		size, k := m.parseBoxHeader()
		switch k {
		case "mdia", "minf", "stbl": // enter
		case "moov": // enter and exit after
			moovEnd = m.Off + size
		case "trak":
			m.RawTracks = append(m.RawTracks, &Track{})
		case "ftyp":
			m.MajorBrand = string(m.Bytes(4))
			m.MinorVersion = m.Uint32()
			for range (size - 8) / 4 {
				m.CompatibleBrands = append(m.CompatibleBrands, string(m.Bytes(4)))
			}
		case "hdlr":
			t := m.RawTracks[len(m.RawTracks)-1]
			m.Off += 8 //  version(1), flags(3), pre_defined(4)
			t.Type = string(m.Bytes(4))
			m.Off += 12 // reserved(12)
			t.Handler = strings.Split(string(m.Bytes(int(size-24))), "\000")[0]
		case "stsd":
			t := m.RawTracks[len(m.RawTracks)-1]
			m.Off += 4 // version(1), flags(3)
			for n, i, end := int(m.Uint32()), 0, m.Off+size; i < n && m.Off < end; i++ {
				size, id := m.parseBoxHeader()
				t.CodecTags = append(t.CodecTags, id)
				if t.Type == "vide" {
					m.Off += 78
					size, id := m.parseBoxHeader()
					if c := map[string]string{"avcC": "h.264", "hvcC": "h.265"}[id]; c != "" {
						t.Codec = c
					}
					m.Off += size
				} else {
					m.Off += size
				}
			}
		case "mvhd":
			m.Duration = m.parseDuration()
			m.Off += 80 // rate(4), volume(2), reserved(10), matrix(36), pre_defined(24), next_track_id(4)
		case "mdhd":
			t := m.RawTracks[len(m.RawTracks)-1]
			t.Duration = m.parseDuration()
			if lang := m.Uint16(); lang == 0 {
				t.Lang = "und" // ISO 639-2
			} else {
				chars := make([]byte, 3)
				chars[0] = byte(((lang >> 10) & 0x1F) + 0x60)
				chars[1] = byte(((lang >> 5) & 0x1F) + 0x60)
				chars[2] = byte((lang & 0x1F) + 0x60)
				t.Lang = string(chars)
			}
			m.Off += 2 // quality(2)
		default:
			m.Off += size
		}
	}
	return m, nil
}

func (m *MP4) Tracks() map[string][]string {
	if m == nil {
		return nil
	}
	ts := map[string][]string{}
	for _, t := range m.RawTracks {
		switch t.Type {
		case "vide":
			ts["video"] = append(ts["video"], cmp.Or(t.Codec, strings.Join(t.CodecTags, "|")))
		case "soun":
			for _, c := range t.CodecTags {
				ts["audio"] = append(ts["audio"], fmt.Sprintf("%s::%s", c, t.Lang))
			}
		}
	}
	return ts
}

func (m *MP4) parseDuration() float64 {
	version := m.Bytes(1)[0]
	m.Off += 3 // flags(3)
	if version == 1 {
		m.Off += 16 // cdate(8), mdate(8)
		ts, d := m.Uint32(), m.Uint64()
		return float64(d) / float64(ts)
	} else {
		m.Off += 8 // cdate(4), mdate(4)
		ts, d := m.Uint32(), m.Uint32()
		return float64(d) / float64(ts)
	}
}

func (m *MP4) parseBoxHeader() (int64, string) {
	switch size, name := m.Uint32(), string(m.Bytes(4)); size {
	case 1: // 64-bit extended size
		return int64(m.Uint64()) - 16, name
	default: // Standard 32-bit size
		return int64(size) - 8, name
	}
}
