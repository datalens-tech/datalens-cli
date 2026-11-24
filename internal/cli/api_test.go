package cli_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/cli"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFirstCloudRequestWithoutSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir, "DATALENS_TOKEN": "test-token", "DATALENS_ORG_ID": "org"})
	const payload = `{"tenantId":"demo","settings":{"limit":9007199254740993}}`
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if req.Method != "POST" || req.URL.String() != "https://api.datalens.tech/rpc/getTenantDetails" || string(body) != "{}" {
			t.Errorf("unexpected request: %s %s %s", req.Method, req.URL, body)
		}
		if req.Header.Get("Authorization") != "Bearer test-token" || req.Header.Get("x-dl-org-id") != "org" || req.Header.Get("x-dl-api-version") != "3" {
			t.Error("unexpected Cloud authentication or version headers")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(payload)), Header: make(http.Header)}, nil
	})
	for _, args := range [][]string{{"tenant", "settings", "show"}, {"rpc", "getTenantDetails"}} {
		result := execute(context.Background(), options, append(args, "--format", "json")...)
		if result.code != 0 || result.stdout != payload+"\n" || result.stderr != "" {
			t.Fatalf("args=%v: %#v", args, result)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only command created configuration: %v", err)
	}
}

func TestRPCWithLegacyProfileVersion(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil || req.Method != "POST" || req.URL.Path != "/team/api/rpc/exampleAction" || string(body) != `{"limit":42}` {
			t.Errorf("unexpected RPC request: %s %s %s (%v)", req.Method, req.URL.Path, body, err)
		}
		if req.Header.Get("Authorization") != "OAuth session-token" || req.Header.Get("x-dl-org-id") != "" || req.Header.Get("x-dl-api-version") != "3" {
			t.Error("unexpected team authentication or version headers")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"diagnostics":"returned by API"},9007199254740993]`)
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "config")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	profile := `{"version":2,"profiles":{"team":{"installationType":"yateam","endpoint":"` + server.URL + `/team","apiVersion":"4"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(profile), 0600); err != nil {
		t.Fatal(err)
	}
	options := testOptions(map[string]string{
		"DATALENS_CONFIG_DIR": dir, "DATALENS_TOKEN": "unused-token", "DATALENS_ORG_ID": "unused-org",
	})
	options.Transport = server.Client().Transport
	result := execute(context.Background(), options, "rpc", "exampleAction", "--args", `{"limit":42}`, "--profile=team", "--auth-token=session-token", "--format=json", "--debug")
	if result.code != 0 || result.stdout != "[{\"diagnostics\":\"returned by API\"},9007199254740993]\n" || !strings.Contains(result.stderr, "datalens rpc") || strings.Contains(result.stderr, "session-token") {
		t.Fatalf("RPC result: %#v", result)
	}
}

func TestRPCInputAndOfflineHelp(t *testing.T) {
	options := testOptions(nil)
	options.UserConfigDir = func() (string, error) { t.Fatal("unexpected config read"); return "", nil }
	for _, args := range [][]string{{"tenant"}, {"tenant", "settings"}} {
		result := execute(context.Background(), options, args...)
		if result.code != 0 || !strings.Contains(result.stdout, "Usage:") || result.stderr != "" {
			t.Fatalf("help %v: %#v", args, result)
		}
	}
	for _, args := range [][]string{{"rpc"}, {"rpc", "--help"}, {"help", "rpc"}} {
		result := execute(context.Background(), options, args...)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("help %v: %#v", args, result)
		}
		for _, text := range []string{"Usage:", "datalens rpc getTenantDetails", "datalens rpc getWorkbook --args", `"workbookId"`} {
			if !strings.Contains(result.stdout, text) {
				t.Errorf("help %v is missing %q: %s", args, text, result.stdout)
			}
		}
	}
	for _, args := range [][]string{
		{"rpc", "--args", "{}"},
		{"rpc", "getTenantDetails", "extra"},
		{"rpc", "getTenantDetails", "--args", "[]"},
		{"rpc", "getTenantDetails", "--args", "not JSON"},
		{"rpc", "getTenantDetails", "--profile="},
	} {
		result := execute(context.Background(), options, append(args, "--format=json")...)
		if result.code != 2 || result.stdout != "" || !json.Valid([]byte(result.stderr)) {
			t.Fatalf("%v: %#v", args, result)
		}
	}
}

func TestRPCFlagsDoNotEchoValuesOnParseError(t *testing.T) {
	result := execute(context.Background(), cli.Options{}, "rpc", "getTenantDetails", "--format=json", "-auth-token=example-placeholder")
	if result.code != 2 || strings.Contains(result.stderr, "example-placeholder") || result.stdout != "" {
		t.Fatalf("parse error: %#v", result)
	}
}

func TestRPCActionHelp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(nil)
	options.LookupEnv = func(name string) string {
		switch name {
		case "DATALENS_CONFIG_DIR":
			return dir
		case "DATALENS_PROFILE":
			return ""
		default:
			t.Fatalf("unexpected environment lookup: %s", name)
			return ""
		}
	}
	options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("RPC help made a network request")
		return nil, nil
	})
	for _, tc := range []struct {
		action, flag string
		want, absent []string
	}{
		{
			action: "getWorkbook", flag: "--help",
			want: []string{"Returns the specified workbook.", "Example:", "datalens rpc getWorkbook --args",
				"Arguments schema:", `"includePermissionsInfo"`, `"required"`, `"workbookId"`},
		},
		{
			action: "getTenantDetails", flag: "-h",
			want:   []string{"Returns the current tenant state and public settings.", "Example:", "datalens rpc getTenantDetails"},
			absent: []string{"Arguments schema:", "getWorkbook"},
		},
		{
			action: "siteSpecificAction", flag: "--help",
			want:   []string{"No bundled documentation", "datalens rpc siteSpecificAction"},
			absent: []string{"Arguments schema:", "Example:"},
		},
	} {
		t.Run(tc.action, func(t *testing.T) {
			result := execute(t.Context(), options, "rpc", tc.action, tc.flag)
			if result.code != 0 || result.stderr != "" {
				t.Fatalf("help failed: %#v", result)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.stdout, want) {
					t.Errorf("missing %q in help: %s", want, result.stdout)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(result.stdout, absent) {
					t.Errorf("unexpected %q in help: %s", absent, result.stdout)
				}
			}
		})
	}
	for _, args := range [][]string{{"rpc", "private/value", "--help"}, {"rpc", "getWorkbook", "private/value", "-h"}, {"rpc", "getWorkbook", "--profile=", "--help"}} {
		result := execute(t.Context(), options, append(args, "--format=json")...)
		assertFailure(t, result, 2, "INVALID_USAGE")
		if strings.Contains(result.stderr, "private/value") {
			t.Fatal("usage error echoed user input")
		}
	}
}

func TestRPCActionHelpSelectsInstallation(t *testing.T) {
	env := map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")}
	options := testOptions(env)
	for _, args := range [][]string{
		{"profiles", "create", "--name=team", "--installation-type=cloud"},
		{"profiles", "create", "--name=work", "--installation-type=onpremise-standalone", "--endpoint=https://work.example"},
		{"profiles", "create", "--name=internal", "--installation-type=yateam"},
		{"profiles", "create", "--name=stack", "--installation-type=stackland", "--endpoint=https://stack.example"},
		{"profiles", "activate", "work"},
	} {
		if result := execute(t.Context(), options, args...); result.code != 0 {
			t.Fatalf("%v: %#v", args, result)
		}
	}
	options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("help contacted the API")
		return nil, nil
	})
	for _, tc := range []struct {
		name, envProfile, flag, want string
		absent                       []string
	}{
		{"active", "", "", "specified shared entry", []string{"Cloud API reference", "specified common object"}},
		{"environment", "team", "", "specified common object", []string{"Enterprise API reference", "specified shared entry"}},
		{"explicit", "team", "--profile=work", "specified shared entry", []string{"Cloud API reference", "specified common object"}},
		{"stackland", "", "--profile=stack", "specified shared entry", []string{"Cloud API reference", "specified common object"}},
		{"yateam", "", "--profile=internal", "No bundled documentation for Yandex Team", []string{"Cloud API reference", "Enterprise API reference", "specified common object", "specified shared entry", "Arguments schema:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env["DATALENS_PROFILE"] = tc.envProfile
			args := []string{"rpc", "listSharedEntryAccessBindings", "--help"}
			if tc.flag != "" {
				args = append(args, tc.flag)
			}
			result := execute(t.Context(), options, args...)
			if result.code != 0 || result.stderr != "" || !strings.Contains(result.stdout, tc.want) {
				t.Fatalf("help: %#v", result)
			}
			for _, phrase := range tc.absent {
				if strings.Contains(result.stdout, phrase) {
					t.Errorf("unexpected %q in help: %s", phrase, result.stdout)
				}
			}
		})
	}
	result := execute(t.Context(), options, "rpc", "getWorkbook", "--profile=missing", "--help", "--format=json")
	assertFailure(t, result, 2, "PROFILE_NOT_FOUND")
}

func TestTenantSettingsRequiresCloudProfile(t *testing.T) {
	env := map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")}
	options := testOptions(env)
	for _, installation := range []string{"yateam", "onpremise-standalone", "stackland"} {
		args := []string{"profiles", "create", "--name=" + installation, "--installation-type=" + installation, "--endpoint=https://service.example"}
		if result := execute(t.Context(), options, args...); result.code != 0 {
			t.Fatalf("create: %#v", result)
		}
		result := execute(t.Context(), options, "tenant", "settings", "show", "--profile="+installation, "--format=json")
		assertFailure(t, result, 2, "UNSUPPORTED_INSTALLATION")
	}
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/rpc/getTenantDetails" {
			t.Errorf("unexpected RPC path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	raw := execute(t.Context(), options, "rpc", "getTenantDetails", "--profile=stackland", "--auth-token=test", "--format=json")
	if raw.code != 0 || raw.stdout != "{}\n" || raw.stderr != "" {
		t.Errorf("raw RPC: %#v", raw)
	}
}
