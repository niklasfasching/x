package util

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleByteRange(t *testing.T) {
	testRange(t, "bytes=100-", 200, 100, 100)
	testRange(t, "bytes=-100", 200, 100, 100)
	testRange(t, "bytes=10-19", 200, 10, 10)
	testRange(t, "bytes=", 200, 0, 0)
	testRange(t, "", 200, 0, 0)
	testRange(t, "300-", 200, 0, 0)
	testRange(t, "-100-", 200, 0, 0)
	testRange(t, "-300", 200, 0, 0)
}

func testRange(t *testing.T, reqRange string, size, expectedOff, expectedLen int64) {
	r1, w1 := httptest.NewRequest("GET", "/", nil), &httptest.ResponseRecorder{}
	r1.Header.Set("Range", reqRange)
	http.ServeContent(w1, r1, "", time.Time{}, bytes.NewReader(make([]byte, size)))
	r2, w2 := httptest.NewRequest("GET", "/", nil), &httptest.ResponseRecorder{}
	r2.Header.Set("Range", reqRange)
	parsedOff, parsedLen := HandleByteRange(r2, w2, size)
	res1, res2 := w1.Result(), w2.Result()
	h1, h2 := res1.Header, res2.Header
	if expectedOff == 0 && expectedLen == 0 && parsedOff == 0 && parsedLen == 0 && res2.StatusCode == 200 {
		return
	} else if res1.StatusCode != res2.StatusCode {
		t.Fatalf("expected=%d actual=%d", res1.StatusCode, res2.StatusCode)
	} else if h1.Get("Content-Range") != h2.Get("Content-Range") {
		t.Fatalf("expected=%q actual=%q", h1.Get("Content-Range"), h2.Get("Content-Range"))
	} else if h1.Get("Accept-Ranges") != h2.Get("Accept-Ranges") {
		t.Fatalf("expected=%q actual=%q", h1.Get("Accept-Ranges"), h2.Get("Accept-Ranges"))
	} else if parsedOff != expectedOff || parsedLen != expectedLen {
		t.Fatalf("expectedOff=%d expectedLen=%d parsedOff=%d parsedLen=%d",
			expectedOff, expectedLen, parsedOff, parsedLen)
	}
}
