package api

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/config"
)

type exchangeTransport func(*http.Request) (*http.Response, error)

func (f exchangeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServiceAccountTokenExchange(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "service account.pem")
	storeKey := func(block *pem.Block) {
		t.Helper()
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	storeKey(&pem.Block{Type: "PRIVATE KEY", Bytes: p8})
	p := config.Profile{APIEndpoint: "https://api.example.com/prefix/api", ServiceAccount: &config.ServiceAccount{ID: "subject", KeyID: "key", PrivateKeyFile: path}}
	enc := base64.RawURLEncoding
	accessToken := enc.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + enc.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, time.Now().Unix()+60))) + ".c2ln"
	transport := exchangeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.example.com/prefix/api/rpc/exchangeServiceAccountToken" || r.Method != http.MethodPost {
			t.Fatalf("unexpected exchange destination: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("x-dl-api-version") != buildinfo.APIVersion || r.Header.Get("Authorization") != "" {
			t.Fatal("unexpected request headers")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(body["saToken"], ".")
		if len(body) != 1 || len(parts) != 3 {
			t.Fatal("expected only saToken with a JWT")
		}
		decode := func(s string) []byte {
			t.Helper()
			b, err := enc.DecodeString(s)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		var header map[string]string
		if err := json.Unmarshal(decode(parts[0]), &header); err != nil || header["alg"] != "PS256" || header["kid"] != "key" || header["typ"] != "JWT" {
			t.Fatal("incorrect service account JWT header")
		}
		var claims struct {
			Iss string
			Iat int64
			Exp int64
		}
		if err := json.Unmarshal(decode(parts[1]), &claims); err != nil || claims.Iss != "subject" || claims.Exp-claims.Iat != 300 || time.Now().Unix()-claims.Iat > 5 {
			t.Fatal("incorrect service account JWT claims")
		}
		hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, hash[:], decode(parts[2]), &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}); err != nil {
			t.Fatal(err)
		}
		bodyJSON := fmt.Sprintf(`{"accessToken":%q}`, accessToken)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(bodyJSON))}, nil
	})
	for _, encoding := range []string{"PKCS8", "PKCS1"} {
		if encoding == "PKCS1" {
			storeKey(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		}
		got, err := exchangeServiceAccountToken(t.Context(), transport, p)
		if err != nil || got != accessToken {
			t.Fatalf("%s token exchange failed: %v", encoding, err)
		}
	}

	for _, tc := range []struct {
		status int
		body   string
		code   string
		exit   int
	}{
		{307, "", "API_ERROR", 1},
		{401, "sensitive upstream response", "AUTH_REJECTED", 3},
		{500, "sensitive upstream response", "API_ERROR", 1},
		{200, "invalid JSON", "API_INVALID_RESPONSE", 1},
		{200, strings.Repeat("x", 100<<10), "API_INVALID_RESPONSE", 1},
		{200, `{"accessToken":"invalid"}`, "AUTH_TOKEN_INVALID", 3},
	} {
		tr := exchangeTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "api.example.com" {
				t.Fatal("token exchange followed redirect")
			}
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://other.example/"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})
		_, err := exchangeServiceAccountToken(t.Context(), tr, p)
		requireCode(t, err, tc.code, tc.exit)
		if strings.Contains(err.Error(), "sensitive") {
			t.Fatal("server response leaked")
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = exchangeServiceAccountToken(ctx, exchangeTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }), p)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("token exchange lost cancellation: %v", err)
	}
}

func TestServiceAccountBadInputs(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "sensitive-name")
	if err := os.WriteFile(keyFile, []byte("sensitive-content"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := loadServiceAccountKey(keyFile)
	requireCode(t, err, "AUTH_KEY_INVALID", 2)
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatal("private key error exposed input")
	}
	enc := base64.RawURLEncoding
	for _, claims := range []string{`{}`, `{"exp":0}`, `{"exp":true}`, `{"exp":"9999999999"}`, `{"exp":1e1000}`, `null`} {
		token := enc.EncodeToString([]byte("{}")) + "." + enc.EncodeToString([]byte(claims)) + ".c2ln"
		if validAccessToken(token, time.Now()) {
			t.Fatalf("accepted invalid expiration: %s", claims)
		}
	}
}
