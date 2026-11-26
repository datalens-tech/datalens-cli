package cli_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRPCReadsExternalJSON(t *testing.T) {
	dir := t.TempDir()
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(dir, "config"), "DATALENS_ORG_ID": "org", "DATALENS_TOKEN": "token"})
	createCloudProfile(t, options)
	body := "{\n  \"workbookId\": \"from input\"\n}\n"
	file := filepath.Join(dir, "test args.json")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		got, err := io.ReadAll(req.Body)
		if err != nil || string(got) != body {
			t.Fatalf("RPC received %q, %v", got, err)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	for _, source := range []string{"-", "@" + file} {
		for _, args := range [][]string{{"rpc", "getWorkbook", source}, {"rpc", "getWorkbook", "--args", source}} {
			options.In = strings.NewReader(body)
			if result := execute(t.Context(), options, args...); result.code != 0 {
				t.Fatalf("RPC %q: %s", args, result.stderr)
			}
		}
	}
	if requests != 4 {
		t.Fatalf("received %d requests", requests)
	}
}

func TestRPCNoValidateOnlySkipsBundledSchema(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config"), "DATALENS_ORG_ID": "org"})
	createCloudProfile(t, options)
	in := `{"dashboardId":"id","branch":"future-branch"}`
	options.In = strings.NewReader(in)
	assertFailure(t, execute(t.Context(), options, "rpc", "getDashboard", "-", "--format=json"), 2, "INVALID_USAGE")
	var forwarded string
	options.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		forwarded = string(data)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	options.In = strings.NewReader(in)
	result := execute(t.Context(), options, "rpc", "getDashboard", "-", "--no-validate", "--auth-token=token", "--format=json")
	if result.code != 0 || forwarded != in {
		t.Fatalf("schema opt-out: %#v, sent %q", result, forwarded)
	}

	for _, args := range [][]string{
		{"rpc", "invalid/action", "{}"},
		{"rpc", "getDashboard", "[]"},
		{"rpc", "getDashboard", "{broken"},
		{"rpc", "getDashboard", "{\"id\":\"\xff\"}"},
		{"tenant", "settings", "show"},
	} {
		assertFailure(t, execute(t.Context(), options, append(args, "--no-validate", "--format=json")...), 2, "INVALID_USAGE")
	}
}

func TestRPCExternalInputFailures(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	createCloudProfile(t, options)
	missing := "@" + filepath.Join(t.TempDir(), "missing")
	assertFailure(t, execute(t.Context(), options, "rpc", "getWorkbook", missing, "--format=json"), 2, "INVALID_USAGE")
	options.In = strings.NewReader(strings.Repeat(" ", 16<<20) + "{}")
	oversized := execute(t.Context(), options, "rpc", "getWorkbook", "-", "--no-validate", "--format=json")
	assertFailure(t, oversized, 2, "INVALID_USAGE")
	if !strings.Contains(oversized.stderr, "16 MiB") {
		t.Fatalf("unexpected message: %s", oversized.stderr)
	}

	in, writer := io.Pipe()
	defer writer.Close()
	options.In = in
	for _, source := range []string{"-", missing} {
		result := execute(t.Context(), options, "rpc", "getWorkbook", source, "--help", "--timeout=1s")
		if result.code != 0 || !strings.Contains(result.stdout, "workbookId") {
			t.Fatalf("external input interrupted help: %#v", result)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := execute(ctx, options, "rpc", "getWorkbook", "-", "--timeout=20ms", "--format=json")
	assertFailure(t, result, 1, "TIMEOUT")
}
