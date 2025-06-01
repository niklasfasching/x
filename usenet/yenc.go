package usenet

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"
)

// http://www.yenc.org/yenc-draft.1.3.txt

func Decode(bs []byte) (int, []byte, error) {
	s, pcrc, offset, n := bufio.NewScanner(bytes.NewReader(bs)), crc32.NewIEEE(), 0, 0
	for s.Scan() {
		if l := s.Bytes(); bytes.HasPrefix(l, []byte("=y")) {
			xs, m, k, v := strings.Split(string(l), " "), map[string]string{}, "", ""
			for _, x := range xs[1:] {
				kv := strings.Split(x, "=")
				if len(kv) == 2 {
					k, v = strings.ToLower(kv[0]), kv[1]
					m[k] = v
				} else if k != "" {
					m[k] += " " + x
				} else {
					return -1, nil, fmt.Errorf("%s: bad kv: %s (%v)", xs[0], x, xs)
				}
			}
			switch cmd := xs[0]; cmd {
			case "=ybegin": // line, size, name, part
			case "=ypart": // begin, end [part, total]
				if offset != 0 {
					return -1, nil, fmt.Errorf("multipart yenc is not supported")
				}
				offset, _ = strconv.Atoi(m["begin"])
				offset--
			case "=yend": // size, crc32, pcrc32
				if h := strings.ToLower(strings.TrimLeft(m["pcrc32"], "0")); h != "" {
					if hc := fmt.Sprintf("%x", pcrc.Sum32()); h != hc {
						return -1, nil, fmt.Errorf("h=%s != pcrc32=%x (%v)", h, hc, m)
					}
				}
				pcrc.Reset()
			default:
				return -1, nil, fmt.Errorf("unexpected %s line", cmd)
			}
		} else {
			lbs := decodeYencLine(l)
			pcrc.Write(lbs)
			n += copy(bs[n:], lbs)
		}
	}
	if err := s.Err(); err != nil {
		return -1, nil, fmt.Errorf("scan: %w", err)
	}
	return offset, bs[:n], nil
}

func Encode(bs []byte, off int64, name string) []byte {
	w, h := &bytes.Buffer{}, crc32.New(crc32.IEEETable)
	h.Write(bs)
	fmt.Fprintf(w, "=ybegin size=%d name=%s\n", len(bs), name)
	fmt.Fprintf(w, "=ypart begin=%d end=%d\n", off+1, off+int64(len(bs)))
	for _, b := range bs {
		b = (b + 42) % 255
		switch b {
		case '\x00', '\r', '\n', '=':
			w.WriteByte('=')
			b = (b + 64) % 255
		}
		w.WriteByte(b)
	}
	fmt.Fprintf(w, "\n=yend size=10 pcrc32=%x", h.Sum32())
	return w.Bytes()
}

func decodeYencLine(line []byte) []byte {
	i, j, escaped := 0, 0, false
	for ; i < len(line); i, j = i+1, j+1 {
		if escaped {
			line[j], escaped = (((line[i]-42)&255)-64)&255, false
		} else if line[i] == '=' {
			escaped, j = true, j-1
		} else {
			line[j] = (line[i] - 42) & 255
		}
	}
	return line[:j]
}
