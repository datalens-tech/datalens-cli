package cli_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/cli"
	"github.com/datalens-tech/datalens-cli/internal/rpchelp"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRPCUsesOnPremisesServiceAccount(t *testing.T) {
	dir := t.TempDir()
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(dir, "config"), "DATALENS_TOKEN": "env-token"})
	keyPath := filepath.Join(dir, "account.pem")
	run := func(args ...string) {
		t.Helper()
		if result := execute(t.Context(), options, args...); result.code != 0 {
			t.Fatalf("%v: %s", args, result.stderr)
		}
	}
	run("profiles", "create", "job", "--installation-type=on-premises", "--endpoint=https://ui.example",
		"--api-endpoint=https://api.example/test", "--service-account-id=sa", "--key-id=key", "--private-key-file="+keyPath)
	run("rpc", "getWorkbook", "--help")

	enc := base64.RawURLEncoding
	token := enc.EncodeToString([]byte(`{"alg":"RS256"}`)) + "." + enc.EncodeToString([]byte(`{"exp":4102444800}`)) + ".c2ln"
	var requests []string
	wantToken := "explicit"
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		body := "{}"
		switch req.URL.String() {
		case "https://api.example/test/rpc/exchangeServiceAccountToken":
			body = `{"accessToken":"` + token + `"}`
		case "https://api.example/test/rpc/getWorkbook":
			if req.Header.Get("Authorization") != "Bearer "+wantToken {
				t.Fatal("unexpected RPC authorization")
			}
		default:
			t.Fatalf("unexpected request URL %s", req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	run("rpc", "getWorkbook", `{"workbookId":"wb"}`, "--auth-token=explicit")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600); err != nil {
		t.Fatal(err)
	}
	wantToken = token
	run("rpc", "getWorkbook", `{"workbookId":"wb"}`)
	if len(requests) != 3 || requests[1] != "https://api.example/test/rpc/exchangeServiceAccountToken" {
		t.Fatalf("unexpected request sequence: %v", requests)
	}
}

func TestRPCValidatesBeforeAuthentication(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	created := execute(t.Context(), options, "profiles", "create", "team", "--installation-type=yateam", "--token-cmd=datalens-missing-test-helper")
	if created.code != 0 {
		t.Fatalf("create profile: %#v", created)
	}
	options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached the API")
		return nil, nil
	})
	for _, flags := range [][]string{nil, {`{"dashboardId":42}`}, {"--args", `{"dashboardId":42}`}} {
		args := append([]string{"rpc", "getDashboard", "--profile=team", "--format=json"}, flags...)
		result := execute(t.Context(), options, args...)
		assertFailure(t, result, 2, "INVALID_USAGE")
		msg := document(t, result.stderr)["error"].(map[string]any)["message"].(string)
		if !strings.Contains(msg, rpchelp.ExampleCommands("getDashboard", `{"dashboardId":"your-dashboard-id"}`, " --profile=team")) {
			t.Errorf("missing minimal invocation: %s", msg)
		}
	}
}

func TestMissingOrganizationPrecedesTokenCommand(t *testing.T) {
	for _, installation := range []string{"cloud", "stackland"} {
		options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
		args := []string{"profiles", "create", "work", "--installation-type=" + installation, "--token-cmd=datalens-missing-test-helper"}
		if installation == "stackland" {
			args = append(args, "--endpoint=https://stack.example.org")
		}
		if got := execute(t.Context(), options, args...); got.code != 0 {
			t.Fatalf("create: %#v", got)
		}
		assertFailure(t, execute(t.Context(), options, "rpc", "getWorkbook", `{"workbookId":"test"}`, "--format=json"), 2, "ORGANIZATION_REQUIRED")
	}
}

func TestProfileTokenHelper(t *testing.T) {
	marker := os.Getenv("DATALENS_TEST_HELPER_MARKER")
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("called"), 0600); err != nil {
		os.Exit(2)
	}
	_, _ = io.WriteString(os.Stdout, "from-helper\n")
	os.Exit(0)
}

func TestRPCRunsOnlySelectedTokenCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "called")
	t.Setenv("DATALENS_TEST_HELPER_MARKER", marker)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := testOptions(map[string]string{
		"DATALENS_CONFIG_DIR": filepath.Join(dir, "config"), "DATALENS_TOKEN": "from-env", "DATALENS_ORG_ID": "org",
	})
	wantToken := ""
	requests := 0
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Header.Get("Authorization") != "Bearer "+wantToken {
			t.Error("request used the wrong token source")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	run := func(args ...string) {
		t.Helper()
		result := execute(t.Context(), options, args...)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("%v: %#v", args, result)
		}
	}
	run("profiles", "create", "work", "--installation-type=cloud", `--token-cmd="`+executable+`" -test.run=^TestProfileTokenHelper$`)
	run("profiles", "create", "env", "--installation-type=cloud")
	run("profiles", "activate", "work")
	run("profiles", "list")
	run("rpc", "getWorkbook", "--help")
	run("schema", "show", "getWorkbook")
	run("version")
	wantToken = "from-env"
	run("rpc", "getTenantDetails", "--profile=env")
	wantToken = "from-flag"
	run("tenant", "settings", "show", "--auth-token=from-flag")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("helper ran before it was needed: %v", err)
	}
	wantToken = "from-helper"
	run("tenant", "settings", "show")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config", "config.json"))
	if err != nil || strings.Contains(string(data), wantToken) {
		t.Fatalf("profile must not store the returned token: %v", err)
	}
	run("profiles", "create", "broken", "--installation-type=cloud", "--token-cmd=datalens-missing-test-helper")
	result := execute(t.Context(), options, "rpc", "getTenantDetails", "--profile=broken", "--format=json")
	assertFailure(t, result, 3, "AUTH_TOKEN_COMMAND_FAILED")
	if requests != 3 {
		t.Errorf("got %d API requests, want 3", requests)
	}
}

func createCloudProfile(t *testing.T, options cli.Options) {
	t.Helper()
	if result := execute(t.Context(), options, "profiles", "create", "cloud", "--installation-type=cloud"); result.code != 0 {
		t.Fatalf("create Cloud profile: %#v", result)
	}
}

func TestProfileRequiredBeforeFirstRequest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir, "DATALENS_TOKEN": "test-token", "DATALENS_ORG_ID": "org"})
	options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request reached the API before a profile was created")
		return nil, nil
	})
	for _, args := range [][]string{
		{"rpc", "getTenantDetails"},
		{"rpc", "getWorkbook", "--help"},
		{"schema", "show", "getWorkbook"},
		{"tenant", "settings", "show"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			result := execute(t.Context(), options, append(args, "--format=json")...)
			assertFailure(t, result, 2, "NO_PROFILES")
			message := document(t, result.stderr)["error"].(map[string]any)["message"].(string)
			if !strings.Contains(message, "datalens setup") || !strings.Contains(message, "datalens profiles create") {
				t.Fatalf("missing setup instructions: %s", message)
			}
		})
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("read-only command created configuration: %v", err)
	}
}

func TestFirstRequestWithCloudProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir, "DATALENS_TOKEN": "test-token", "DATALENS_ORG_ID": "org"})
	createCloudProfile(t, options)
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
}

func TestRPCWithLegacyProfileVersion(t *testing.T) {
	const payload = `{"limit":9007199254740993, "name":"two words"}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil || req.Method != "POST" || req.URL.Path != "/team/api/rpc/exampleAction" || string(body) != payload {
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
		"DATALENS_CONFIG_DIR": dir, "DATALENS_OAUTH_TOKEN": "session-token", "DATALENS_TOKEN": "unused-token", "DATALENS_ORG_ID": "unused-org",
	})
	options.Transport = server.Client().Transport
	for _, args := range [][]string{
		{"rpc", "exampleAction", payload, "--profile=team"},
		{"rpc", "exampleAction", "--profile=team", "--args", payload},
	} {
		result := execute(t.Context(), options, append(args, "--format=json", "--debug")...)
		if result.code != 0 || result.stdout != "[{\"diagnostics\":\"returned by API\"},9007199254740993]\n" || !strings.Contains(result.stderr, "datalens rpc") || strings.Contains(result.stderr, "session-token") {
			t.Fatalf("RPC result: %#v", result)
		}
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
		for _, text := range []string{"Usage:", "datalens rpc getTenantDetails", rpchelp.ExampleCommands("getWorkbook", `{"workbookId":"your-workbook-id"}`, "")} {
			if !strings.Contains(result.stdout, text) {
				t.Errorf("help %v is missing %q: %s", args, text, result.stdout)
			}
		}
	}
	for _, args := range [][]string{
		{"rpc", "--args", "{}"},
		{"rpc", "getTenantDetails", "extra"},
		{"rpc", "getTenantDetails", "{}", "{}", "--args={}"},
		{"rpc", "getTenantDetails", "{}", "--args={}"},
		{"rpc", "getTenantDetails", "--args", "{}", "{}"},
		{"rpc", "getTenantDetails", "[]"},
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
	createCloudProfile(t, options)
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
			want: []string{"Returns the specified workbook.", "Example:", rpchelp.ExampleCommands("getWorkbook", `{"workbookId":"your-workbook-id"}`, ""),
				"Required: workbookId", "includePermissionsInfo", "datalens schema show getWorkbook"},
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
		{"profiles", "create", "team", "--installation-type=cloud"},
		{"profiles", "create", "work", "--installation-type=on-premises", "--endpoint=https://work.example"},
		{"profiles", "create", "internal", "--installation-type=yateam"},
		{"profiles", "create", "stack", "--installation-type=stackland", "--endpoint=https://stack.example"},
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
		{"environment", "team", "", "specified common object", []string{"On-premises API reference", "specified shared entry"}},
		{"explicit", "team", "--profile=work", "specified shared entry", []string{"Cloud API reference", "specified common object"}},
		{"stackland", "", "--profile=stack", "specified shared entry", []string{"Cloud API reference", "specified common object"}},
		{"yateam", "", "--profile=internal", "Yandex Team API reference", []string{"Cloud API reference", "On-premises API reference"}},
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

func TestSchemaShowUsesProfileAndWritesOnlyJSON(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	createCloudProfile(t, options)
	options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("schema lookup contacted the API")
		return nil, nil
	})
	created := execute(t.Context(), options, "profiles", "create", "work", "--installation-type=yateam")
	if created.code != 0 {
		t.Fatalf("create: %#v", created)
	}
	for _, flags := range [][]string{nil, {"--format=json", "--debug"}} {
		args := append([]string{"schema", "show", "updateEditorChartSecrets", "--profile=work"}, flags...)
		result := execute(t.Context(), options, args...)
		if result.code != 0 {
			t.Fatalf("schema show: %#v", result)
		}
		schema := document(t, result.stdout)
		if schema["properties"] == nil || schema["$defs"] == nil || schema["diagnostics"] != nil {
			t.Fatalf("expected an argument schema with referenced definitions: %v", schema)
		}
	}
	for action, code := range map[string]string{"updateEditorChartSecrets": "RPC_ACTION_UNDOCUMENTED", "getTenantDetails": "RPC_SCHEMA_UNAVAILABLE"} {
		result := execute(t.Context(), options, "schema", "show", action, "--profile=cloud", "--format=json")
		assertFailure(t, result, 2, code)
	}
}

func TestTenantSettingsRequiresCloudProfile(t *testing.T) {
	env := map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config"), "DATALENS_ORG_ID": "stack-org"}
	options := testOptions(env)
	for _, installation := range []string{"yateam", "on-premises", "stackland"} {
		args := []string{"profiles", "create", installation, "--installation-type=" + installation, "--endpoint=https://service.example"}
		if result := execute(t.Context(), options, args...); result.code != 0 {
			t.Fatalf("create: %#v", result)
		}
		result := execute(t.Context(), options, "tenant", "settings", "show", "--profile="+installation, "--format=json")
		assertFailure(t, result, 2, "UNSUPPORTED_INSTALLATION")
	}
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://service.example/api/rpc/getTenantDetails" || req.Header.Get("x-dl-org-id") != "stack-org" {
			t.Errorf("unexpected Stackland request to %s with organization %q", req.URL, req.Header.Get("x-dl-org-id"))
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	raw := execute(t.Context(), options, "rpc", "getTenantDetails", "--profile=stackland", "--auth-token=test", "--format=json")
	if raw.code != 0 || raw.stdout != "{}\n" || raw.stderr != "" {
		t.Errorf("raw RPC: %#v", raw)
	}
}
