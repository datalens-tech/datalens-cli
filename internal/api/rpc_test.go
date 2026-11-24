package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestResolveToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("from-file\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DATALENS_TOKEN": "from-env", "DATALENS_TOKEN_PATH": path}
	lookup := func(name string) string { return env[name] }
	explicit := "from-flag"
	check := func(flag *string, want string) {
		t.Helper()
		got, err := ResolveToken(flag, lookup)
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	check(&explicit, "from-flag")
	explicit = ""
	_, err := ResolveToken(&explicit, lookup)
	requireCode(t, err, "AUTH_TOKEN_INVALID", 3)
	check(nil, "from-env")
	delete(env, "DATALENS_TOKEN")
	check(nil, "from-file")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveToken(nil, lookup)
	requireCode(t, err, "AUTH_TOKEN_INVALID", 3)
	env["DATALENS_TOKEN_PATH"] = filepath.Dir(path)
	_, err = ResolveToken(nil, lookup)
	requireCode(t, err, "AUTH_TOKEN_FILE_INVALID", 2)
	delete(env, "DATALENS_TOKEN_PATH")
	_, err = ResolveToken(nil, lookup)
	requireCode(t, err, "AUTH_TOKEN_REQUIRED", 3)
}

func TestRPCInstallationHeadersAndPayload(t *testing.T) {
	for _, test := range []struct{ installation, scheme, org string }{
		{"cloud", "Bearer", "test-org"},
		{"yateam", "OAuth", ""},
		{"onpremise-standalone", "Bearer", ""},
		{"stackland", "Bearer", ""},
	} {
		t.Run(test.installation, func(t *testing.T) {
			const args = `{"id":9007199254740993}`
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.RequestURI() != "/custom/api/rpc/getDetails" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if got := r.Header.Get("Authorization"); got != test.scheme+" fake-token" {
					t.Error("unexpected auth header")
				}
				if r.Header.Get("x-dl-org-id") != test.org || r.Header.Get("x-dl-api-version") != "3" ||
					r.Header.Get("Content-Type") != "application/json" || !strings.HasPrefix(r.UserAgent(), "datalens-cli/") {
					t.Error("unexpected request headers")
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != args {
					t.Errorf("unexpected arguments: %s %v", body, err)
				}
				io.WriteString(w, "[9007199254740993]")
			}))
			defer srv.Close()
			p := config.Profile{Installation: test.installation, APIEndpoint: srv.URL + "/custom/api/", Endpoint: srv.URL, OrganizationID: test.org}
			res, err := Call(t.Context(), srv.Client().Transport, p, "fake-token", "getDetails", json.RawMessage(args))
			if err != nil || string(res) != "[9007199254740993]" {
				t.Fatalf("got %s, %v", res, err)
			}
		})
	}
}

func TestValidateRPC(t *testing.T) {
	for _, tc := range []struct{ action, args string }{
		{"getTenantDetails", "[]"}, {"getTenantDetails", "null"}, {"getTenantDetails", "{} {}"},
		{"getTenantDetails", "\u00a0{}"},
		{"getTenantDetails", "{\"label\":\"\xff\"}"},
		{"../settings", "{}"}, {"path/action", "{}"}, {"get?query=1", "{}"}, {"get%2Faction", "{}"},
	} {
		requireCode(t, ValidateRPC(tc.action, json.RawMessage(tc.args)), "INVALID_USAGE", 2)
	}
}

func TestRPCErrorHandling(t *testing.T) {
	for _, tc := range []struct {
		status, exit int
		code, body   string
	}{
		{401, 3, "AUTH_REJECTED", "server-only-detail"},
		{403, 1, "API_ERROR", "server-only-detail"},
		{500, 1, "API_ERROR", "server-only-detail"},
		{307, 1, "API_ERROR", "server-only-detail"},
		{200, 1, "API_INVALID_RESPONSE", "server-only-detail"},
		{200, 1, "API_INVALID_RESPONSE", "{\"label\":\"\xff\"}"},
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/rpc/test" {
				t.Error("followed a redirect")
			}
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}))
		p := config.Profile{Installation: "onpremise-standalone", Endpoint: srv.URL, APIEndpoint: srv.URL}
		_, err := Call(t.Context(), srv.Client().Transport, p, "fake-token", "test", json.RawMessage("{}"))
		srv.Close()
		requireCode(t, err, tc.code, tc.exit)
		if strings.Contains(err.Error(), "server-only-detail") {
			t.Fatal("exposed the error body")
		}
	}
}

func TestRPCCancelDuringResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	p := config.Profile{Installation: "onpremise-standalone", Endpoint: srv.URL, APIEndpoint: srv.URL}
	done := make(chan error, 1)
	go func() {
		_, err := Call(ctx, srv.Client().Transport, p, "fake-token", "test", json.RawMessage("{}"))
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case err := <-done:
		t.Fatalf("call failed before reaching server: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server never received the request")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v; want cancellation", err)
	}
}

func requireCode(t *testing.T, err error, code string, exit int) {
	t.Helper()
	var failure *output.Error
	if !errors.As(err, &failure) || failure.Code != code || failure.ExitCode != exit {
		t.Fatalf("got %v; want %s and exit %d", err, code, exit)
	}
}
