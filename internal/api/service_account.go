package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
)

func exchangeServiceAccountToken(ctx context.Context, transport http.RoundTripper, profile config.Profile) (string, error) {
	base, err := url.Parse(profile.APIEndpoint)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return "", &output.Error{Code: "PROFILE_INVALID", Message: "An HTTPS API endpoint is required to exchange a service account token.", ExitCode: 2}
	}
	tokenEndpoint := base.JoinPath("rpc", "exchangeServiceAccountToken")
	assertion, err := serviceAccountAssertion(profile.ServiceAccount)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]string{"saToken": assertion})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", &output.Error{Code: "PROFILE_INVALID", Message: "Cannot build the service account token exchange request.", ExitCode: 2}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-dl-api-version", buildinfo.APIVersion)
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		return "", &output.Error{Code: "API_UNAVAILABLE", Message: "Cannot reach the service account token exchange endpoint.", ExitCode: 1}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return "", &output.Error{Code: "AUTH_REJECTED", Message: "The API rejected the service account credentials.", ExitCode: 3}
		}
		return "", &output.Error{Code: "API_ERROR", Message: fmt.Sprintf("Service account token exchange returned HTTP %d.", resp.StatusCode), ExitCode: 1}
	}
	const maxResponse = maxTokenSize + 4096
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return "", cause
		}
		return "", &output.Error{Code: "API_READ_FAILED", Message: "Cannot read the service account token exchange response.", ExitCode: 1}
	}
	var result struct {
		AccessToken string `json:"accessToken"`
	}
	if len(data) > maxResponse || json.Unmarshal(data, &result) != nil {
		return "", &output.Error{Code: "API_INVALID_RESPONSE", Message: "Invalid service account token exchange response.", ExitCode: 1}
	}
	token := strings.TrimSpace(result.AccessToken)
	if len(token) > maxTokenSize || !validAccessToken(token, time.Now()) {
		return "", &output.Error{Code: "AUTH_TOKEN_INVALID", Message: "The token exchange returned an invalid or expired access token.", ExitCode: 3}
	}
	return token, nil
}

func serviceAccountAssertion(account *config.ServiceAccount) (string, error) {
	key, err := loadServiceAccountKey(account.PrivateKeyFile)
	if err != nil {
		return "", err
	}
	header, _ := json.Marshal(map[string]string{"alg": "PS256", "kid": account.KeyID, "typ": "JWT"})
	now := time.Now().Unix()
	claims, _ := json.Marshal(struct {
		Issuer   string `json:"iss"`
		IssuedAt int64  `json:"iat"`
		Expires  int64  `json:"exp"`
	}{account.ID, now, now + 300})
	enc := base64.RawURLEncoding
	unsigned := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	hash := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, hash[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	if err != nil {
		return "", &output.Error{Code: "AUTH_KEY_INVALID", Message: "Could not sign with the service account RSA private key.", ExitCode: 2}
	}
	return unsigned + "." + enc.EncodeToString(signature), nil
}

func loadServiceAccountKey(path string) (*rsa.PrivateKey, error) {
	invalid := &output.Error{Code: "AUTH_KEY_INVALID", Message: "The private key file must be a readable, unencrypted RSA private key in PEM format.", ExitCode: 2}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, invalid
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, invalid
	}
	defer file.Close()
	const maxKeySize = 64 << 10
	data, err := io.ReadAll(io.LimitReader(file, maxKeySize+1))
	if err != nil || len(data) > maxKeySize {
		return nil, invalid
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, invalid
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if key, ok := k.(*rsa.PrivateKey); err == nil && ok {
			return key, nil
		}
	}
	return nil, invalid
}

func validAccessToken(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || strings.ContainsAny(token, "\r\n") {
		return false
	}
	enc := base64.RawURLEncoding
	header, err := enc.DecodeString(parts[0])
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(header, &fields) != nil || fields == nil {
		return false
	}
	claims, err := enc.DecodeString(parts[1])
	var payload struct {
		Expires float64 `json:"exp"`
	}
	if err != nil || json.Unmarshal(claims, &payload) != nil || payload.Expires <= float64(now.UnixNano())/1e9 {
		return false
	}
	_, err = enc.DecodeString(parts[2])
	return err == nil
}
