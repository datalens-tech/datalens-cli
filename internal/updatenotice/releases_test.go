package updatenotice

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		current, candidate string
		want               bool
	}{
		{"1.0.9", "v1.0.10", true},
		{"dev", "1.0.0", false},
		{"1.0.0", "1.1", false},
		{"1.0.0", "2.0.0-beta.1", false},
		{"1.0.0-beta.2", "1.0.0-beta.10", true},
		{"1.0.0-beta.10", "1.0.0-beta.2", false},
		{"1.0.0-beta.10", "1.0.0", true},
		{"1.0.0-beta.10", "1.1.0-rc.1", false},
		{"1.0.0-beta.2", "1.0.1-beta", false},
		{"1.0.0-beta.2", "1.0.1-beta.preview", false},
		{"1.0.0-beta.2", "1.0.1-beta.01", false},
		{"1.0.0-beta.2", "1.0.1-beta.10.1", false},
		{"1.0.0", "1.0.1+build", false},
		{"1.0.0+abc", "1.0.0+def", false},
		{"1.0.0", "1.0.1\nrun this", false},
	} {
		if got := Newer(tc.current, tc.candidate); got != tc.want {
			t.Errorf("Newer(%q, %q) = %t, want %t", tc.current, tc.candidate, got, tc.want)
		}
	}
}

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLatestAcrossPages(t *testing.T) {
	for _, tc := range []struct{ current, want string }{
		{"0.9.0", "1.0.10"},
		{"0.9.0-beta.1", "2.0.0-beta.10"},
		{"1.0.10", "1.0.10"},
		{"3.0.0", "1.0.10"},
	} {
		page := 0
		client := &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
			page++
			if r.URL.Host != "api.github.com" || r.URL.Query().Get("page") != strconv.Itoa(page) || r.Header.Get("Authorization") != "" {
				t.Fatalf("unexpected request %v", r.URL)
			}
			header := make(http.Header)
			var body string
			switch page {
			case 1:
				header.Set("Link", `<https://api.github.com/next>; rel="next"`)
				body = `[{"tag_name":"v1.0.2"},{"tag_name":"v8.0.0","draft":true},{"tag_name":"v7.0.0","prerelease":true},{"tag_name":"v2.0.0-beta.2","prerelease":true}]`
			case 2, 3, 4, 5:
				header.Set("Link", `<https://api.github.com/next>; rel="next"`)
				body = `[]`
			case 6:
				body = `[{"tag_name":"v2.0.0-beta.10","prerelease":true},{"tag_name":"v1.0.10"},{"tag_name":"5.0.0"},{"tag_name":"v9.0.0-beta.preview","prerelease":true},{"tag_name":"v9.0.0+build"}]`
			default:
				t.Fatalf("unexpected page %d", page)
			}
			return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		got, err := Latest(context.Background(), client, tc.current)
		if err != nil || got != tc.want || page != 6 {
			t.Errorf("Latest(%q) = %q, %v (pages %d), want %q", tc.current, got, err, page, tc.want)
		}
	}
}

func TestLatestFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{403, "{}"}, {200, "bad json"}} {
		client := &http.Client{Transport: releaseTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}
		if version, err := Latest(context.Background(), client, "1.0.0"); err == nil || version != "" {
			t.Errorf("Latest returned %q, %v", version, err)
		}
	}
}
