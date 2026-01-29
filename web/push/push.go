package push

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/hkdf"
)

// ios is special :)
// https://www.dr-lex.be/info-stuff/web-push.html
// https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/

type Server struct {
	Email, Pub string
	Priv       *ecdsa.PrivateKey
}

type Sub struct {
	UserID   string `json:"userId"`
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func GeneratePrivateKey() string {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return encodePrivateKey(k)
}

func PublicKey(privateKeyPEM string) string {
	k, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		panic(fmt.Sprintf("PublicKey: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(marshalPublicKey(k))
}

func New(email, privateKeyPEM string) (*Server, error) {
	k, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}
	return &Server{
		Email: email,
		Priv:  k,
		Pub:   base64.RawURLEncoding.EncodeToString(marshalPublicKey(k)),
	}, nil
}

func (s *Server) Send(sub Sub, body []byte) error {
	const DefaultTTL = "86400"

	payload, err := s.encrypt(sub, body)
	if err != nil {
		return err
	}
	u, err := url.Parse(sub.Endpoint)
	if err != nil {
		return err
	}
	jwt, err := s.signVapid(u.Scheme + "://" + u.Host)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", sub.Endpoint, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("vapid t=%s, k=%s", jwt, s.Pub))
	req.Header.Set("Crypto-Key", "p256ecdsa="+s.Pub)
	req.Header.Set("VAPID", "k="+s.Pub)
	req.Header.Set("TTL", DefaultTTL)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		bs, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("status %d: error reading body: %v", resp.StatusCode, err)
		}
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(bs))
	}
	return nil
}

func (s *Server) ExportKey() string {
	return encodePrivateKey(s.Priv)
}

func (s *Server) encrypt(sub Sub, body []byte) (io.Reader, error) {
	clientPub, err := decode64(sub.Keys.P256dh)
	if err != nil {
		return nil, err
	}
	authSecret, err := decode64(sub.Keys.Auth)
	if err != nil {
		return nil, err
	}
	serverKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	serverPub := serverKey.PublicKey().Bytes()
	clientKey, err := ecdh.P256().NewPublicKey(clientPub)
	if err != nil {
		return nil, errors.New("invalid client key")
	}
	sharedBytes, err := serverKey.ECDH(clientKey)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	prk := hkdf.Extract(sha256.New, sharedBytes, authSecret)
	ctx := append(append([]byte("WebPush: info\x00"), clientPub...), serverPub...)
	ikmReader := hkdf.Expand(sha256.New, prk, ctx)
	ikm := make([]byte, 32)
	if _, err := io.ReadFull(ikmReader, ikm); err != nil {
		return nil, err
	}
	cekReader := hkdf.Expand(sha256.New, hkdf.Extract(sha256.New, ikm, salt), []byte("Content-Encoding: aes128gcm\x00"))
	cek := make([]byte, 16)
	if _, err := io.ReadFull(cekReader, cek); err != nil {
		return nil, err
	}
	nonceReader := hkdf.Expand(sha256.New, hkdf.Extract(sha256.New, ikm, salt), []byte("Content-Encoding: nonce\x00"))
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(nonceReader, nonce); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ciphertext := gcm.Seal(nil, nonce, append(body, 0x02), nil)
	rs := make([]byte, 4)
	binary.BigEndian.PutUint32(rs, uint32(len(ciphertext)+len(serverPub)+1))
	h := slices.Concat(salt, rs, []byte{byte(len(serverPub))}, serverPub, ciphertext)
	return bytes.NewReader(h), nil
}

func (s *Server) signVapid(aud string) (string, error) {
	sub := s.Email
	if sub == "" {
		return "", fmt.Errorf("webpush: invalid or empty email: %q", sub)
	}
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	x := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"aud":"%s","exp":%d,"sub":"mailto:%s"}`,
		aud, time.Now().Add(12*time.Hour).Unix(), sub)))
	hash := sha256.Sum256([]byte(h + "." + x))
	r, sig, err := ecdsa.Sign(rand.Reader, s.Priv, hash[:])
	if err != nil {
		return "", err
	}
	sigBytes := make([]byte, 64)
	r.FillBytes(sigBytes[0:32])
	sig.FillBytes(sigBytes[32:64])
	return h + "." + x + "." + base64.RawURLEncoding.EncodeToString(sigBytes), nil
}

func marshalPublicKey(key *ecdsa.PrivateKey) []byte {
	pub, err := key.PublicKey.ECDH()
	if err != nil {
		panic(err)
	}
	return pub.Bytes()
}

func encodePrivateKey(k *ecdsa.PrivateKey) string {
	b, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b}))
}

func parsePrivateKey(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("failed to parse PEM")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func decode64(in string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(in, "="))
}
