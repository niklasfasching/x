package push

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServer(t *testing.T) {
	email := "admin@example.com"
	if _, err := New(email, GeneratePrivateKey()); err != nil {
		t.Fatal(err)
	} else if _, err := New(email, ""); err == nil {
		t.Fatal("expected error on invalid pem")
	}

	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientPub := base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes())
	authSecret := base64.RawURLEncoding.EncodeToString([]byte("1234567890123456"))
	req, body := &http.Request{}, []byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req = r
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(201)
	}))
	defer srv.Close()
	s, _ := New("admin@test", GeneratePrivateKey())
	sub := Sub{Endpoint: srv.URL + "/push/123", UserID: "u1"}
	sub.Keys.P256dh, sub.Keys.Auth = clientPub, authSecret
	if err := s.Send(sub, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatal(err)
	}

	t.Run("headers", func(t *testing.T) {
		headers := map[string]string{
			"Content-Encoding": "aes128gcm",
			"TTL":              "86400",
			"Content-Type":     "application/octet-stream",
		}
		for k, v := range headers {
			if got := req.Header.Get(k); got != v {
				t.Errorf("%s: got %q, want %q", k, got, v)
			}
		}
		auth := req.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "vapid t=") {
			t.Errorf("bad auth: %s", auth)
		}
	})

	t.Run("payload", func(t *testing.T) {
		idLen := body[20]
		if idLen != 65 {
			t.Errorf("expected key len 65, got %d", idLen)
		}
		serverPubBytes := body[21 : 21+int(idLen)]
		serverPub, err := ecdh.P256().NewPublicKey(serverPubBytes)
		if err != nil {
			t.Fatalf("invalid ephemeral key: %v", err)
		}
		shared, err := k.ECDH(serverPub)
		if err != nil || len(shared) == 0 {
			t.Fatal("failed to derive shared secret")
		}
	})
}
