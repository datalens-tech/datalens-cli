package cli_test

import (
	_ "embed"
	"errors"
	"flag"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testdata/cloud-api.json
var cloudSpec string

//go:embed testdata/team-api.json
var teamSpec string

func serveSchema(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet || !strings.HasSuffix(req.URL.Path, "/3/json/") || req.Header.Get("Authorization") != "" || req.Header.Get("x-dl-org-id") != "" {
		return nil, errors.New("unexpected API schema request")
	}
	switch req.URL.Host {
	case "api.datalens.tech":
		return schemaResponse(cloudSpec), nil
	case "api.datalens.yandex.net":
		return schemaResponse(teamSpec), nil
	}
	return schemaResponse(strings.Replace(cloudSpec, "specified common object", "specified shared entry", 1)), nil
}

func schemaResponse(data string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data))}
}

func TestSchemaUpdateRecoversProfileAndPreservesWorkingCopy(t *testing.T) {
	env := map[string]string{
		"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config"),
		"DATALENS_ORG_ID":     "org",
		"DATALENS_TOKEN":      "test-token",
	}
	options := testOptions(env)
	schemaStatus := http.StatusServiceUnavailable
	spec := cloudSpec
	schemaRequests, rpcRequests := 0, 0
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodGet:
			if req.URL.String() != "https://api.datalens.tech/3/json/" || req.Header.Get("Authorization") != "" {
				t.Fatalf("unexpected schema request: %s", req.URL)
			}
			schemaRequests++
			resp := schemaResponse(spec)
			resp.StatusCode = schemaStatus
			return resp, nil
		case http.MethodPost:
			if req.URL.String() != "https://api.datalens.tech/rpc/getWorkbook" || req.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("unexpected RPC request: %s", req.URL)
			}
			rpcRequests++
			return schemaResponse(`{"ok":true}`), nil
		default:
			t.Fatalf("unexpected method %s", req.Method)
			return nil, nil
		}
	})
	run := func(args ...string) execution {
		t.Helper()
		return execute(t.Context(), options, append(args, "--format=json")...)
	}
	created := run("profiles", "create", "work", "--installation-type=cloud")
	if created.code != 0 || created.stderr != "" {
		t.Fatalf("profile was not saved after the failed download: %#v", created)
	}
	warning, _ := document(t, created.stdout)["schemaWarning"].(string)
	if !strings.Contains(warning, "datalens schema update --profile=work") || !strings.Contains(warning, "503") {
		t.Fatalf("missing recovery instructions: %q", warning)
	}
	assertFailure(t, run("rpc", "getWorkbook"), 2, "SCHEMA_UNAVAILABLE")
	tenant := run("tenant", "settings", "show")
	assertFailure(t, tenant, 2, "SCHEMA_UNAVAILABLE")
	if !strings.Contains(tenant.stderr, "datalens schema update --profile=work") {
		t.Fatalf("missing schema recovery hint for tenant settings: %s", tenant.stderr)
	}
	assertFailure(t, run("rpc", "getWorkbook", "--help"), 2, "SCHEMA_UNAVAILABLE")
	assertFailure(t, run("schema", "show", "getWorkbook"), 2, "SCHEMA_UNAVAILABLE")
	if r := run("rpc", "getWorkbook", `{"workbookId":"id"}`, "--no-validate"); r.code != 0 || r.stdout != "{\"ok\":true}\n" {
		t.Fatalf("explicit validation bypass failed: %#v", r)
	}
	assertFailure(t, run("rpc", "getWorkbook", "[]", "--no-validate"), 2, "INVALID_USAGE")
	schemaStatus = http.StatusOK
	if r := run("schema", "update", "--profile=work"); r.code != 0 || document(t, r.stdout)["name"] != "work" {
		t.Fatalf("schema update: %#v", r)
	}
	assertFailure(t, run("rpc", "getWorkbook"), 2, "INVALID_USAGE")
	read := func() string {
		t.Helper()
		r := run("schema", "show", "getWorkbook")
		if r.code != 0 {
			t.Fatalf("read schema: %#v", r)
		}
		return r.stdout
	}
	original := read()
	if !strings.Contains(original, "workbookId") {
		t.Fatal(original)
	}
	spec = strings.ReplaceAll(cloudSpec, "workbookId", "bookId")
	if r := run("schema", "update"); r.code != 0 {
		t.Fatalf("refresh: %#v", r)
	}
	refreshed := read()
	if refreshed == original || !strings.Contains(refreshed, "bookId") {
		t.Fatal(refreshed)
	}
	spec = `{"openapi":"3.1.0","info":{"version":"4"},"paths":{}}`
	assertFailure(t, run("schema", "update"), 1, "SCHEMA_UPDATE_FAILED")
	if read() != refreshed || schemaRequests != 4 || rpcRequests != 1 {
		t.Fatalf("last cached schema was lost or commands made unexpected network calls: %d downloads, %d RPCs", schemaRequests, rpcRequests)
	}
}

func TestOnPremisesProfileDownloadsSchemaAtCreationAndUpdate(t *testing.T) {
	for _, installation := range []string{"on-premises", "stackland"} {
		t.Run(installation, func(t *testing.T) {
			options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
			downloads := 0
			options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://example.com/site/api/docs/swagger/3/json/" || req.Method != http.MethodGet || req.Header.Get("Authorization") != "" || req.Header.Get("x-dl-org-id") != "" {
					t.Fatalf("unexpected schema request: %s %s %v", req.Method, req.URL, req.Header)
				}
				downloads++
				return schemaResponse(strings.ReplaceAll(cloudSpec, "workbookId", "customBookId")), nil
			})
			run := func(args ...string) execution {
				t.Helper()
				return execute(t.Context(), options, args...)
			}
			if r := run("profiles", "create", "work", "--installation-type="+installation, "--endpoint=https://example.com/site"); r.code != 0 || strings.Contains(r.stdout, "retry") || downloads != 1 {
				t.Fatalf("create: %#v, downloads %d", r, downloads)
			}
			if r := run("schema", "update", "--profile=work"); r.code != 0 || downloads != 2 {
				t.Fatalf("update: %#v, downloads %d", r, downloads)
			}
			options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("offline commands contacted the server")
				return nil, nil
			})
			if r := run("rpc", "getWorkbook", "--help"); r.code != 0 || !strings.Contains(r.stdout, "Required: customBookId") {
				t.Fatalf("offline help: %#v", r)
			}
			schema := run("schema", "show", "getWorkbook")
			if schema.code != 0 || !strings.Contains(schema.stdout, "customBookId") {
				t.Fatalf("offline schema: %#v", schema)
			}
			invalid := run("rpc", "getWorkbook", "--profile=work", "--format=json")
			assertFailure(t, invalid, 2, "INVALID_USAGE")
			if !strings.Contains(invalid.stderr, "customBookId") || !strings.Contains(invalid.stderr, "--profile=work") {
				t.Fatalf("validation used a different schema: %s", invalid.stderr)
			}
		})
	}
}

var windowsInstallConfig = flag.String("windows-install-config", "", "fixture directory containing the installed CLI's smoke profile")

func TestRefreshInstalledProfileSchema(t *testing.T) {
	if *windowsInstallConfig == "" {
		t.Skip("run by the Windows installer test")
	}
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": *windowsInstallConfig})
	requests := 0
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() != "https://127.0.0.1:1/api/docs/swagger/3/json/" {
			t.Fatalf("unexpected installer test endpoint: %s", req.URL)
		}
		return serveSchema(req)
	})
	if r := execute(t.Context(), options, "schema", "update", "--profile=smoke"); r.code != 0 || requests != 1 {
		t.Fatalf("smoke schema download: %#v, requests %d", r, requests)
	}
}
