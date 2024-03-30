package rar

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"
)

// http://rescene.wikidot.com/rar-420-technote
// https://www.rarlab.com/technote.htm
// https://codedread.github.io/bitjs/docs/unrar.html#ext-time-structure

type RAR struct {
	io.ReaderAt
	Version, Offset, Size, TotalSize int64
	Name                             string
	off                              int64
}

type RARBlock struct {
	Type, HeadSize, Size, ExtraSize int64
	Flags                           uint16
}

var rarSig = append([]byte("Rar!"), 0x1A, 0x07)

func Parse(r io.ReaderAt, exitFileExt string) (rar *RAR, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("rar: %w", e.(error))
		}
	}()
	rar = &RAR{ReaderAt: r}
	if bs := rar.bytes(7); bytes.Equal(bs, append(rarSig, 0x00)) {
		rar.Version = 4
		for {
			switch b := rar.parseBlock4(); b.Type {
			case 0x73: // marker
				rar.skip(b.HeadSize)
			case 0x74: // file
				off, size, totalSize, name := rar.parseFile4(b)
				if exitFileExt == filepath.Ext(name) {
					rar.Offset, rar.Size, rar.TotalSize, rar.Name = off, size, totalSize, name
					return
				}
			default:
				throwIf(exitFileExt != "" && rar.Offset == 0, "'*%s' not found", exitFileExt)
				return
			}
		}
	} else if bs = append(bs, rar.bytes(1)...); bytes.Equal(bs, append(rarSig, 0x01, 0x00)) {
		rar.Version = 5
		for {
			switch b := rar.parseBlock5(); b.Type {
			case 1, 3: // main, service
				rar.skip(b.Size + b.HeadSize + b.ExtraSize)
			case 2: // file
				off, size, totalSize, name := rar.parseFile5(b)
				if exitFileExt == filepath.Ext(name) {
					rar.Offset, rar.Size, rar.TotalSize, rar.Name = off, size, totalSize, name
					return
				}
			default:
				throwIf(exitFileExt != "" && rar.Offset == 0, "'*%s' not found", exitFileExt)
				return
			}
		}
	} else {
		return nil, fmt.Errorf("rar: not a rar: %x", bs)
	}
}

func (r *RAR) ReadAt(bs []byte, off int64) (int, error) {
	l := min(len(bs), int(r.Size-off))
	c, err := r.ReaderAt.ReadAt(bs[:l], r.Offset+off)
	if err == nil && c != l {
		return c, io.EOF
	}
	return c, err
}

func (r *RAR) parseBlock4() *RARBlock {
	r.uint16() // crc
	headerType, flags := int64(r.bytes(1)[0]), r.uint16()
	headSize, addSize, i := int64(r.uint16()), int64(0), int64(7)
	if flags&0x8000 != 0 {
		addSize, i = int64(r.uint32()), i+4
	}
	return &RARBlock{headerType, headSize - i, addSize, 0, flags}
}

func (r *RAR) parseFile4(b *RARBlock) (int64, int64, int64, string) {
	throwIf(b.Flags&0x04 != 0, "encrypted files are not supported")
	totalUnpackedSize := int64(r.uint32())
	r.bytes(10) // os(1) + crc(4) + ftime(4) + version(1)
	compressionLvl := r.bytes(1)[0] - 0x30
	throwIf(compressionLvl != 0, "compressed files are not supported: lvl %d", compressionLvl)
	nameSize := int(r.uint16())
	r.bytes(4) // attributes
	if b.Flags&0x100 != 0 {
		r.bytes(4) // highPackedSize(4)
		totalUnpackedSize |= int64(r.uint32()) << 32
	}
	name := string(r.bytes(nameSize))
	if b.Flags&0x1000 != 0 {
		flags := r.uint16()
		for _, n := range []int{0, 4, 8, 12} {
			// {arc,a,c,m}time; arc,a,c: uint32+x&3 extra bytes. m: x&3 extra bytes
			// should be extra bytes according to docs, but extra bits seems correct. whatever
			r.bytes(int(flags>>n&1) * (min(4, 12-n) + int(flags>>n&3)))
		}
	}
	return r.skip(b.Size) - b.Size, b.Size, totalUnpackedSize, name
}

func (r *RAR) parseBlock5() *RARBlock {
	r.uint32() // crc
	headerSize := int64(r.uvarint())
	headerOff := r.off
	headerType, flags := int64(r.uvarint()), r.uvarint()
	extraSize, dataSize := int64(0), int64(0)
	if flags&0x0001 != 0 {
		extraSize = int64(r.uvarint())
	}
	if flags&0x0002 != 0 {
		dataSize = int64(r.uvarint())
	}
	return &RARBlock{headerType, headerSize - (r.off - headerOff) - extraSize, dataSize, extraSize, 0}
}

func (r *RAR) parseFile5(b *RARBlock) (int64, int64, int64, string) {
	fileFlags := r.uvarint()
	totalUnpackedSize := int64(r.uvarint())
	r.uvarint()                // attributes
	if fileFlags&0x0002 != 0 { // mtime
		r.uint32()
	}
	if fileFlags&0x0004 != 0 { // crc
		r.uint32()
	}
	method := r.uvarint()
	throwIf(method != 0, "unsupported compression method: %v", method)
	r.uvarint() // os
	nameSize := int(r.uvarint())
	name := string(r.bytes(nameSize))
	return r.skip(b.Size+b.ExtraSize) - b.Size, b.Size, totalUnpackedSize, name
}

func (r *RAR) ReadByte() (byte, error) {
	bs := make([]byte, 1)
	c, err := r.ReaderAt.ReadAt(bs, int64(r.off))
	r.off += int64(c)
	return bs[0], err
}

func (r *RAR) bytes(n int) []byte {
	bs := make([]byte, n)
	n, err := r.ReaderAt.ReadAt(bs, int64(r.off))
	r.off += int64(n)
	throwIf(err != nil || n != len(bs), "bytes: incomplete read: %d < %d: %w", n, len(bs), err)
	return bs
}

func (r *RAR) uvarint() uint64 {
	v, err := binary.ReadUvarint(r)
	throwIf(err != nil, "bad uvarint: %s", err)
	return v
}

func (r *RAR) uint32() uint32 {
	return binary.LittleEndian.Uint32(r.bytes(4))
}

func (r *RAR) uint16() uint16 {
	return binary.LittleEndian.Uint16(r.bytes(2))
}

func (r *RAR) skip(n int64) int64 {
	r.off += n
	return r.off
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
