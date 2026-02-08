package util

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

func NewPublicDialer(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !ip.IsGlobalUnicast() || ip.IsPrivate() {
				return nil, fmt.Errorf("restricted ip: %v", ip)
			}
		}
		if dialer == nil {
			dialer = &net.Dialer{}
		}
		// TODO: prefer ipv4. ppl seem to fuck up ipv6 more often and don't fix it bc no one uses it
		slices.SortFunc(ips, func(a, b netip.Addr) int { return a.Compare(b) })
		lastErr := error(nil)
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func HandleByteRange(r *http.Request, w http.ResponseWriter, size int64) (off, l int64, ok bool) {
	if off, l, ok = ParseByteRange(r.Header.Get("Range"), size); !ok {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(l, 10))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, off+l-1, size))
		w.WriteHeader(http.StatusPartialContent)
	}
	return off, l, ok
}

func ParseByteRange(rng string, size int64) (off, l int64, ok bool) {
	kv := strings.Split(rng, "=")
	if size <= 0 || len(kv) != 2 || strings.ToLower(kv[0]) != "bytes" {
		return 0, size, false
	} else if vs := strings.Split(kv[1], "-"); len(vs) != 2 {
		return 0, size, false
	} else if a, aErr := strconv.ParseInt(vs[0], 10, 64); aErr == nil && vs[1] == "" {
		return a, size - a, true // a-
	} else if b, bErr := strconv.ParseInt(vs[1], 10, 64); bErr == nil && aErr == nil {
		return a, b - a + 1, true // a-b
	} else if bErr == nil {
		return size - b, b, true // -b
	}
	return 0, size, false
}
