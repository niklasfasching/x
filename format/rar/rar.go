package rar

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"path/filepath"

	"github.com/niklasfasching/x/format/bits"
	"golang.org/x/exp/slices"
)

// http://rescene.wikidot.com/rar-420-technote
// https://www.rarlab.com/technote.htm
// https://codedread.github.io/bitjs/docs/unrar.html#ext-time-structure

type RAR struct {
	*bits.R
	File
	Files []File
}

type File struct {
	Version, Offset, Size, TotalSize int64
	Name                             string
}

type Block struct {
	Type, HeadSize, Size, ExtraSize int64
	Flags                           uint16
}

var rarSig = append([]byte("Rar!"), 0x1A, 0x07)

func Parse(r io.ReaderAt, bufferSize int, exitFileExts ...string) (rar *RAR, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("rar: %w", e.(error))
		}
	}()

	rar = &RAR{R: bits.New(r, binary.LittleEndian, bufferSize)}
	if bs := rar.Bytes(7); bytes.Equal(bs, append(rarSig, 0x00)) {
		for {
			switch b := rar.parseBlock4(); b.Type {
			case 0x73: // marker
				rar.Skip(b.HeadSize)
			case 0x74: // file
				f := rar.parseFile4(b)
				if rar.Files = append(rar.Files, f); slices.Contains(exitFileExts, filepath.Ext(f.Name)) {
					rar.File = f
					return
				}
			default:
				rar.ThrowIf(len(exitFileExts) != 0 && rar.Offset == 0, "%v not found (%#v)", exitFileExts, rar.Files)
				rar.File = rar.LargestFile()
				return
			}
		}
	} else if bs = append(bs, rar.Bytes(1)...); bytes.Equal(bs, append(rarSig, 0x01, 0x00)) {
		for {
			switch b := rar.parseBlock5(); b.Type {
			case 1, 3: // main, service
				rar.Skip(b.Size + b.HeadSize + b.ExtraSize)
			case 2: // file
				f := rar.parseFile5(b)
				if rar.Files = append(rar.Files, f); slices.Contains(exitFileExts, filepath.Ext(f.Name)) {
					rar.File = f
					return
				}
			default:
				rar.ThrowIf(len(exitFileExts) != 0 && rar.Offset == 0, "%v not found (%#v)", exitFileExts, rar.Files)
				rar.File = rar.LargestFile()
				return
			}
		}
	} else {
		return nil, fmt.Errorf("rar: not a rar: %x", bs)
	}
}

func (r *RAR) LargestFile() File {
	slices.SortFunc(r.Files, func(a, b File) int { return int(a.TotalSize - b.TotalSize) })
	return r.Files[0]
}

func (r *RAR) ReadAt(bs []byte, off int64) (int, error) {
	return r.File.ReadAt(r.ReaderAt, bs, off)
}

func (d *File) ReadAt(r io.ReaderAt, bs []byte, off int64) (int, error) {
	off, l := int64(d.Offset)+off, min(int64(len(bs)), d.Size-off)
	c, err := r.ReadAt(bs[:l], off)
	if err == nil && int64(c) != l {
		return c, io.EOF
	}
	return c, err
}

func (r *RAR) parseBlock4() *Block {
	r.Uint16() // crc
	headerType, flags := int64(r.Bytes(1)[0]), r.Uint16()
	headSize, addSize, i := int64(r.Uint16()), int64(0), int64(7)
	if flags&0x8000 != 0 {
		addSize, i = int64(r.Uint32()), i+4
	}
	return &Block{headerType, headSize - i, addSize, 0, flags}
}

func (r *RAR) parseFile4(b *Block) File {
	r.ThrowIf(b.Flags&0x04 != 0, "encrypted files are not supported")
	totalUnpackedSize := int64(r.Uint32())
	r.Bytes(10) // os(1) + crc(4) + ftime(4) + version(1)
	compressionLvl := r.Bytes(1)[0] - 0x30
	r.ThrowIf(compressionLvl != 0, "compressed files are not supported: lvl %d", compressionLvl)
	nameSize := int(r.Uint16())
	r.Bytes(4) // attributes
	if b.Flags&0x100 != 0 {
		r.Bytes(4) // highPackedSize(4)
		totalUnpackedSize |= int64(r.Uint32()) << 32
	}
	name := string(r.Bytes(nameSize))
	if b.Flags&0x1000 != 0 {
		flags := r.Uint16()
		for _, n := range []int{0, 4, 8, 12} {
			// {arc,a,c,m}time; arc,a,c: uint32+x&3 extra bytes. m: x&3 extra bytes
			// should be extra bytes according to docs, but extra bits seems correct. whatever
			r.Bytes(int(flags>>n&1) * (min(4, 12-n) + int(flags>>n&3)))
		}
	}
	return File{4, r.Skip(b.Size) - b.Size, b.Size, totalUnpackedSize, name}
}

func (r *RAR) parseBlock5() *Block {
	r.Uint32() // crc
	headerSize := int64(r.Uvarint())
	headerOff := r.R.Off
	headerType, flags := int64(r.Uvarint()), r.Uvarint()
	extraSize, dataSize := int64(0), int64(0)
	if flags&0x0001 != 0 {
		extraSize = int64(r.Uvarint())
	}
	if flags&0x0002 != 0 {
		dataSize = int64(r.Uvarint())
	}
	return &Block{headerType, headerSize - (r.R.Off - headerOff) - extraSize, dataSize, extraSize, 0}
}

func (r *RAR) parseFile5(b *Block) File {
	fileFlags := r.Uvarint()
	totalUnpackedSize := int64(r.Uvarint())
	r.Uvarint()                // attributes
	if fileFlags&0x0002 != 0 { // mtime
		r.Uint32()
	}
	if fileFlags&0x0004 != 0 { // crc
		r.Uint32()
	}
	method := r.Uvarint()
	r.ThrowIf(method != 0, "unsupported compression method: %v", method)
	r.Uvarint() // os
	nameSize := int(r.Uvarint())
	name := string(r.Bytes(nameSize))
	return File{5, r.Skip(b.Size+b.ExtraSize) - b.Size, b.Size, totalUnpackedSize, name}
}
