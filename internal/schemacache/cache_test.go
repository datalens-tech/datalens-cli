package schemacache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/config"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const spec = `{"openapi":"3.1.0","info":{"title":"API","version":"3"},"paths":{"/rpc/testAction":{"post":{"description":"Test action","responses":{}}}}}`

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestUpdateAndLoad(t *testing.T) {
	dir := t.TempDir()
	for _, installation := range []string{"cloud", "yateam", "on-premises", "stackland"} {
		profile := config.Profile{Installation: installation, Endpoint: "https://ui.example", APIEndpoint: "https://schemas.example:443/prefix/"}
		if _, err := Load(dir, profile); err == nil {
			t.Fatal("unexpected schema before update for", installation)
		}
		err := Update(t.Context(), dir, profile, roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://schemas.example/prefix/3/json/" || r.Method != http.MethodGet ||
				r.Header.Get("Authorization") != "" || r.Header.Get("X-Dl-Org-Id") != "" || r.Header.Get("X-Dl-Api-Version") != "3" {
				t.Fatalf("unexpected schema request: %s %s %v", r.Method, r.URL, r.Header)
			}
			return response(200, spec), nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		profile.APIEndpoint = "https://schemas.example/prefix"
		catalog, err := Load(dir, profile)
		if err != nil || catalog.Actions["testAction"].Description != "Test action" {
			t.Fatalf("load %s schema: %v", installation, err)
		}
		_, path, err := location(dir, profile)
		if err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatal("expected private cache file", err)
		}
		profile.APIEndpoint = "https://schemas.example/other"
		if _, err := Load(dir, profile); err == nil {
			t.Fatal("reused schema for a different API endpoint")
		}
	}
}

func TestOnPremisesSchemaRoutes(t *testing.T) {
	dir := t.TempDir()
	for _, installation := range []string{"on-premises", "stackland"} {
		t.Run(installation, func(t *testing.T) {
			profile := config.Profile{Installation: installation, Endpoint: "https://ui.example/datalens/"}
			if installation == "stackland" {
				profile.OrganizationID = "org"
			}
			const mounted = "https://ui.example/datalens/api"
			err := Update(t.Context(), dir, profile, roundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != mounted+"/docs/swagger/3/json/" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Dl-Org-Id") != "" {
					t.Fatalf("unexpected schema request: %s %v", r.URL, r.Header)
				}
				return response(http.StatusOK, spec), nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			profile.APIEndpoint = mounted
			if _, err := Load(dir, profile); err != nil {
				t.Fatalf("explicit UI-mounted endpoint: %v", err)
			}
			profile.Endpoint = "https://another-ui.example"
			if _, err := Load(dir, profile); err == nil {
				t.Fatal("reused UI-mounted schema for a dedicated API endpoint")
			}
			sources, _, err := location(dir, profile)
			if err != nil || !slices.Equal(sources, []string{mounted + "/3/json/", mounted + "/docs/swagger/3/json/"}) {
				t.Fatalf("dedicated API: %q, %v", sources, err)
			}
		})
	}
}

func TestSchemaRouteFallback(t *testing.T) {
	for _, tc := range []struct {
		name, installation, endpoint string
		status                       int
		fallback                     bool
	}{
		{"proxied on-premises API", "on-premises", "https://api.example/prefix", 404, true},
		{"proxied Stackland API", "stackland", "https://api.example/prefix", 404, true},
		{"permission denied", "stackland", "https://api.example/prefix", 401, false},
		{"unavailable", "on-premises", "https://api.example/prefix", 503, false},
		{"wrong version", "on-premises", "https://api.example/prefix", 200, false},
		{"UI-mounted API", "stackland", "https://ui.example/api", 404, false},
		{"Cloud", "cloud", "https://api.example/prefix", 404, false},
		{"YaTeam", "yateam", "https://api.example/prefix", 404, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := config.Profile{Installation: tc.installation, Endpoint: "https://ui.example", APIEndpoint: tc.endpoint}
			requests := []string{}
			err := Update(t.Context(), t.TempDir(), profile, roundTrip(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.URL.String())
				if req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.Header.Get("x-dl-org-id") != "" || req.Header.Get("x-dl-api-version") != "3" {
					t.Fatal("unexpected request", req.Method, req.Header)
				}
				if len(requests) == 1 {
					return response(tc.status, strings.Replace(spec, `"version":"3"`, `"version":"4"`, 1)), nil
				}
				return response(http.StatusOK, spec), nil
			}))
			want := []string{tc.endpoint + "/3/json/"}
			if tc.endpoint == "https://ui.example/api" {
				want[0] = tc.endpoint + "/docs/swagger/3/json/"
			}
			if tc.fallback {
				want = append(want, tc.endpoint+"/docs/swagger/3/json/")
			}
			if !slices.Equal(requests, want) || (err == nil) != tc.fallback {
				t.Fatalf("got %v, %v; want %v, success=%v", requests, err, want, tc.fallback)
			}
		})
	}
}

func TestFailedUpdatePreservesCache(t *testing.T) {
	dir := t.TempDir()
	profile := config.Profile{Installation: "cloud"}
	if err := Update(t.Context(), dir, profile, roundTrip(func(*http.Request) (*http.Response, error) {
		return response(200, spec), nil
	})); err != nil {
		t.Fatal(err)
	}
	_, path, err := location(dir, profile)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"http error", 403, "server-private-value"},
		{"invalid JSON", 200, "server-private-value"},
		{"wrong API version", 200, strings.Replace(spec, `"version":"3"`, `"version":"4"`, 1)},
		{"oversized", 200, strings.Repeat(" ", maxSchemaSize+1)},
		{"redirect", 302, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			err := Update(t.Context(), dir, profile, roundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				resp := response(test.status, test.body)
				resp.Header.Set("Location", "https://other.example/")
				return resp, nil
			}))
			if err == nil || strings.Contains(err.Error(), "server-private-value") || calls != 1 {
				t.Fatalf("unexpected result: %v, calls=%d", err, calls)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatal("previous schema changed", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	err = Update(ctx, dir, profile, roundTrip(func(*http.Request) (*http.Response, error) {
		cancel()
		return response(http.StatusOK, spec), nil
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update: %v", err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, profile); err == nil {
		t.Fatal("accepted corrupt cached schema")
	}
}
