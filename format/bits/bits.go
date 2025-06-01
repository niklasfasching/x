package bits

import (
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
	"runtime"
	"strings"
)

type R struct {
	io.ReaderAt
	binary.ByteOrder
	Off int64
}

func New(r io.ReaderAt, order binary.ByteOrder, size int) *R {
	return &R{NewBufferedReaderAt(r, size), order, 0}
}

func (r *R) Uint16() uint16 {
	return r.ByteOrder.Uint16(r.Bytes(2))
}

func (r *R) Uint32() uint32 {
	return r.ByteOrder.Uint32(r.Bytes(4))
}

func (r *R) Uint64() uint32 {
	return r.ByteOrder.Uint32(r.Bytes(8))
}

func (r *R) Bytes(n int) []byte {
	bs := make([]byte, n)
	n, err := r.ReadAt(bs, int64(r.Off))
	r.Off += int64(n)
	r.ThrowIf(n != len(bs), "bytes: read: %d < %d (%v)", n, len(bs), err)
	return bs
}

func (r *R) Varint() (uint64, int64) {
	b := r.Bytes(1)
	v, l := uint64(b[0]), bits.LeadingZeros8(uint8(b[0]))
	for i := 0; i < l; i++ {
		v = (v << 8) | uint64(r.Bytes(1)[0])
	}
	return v, int64(l + 1)
}

func (r *R) Uvarint() uint64 {
	v, err := binary.ReadUvarint(r)
	r.ThrowIf(err != nil, "bad uvarint: %s", err)
	return v
}

func (r *R) ReadByte() (byte, error) {
	return r.Bytes(1)[0], nil
}

func (r *R) ThrowIf(cond bool, tpl string, xs ...any) {
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

func (r *R) Skip(n int64) int64 {
	r.Off += n
	return r.Off
}
