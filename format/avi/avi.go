package avi

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/niklasfasching/x/format/bits"
)

// https://learn.microsoft.com/en-us/windows/win32/directshow/avi-riff-file-reference

type AVI struct {
	Duration float64
	Width    int
	Height   int
	Streams  []*Stream
}

type Stream struct {
	Type, Codec, Lang string
}

func Parse(r io.ReaderAt) (a *AVI, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("avi: %v", e)
		}
	}()
	a, b := &AVI{}, bits.New(r, binary.LittleEndian, 1e6)
	kind := string(b.Bytes(4))
	b.ThrowIf(kind != "RIFF", `expected "RIFF": %q`, kind)
	b.Bytes(4) // size
	id := string(b.Bytes(4))
	b.ThrowIf(id != "AVI ", `expected "AVI ": %q`, id)
	for s := (&Stream{}); ; {
		id, size, start := string(b.Bytes(4)), b.Uint32(), b.Off
		switch id {
		case "LIST":
			if string(b.Bytes(4)) == "strl" {
				s = &Stream{}
				a.Streams = append(a.Streams, s)
			}
			continue
		case "avih":
			microSecPerFrame := int(b.Uint32())
			b.Bytes(12)
			totalFrames := int(b.Uint32())
			b.Bytes(12)
			a.Duration = (time.Duration(totalFrames*microSecPerFrame) * time.Microsecond).Seconds()
			a.Width = int(b.Uint32())
			a.Height = int(b.Uint32())
		case "strh":
			s.Type = string(b.Bytes(4))
			s.Codec = string(b.Bytes(4))
			langID := b.Uint16()
			switch primaryLang := langID & 0x3FF; primaryLang {
			case 0x0000:
				s.Lang = "und"
			case 0x0007:
				s.Lang = "de"
			case 0x0009:
				s.Lang = "en"
			default:
				s.Lang = fmt.Sprintf("%03d", primaryLang)
			}
		case "strf":
			if s.Type == "auds" {
				wFormatTag := b.Uint16()
				if wFormatTag == 0x2000 {
					s.Codec = "AC3"
				} else if wFormatTag == 0x0055 {
					s.Codec = "MP3"
				}
			}
		case "JUNK", "INAM", "IPRD", "ISFT":
		default:
			return a, nil
		}
		b.Off = start + int64(size)
		if size%2 != 0 {
			b.Off++
		}
	}
}

func (a *AVI) Tracks() map[string][]string {
	if a == nil {
		return nil
	}
	ts := map[string][]string{}
	for _, t := range a.Streams {
		switch t.Type {
		case "vids":
			ts["video"] = append(ts["video"], t.Codec)
		case "auds":
			ts["audio"] = append(ts["audio"], fmt.Sprintf("%s::%s", t.Codec, t.Lang))
		}
	}
	return ts
}
