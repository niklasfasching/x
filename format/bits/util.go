package bits

import (
	"io"
)

type BufferedReaderAt struct {
	off, size int64
	bs        []byte
	io.ReaderAt
}

func NewBufferedReaderAt(r io.ReaderAt, size int) io.ReaderAt {
	if size <= 0 {
		return r
	}
	return &BufferedReaderAt{ReaderAt: r, bs: make([]byte, 0), size: int64(size)}
}

func (r *BufferedReaderAt) ReadAt(bs []byte, off int64) (n int, err error) {
	if off < r.off || off > r.off+int64(len(r.bs)) || len(r.bs) == 0 {
		r.bs, r.off = make([]byte, r.size), off
		if c, err := r.ReaderAt.ReadAt(r.bs, r.off); c > 0 && err == io.EOF {
			r.bs = r.bs[:c]
		} else if err != nil {
			return 0, err
		}
	}
	n = (copy(bs, r.bs[off-r.off:]))
	r.bs, r.off = r.bs[int(off-r.off)+n:], off+int64(n)
	if n < len(bs) {
		n2, err := r.ReadAt(bs[n:], off+int64(n))
		return n + n2, err
	}
	return n, nil
}
