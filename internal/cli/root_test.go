package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/cli"
	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestHelpAndVersionIgnoreConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("not valid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []bool{false, true} {
		for _, args := range [][]string{{"--help"}, {"help"}, {"version"}, {"version", "--format", "json"}, {"profiles", "--help"}, {"help", "profiles", "create"}, {"help", "tenant", "settings", "show"}} {
			env := map[string]string{"DATALENS_PROFILE": "missing"}
			if corrupt {
				env["DATALENS_CONFIG_DIR"] = dir
			}
			options := testOptions(env)
			options.UserConfigDir = func() (string, error) {
				t.Error("offline command resolved the configuration directory")
				return "", errors.New("unavailable")
			}
			result := execute(context.Background(), options, args...)
			if result.code != 0 || result.stderr != "" || result.stdout == "" {
				t.Errorf("corrupt=%t args=%v: %#v", corrupt, args, result)
			}
			if args[0] == "help" && len(args) > 1 && !strings.Contains(result.stdout, "datalens "+strings.Join(args[1:], " ")) {
				t.Errorf("missing help for %v in %q", args[1:], result.stdout)
			}
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "not valid JSON" {
		t.Fatalf("offline commands modified config: %q, %v", data, err)
	}
}

func TestVersionJSONDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name        string
		terminal    bool
		explicit    bool
		nonInteract bool
	}{
		{name: "pipe", nonInteract: true},
		{name: "terminal", terminal: true},
		{name: "explicit", terminal: true, explicit: true, nonInteract: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := testOptions(nil)
			options.IsTerminal = func(io.Reader) bool { return test.terminal }
			args := []string{"version", "--format", "json", "--debug"}
			if test.explicit {
				args = append(args, "--non-interactive")
			}
			result := execute(context.Background(), options, args...)
			if result.code != 0 || result.stderr != "" {
				t.Fatalf("version failed: %#v", result)
			}
			want := map[string]any{
				"version": "0.1.2", "apiVersion": buildinfo.APIVersion, "revision": "abcdef", "os": "linux", "arch": "amd64", "goVersion": "go-test",
				"diagnostics": map[string]any{"version": "0.1.2", "operation": "datalens version", "nonInteractive": test.nonInteract},
			}
			if got := document(t, result.stdout); !reflect.DeepEqual(got, want) {
				t.Fatalf("version=%#v, want %#v", got, want)
			}
		})
	}
}

func TestProfileLifecycleAndSelection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	env := map[string]string{"DATALENS_CONFIG_DIR": dir}
	options := testOptions(env)
	run := func(args ...string) map[string]any {
		t.Helper()
		result := execute(context.Background(), options, append(args, "--format", "json")...)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("%v: %#v", args, result)
		}
		return document(t, result.stdout)
	}
	for _, name := range []string{"saved", "environment", "explicit"} {
		created := run("profiles", "create", name, "--installation-type", "yateam")
		if created["name"] != name {
			t.Fatalf("created: %#v", created)
		}
	}
	run("profiles", "activate", "saved")
	listed := run("profiles", "list")
	if listed["defaultProfile"] != "saved" || listed["selectedProfile"] != "saved" {
		t.Fatalf("saved selection: %#v", listed)
	}
	profiles := listed["profiles"].([]any)
	names := make([]string, len(profiles))
	for i, value := range profiles {
		profile := value.(map[string]any)
		names[i] = profile["name"].(string)
		if profile["endpoint"] != "https://datalens.yandex-team.ru" || profile["apiEndpoint"] != "https://api.datalens.yandex.net" || profile["installationType"] != "yateam" {
			t.Fatalf("profile: %#v", profile)
		}
	}
	if !reflect.DeepEqual(names, []string{"environment", "explicit", "saved"}) {
		t.Fatalf("profiles are not sorted: %v", names)
	}
	env["DATALENS_PROFILE"] = "environment"
	if selected := run("profiles", "list")["selectedProfile"]; selected != "environment" {
		t.Fatalf("environment selection = %v", selected)
	}
	if selected := run("profiles", "list", "--profile", "explicit")["selectedProfile"]; selected != "explicit" {
		t.Fatalf("explicit selection = %v", selected)
	}
	for _, args := range [][]string{
		{"profiles", "list", "--profile", "missing", "--format", "json"},
		{"profiles", "activate", "missing", "--format", "json"},
	} {
		assertFailure(t, execute(context.Background(), options, args...), 2, "PROFILE_NOT_FOUND")
	}
	env["DATALENS_PROFILE"] = "missing"
	assertFailure(t, execute(context.Background(), options, "profiles", "list", "--format", "json"), 2, "PROFILE_NOT_FOUND")
	if selected := run("profiles", "list", "--profile", "explicit")["selectedProfile"]; selected != "explicit" {
		t.Fatalf("explicit profile did not override missing environment profile: %v", selected)
	}
	delete(env, "DATALENS_PROFILE")
	run("profiles", "delete", "saved", "--confirm=yes")
	listed = run("profiles", "list")
	if listed["defaultProfile"] != "environment" || listed["selectedProfile"] != "environment" || len(listed["profiles"].([]any)) != 2 {
		t.Fatalf("deleted default: %#v", listed)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
}

func TestProfileMutationsRejectExplicitSelector(t *testing.T) {
	options := testOptions(nil)
	options.UserConfigDir = func() (string, error) {
		t.Fatal("conflicting profile arguments accessed configuration")
		return "", nil
	}
	options.IsTerminal = func(io.Reader) bool { return true }
	for _, args := range [][]string{
		{"--profile=other", "profiles", "create", "work"},
		{"profiles", "--profile=work", "activate", "work"},
		{"profiles", "delete", "work", "--profile=other"},
	} {
		result := execute(t.Context(), options, args...)
		if result.code != 2 || result.stdout != "" || !strings.Contains(result.stderr, "omit --profile") || strings.Contains(result.stderr, "[y/N]") {
			t.Errorf("%v: %#v", args, result)
		}
	}
}

func TestCustomProfileEndpoints(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	args := []string{"profiles", "create", "corp", "--endpoint=https://corp.example.com", "--format=json"}
	assertFailure(t, execute(context.Background(), options, args...), 2, "PROFILE_INVALID")
	args = append(args, "--installation-type=on-premises", "--api-endpoint=https://rpc.example.com/dl")
	if result := execute(context.Background(), options, args...); result.code != 0 {
		t.Fatalf("create: %#v", result)
	}
	result := execute(context.Background(), options, "profiles", "list", "--format=json")
	want := map[string]any{
		"name": "corp", "installationType": "on-premises", "endpoint": "https://corp.example.com",
		"apiEndpoint": "https://rpc.example.com/dl",
	}
	if got := document(t, result.stdout)["profiles"].([]any)[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("custom profile: %#v", got)
	}
}

func TestCreateProfileWithServiceAccount(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(dir, "config")})
	create := []string{"profiles", "create", "corp", "--installation-type=on-premises", "--endpoint=https://bi.example.com"}
	flags := []string{"--service-account-id=my-account", "--key-id=my-key", "--private-key-file=key.pem"}
	created := execute(t.Context(), options, append(create, flags...)...)
	if created.code != 0 {
		t.Fatalf("create: %#v", created)
	}
	t.Chdir(t.TempDir())
	listed := execute(t.Context(), options, "profiles", "list", "--format=json")
	if listed.code != 0 {
		t.Fatalf("list: %#v", listed)
	}
	account := document(t, listed.stdout)["profiles"].([]any)[0].(map[string]any)["serviceAccount"]
	want := map[string]any{"id": "my-account", "keyId": "my-key", "privateKeyFile": filepath.Join(dir, "key.pem")}
	if !reflect.DeepEqual(account, want) {
		t.Errorf("saved account = %v, want %v", account, want)
	}
}

func TestRejectInvalidServiceAccountProfiles(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	flags := []string{"--service-account-id=sa", "--key-id=key", "--private-key-file=key.pem"}
	for _, tc := range []struct {
		installation string
		flags        []string
	}{
		{"on-premises", []string{"--private-key-file="}},
		{"on-premises", flags[:2]},
		{"on-premises", append(flags, "--token-cmd=helper")},
		{"cloud", flags},
		{"yateam", flags},
		{"stackland", flags},
	} {
		args := []string{"profiles", "create", "work", "--installation-type=" + tc.installation, "--endpoint=https://bi.example.com", "--format=json"}
		assertFailure(t, execute(t.Context(), options, append(args, tc.flags...)...), 2, "PROFILE_INVALID")
	}
}

func TestProfileCreatePersistsOnlyExplicitOrganization(t *testing.T) {
	options := testOptions(map[string]string{
		"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config"),
		"DATALENS_ORG_ID":     "environment-org",
	})
	for _, args := range [][]string{
		{"profiles", "create", "work", "--installation-type=cloud"},
		{"profiles", "create", "other", "--organization=explicit-org", "--endpoint=https://datalens.ru"},
	} {
		if result := execute(context.Background(), options, args...); result.code != 0 {
			t.Fatalf("create failed: %#v", result)
		}
	}
	result := execute(context.Background(), options, "profiles", "list", "--format=json")
	if result.code != 0 {
		t.Fatalf("list failed: %#v", result)
	}
	profiles := document(t, result.stdout)["profiles"].([]any)
	if len(profiles) != 2 || profiles[0].(map[string]any)["organizationId"] != "explicit-org" || profiles[1].(map[string]any)["organizationId"] != nil {
		t.Fatalf("persisted organizations: %#v", profiles)
	}
}

func TestRecreateCloudProfileWithNewOrganization(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	run := func(args ...string) map[string]any {
		t.Helper()
		result := execute(t.Context(), options, append(args, "--format=json")...)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("%v: %#v", args, result)
		}
		return document(t, result.stdout)
	}
	run("profiles", "create", "work", "--installation-type=cloud", "--organization=old-org")
	before := run("profiles", "list")
	assertFailure(t, execute(t.Context(), options, "profiles", "update", "work", "--organization=new-org", "--format=json"), 2, "INVALID_USAGE")
	assertFailure(t, execute(t.Context(), options, "profiles", "create", "work", "--organization=new-org", "--format=json"), 2, "PROFILE_EXISTS")
	if got := run("profiles", "list"); !reflect.DeepEqual(got, before) {
		t.Fatalf("existing profile changed: %#v", got)
	}
	run("profiles", "delete", "work", "--confirm=yes")
	run("profiles", "create", "work", "--installation-type=cloud", "--organization=new-org")
	profiles := run("profiles", "list")["profiles"].([]any)
	work := profiles[0].(map[string]any)
	if work["name"] != "work" || work["organizationId"] != "new-org" {
		t.Fatalf("recreated profile: %#v", work)
	}
}

func TestProfileDeleteConfirmation(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	result := execute(context.Background(), options, "profiles", "create", "work", "--installation-type=yateam")
	if result.code != 0 {
		t.Fatalf("create: %#v", result)
	}
	args := []string{"profiles", "delete", "work"}
	assertFailure(t, execute(context.Background(), options, append(args, "--format=json")...), 2, "CONFIRMATION_REQUIRED")
	assertFailure(t, execute(context.Background(), options, append(args, "--confirm=no", "--format=json")...), 2, "INVALID_USAGE")
	options.IsTerminal = func(io.Reader) bool { return true }
	options.In = strings.NewReader("no\n")
	result = execute(context.Background(), options, args...)
	if result.code != 1 || !strings.Contains(result.stderr, "deletion cancelled") {
		t.Fatalf("declined: %#v", result)
	}
	options.In = strings.NewReader("yes\n")
	result = execute(context.Background(), options, append(args, "--non-interactive")...)
	if result.code != 2 || !strings.Contains(result.stderr, "--confirm=yes") {
		t.Fatalf("noninteractive: %#v", result)
	}
	result = execute(context.Background(), options, args...)
	if result.code != 0 || !strings.Contains(result.stderr, "[y/N]") || !strings.Contains(result.stdout, "Deleted profile work") {
		t.Fatalf("accepted: %#v", result)
	}
	assertFailure(t, execute(context.Background(), options, "profiles", "activate", "work", "--format=json"), 2, "NO_PROFILES")
}

func TestProfileDeleteChecksTargetBeforeConfirmation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir})
	options.IsTerminal = func(io.Reader) bool { return true }
	input := strings.NewReader("yes\n")
	options.In = input

	result := execute(t.Context(), options, "profiles", "delete", "default")
	if result.code != 2 || !strings.Contains(result.stderr, "datalens setup") || !strings.Contains(result.stderr, "datalens profiles create") {
		t.Fatalf("empty installation: %#v", result)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty installation created a directory: %v", err)
	}
	if result := execute(t.Context(), options, "profiles", "create", "default"); result.code != 0 {
		t.Fatalf("create default: %#v", result)
	}
	result = execute(t.Context(), options, "profiles", "delete", "missing", "--format=json")
	assertFailure(t, result, 2, "PROFILE_NOT_FOUND")
	if input.Len() != len("yes\n") {
		t.Fatal("delete read confirmation for a missing profile")
	}
	result = execute(t.Context(), options, "profiles", "delete", "default")
	if result.code != 0 || !strings.Contains(result.stderr, `Delete profile "default"? [y/N]`) ||
		!strings.Contains(result.stdout, "Deleted profile default") || !strings.Contains(result.stdout, "datalens setup") {
		t.Fatalf("delete last profile: %#v", result)
	}
}

func TestProfileDeletePromptTimesOut(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	if result := execute(context.Background(), options, "profiles", "create", "work"); result.code != 0 {
		t.Fatalf("create: %#v", result)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	options.In = reader
	options.IsTerminal = func(io.Reader) bool { return true }
	result := execute(context.Background(), options, "profiles", "delete", "work", "--timeout=20ms")
	if result.code != 1 || !strings.Contains(result.stderr, "command timed out") {
		t.Fatalf("timed out: %#v", result)
	}
	if result := execute(context.Background(), options, "profiles", "activate", "work"); result.code != 0 {
		t.Fatalf("profile was deleted: %#v", result)
	}
}

func TestUsageFailuresAreJSONOnStderr(t *testing.T) {
	for _, args := range [][]string{
		{"missing", "--format", "json"},
		{"profiles", "missing", "--format", "json"},
		{"version", "extra", "--format", "json"},
		{"profiles", "create", "work", "--api-version=4", "--format=json"},
		{"profiles", "create", "--name=work", "--format=json"},
		{"profiles", "create", "first", "second", "--format=json"},
		{"version", "--unknown", "--format", "json"},
		{"profiles", "create", "--format", "json"},
		{"profiles", "list", "extra", "--format", "json"},
		{"version", "--timeout", "0s", "--format", "json"},
		{"version", "--timeout", "-1s", "--format", "json"},
		{"version", "--timeout", "invalid", "--format", "json"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			assertFailure(t, execute(context.Background(), testOptions(nil), args...), 2, "INVALID_USAGE")
		})
	}
	invalidFormat := execute(context.Background(), testOptions(nil), "version", "--format", "xml")
	if invalidFormat.code != 2 || invalidFormat.stdout != "" || !strings.HasPrefix(invalidFormat.stderr, "Error:") {
		t.Fatalf("invalid format: %#v", invalidFormat)
	}
}

func TestEarlyUsageOutputOptions(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		json      bool
		operation string
	}{
		{"malformed shorthand", []string{"version", "-hX", "--format=json", "--debug"}, true, "datalens version"},
		{"malformed long flag", []string{"rpc", "---bad", "--debug", "--format", "json"}, true, "datalens rpc"},
		{"unknown command", []string{"private-value", "--format=json", "--debug"}, true, "datalens"},
		{"bad argument", []string{"profiles", "list", "private-value", "--debug", "--format=json"}, true, "datalens profiles list"},
		{"invalid timeout", []string{"version", "--timeout=bad", "--debug", "--format=json"}, true, "datalens version"},
		{"invalid profile", []string{"version", "--profile=", "--debug", "--format=json"}, true, "datalens version"},
		{"token value", []string{"version", "--auth-token", "--format=json", "--bad=private-value"}, false, ""},
		{"profile value", []string{"version", "--profile", "--debug", "--bad", "--format=json"}, true, ""},
		{"local flag value", []string{"rpc", "--args", "--format=json", "-hX"}, false, ""},
		{"delimiter", []string{"version", "--", "--debug", "--format=json"}, false, ""},
		{"explicit false", []string{"version", "--debug", "--bad", "--debug=false", "--format=json"}, true, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := execute(context.Background(), testOptions(nil), test.args...)
			if result.code != 2 || result.stdout != "" || strings.Contains(result.stderr, "private-value") {
				t.Fatalf("usage failure: %#v", result)
			}
			if !test.json {
				if !strings.HasPrefix(result.stderr, "Error:") || strings.Contains(result.stderr, "Diagnostics:") {
					t.Fatalf("expected text error: %q", result.stderr)
				}
				return
			}
			assertFailure(t, result, 2, "INVALID_USAGE")
			diagnostics := document(t, result.stderr)["diagnostics"]
			if test.operation == "" {
				if diagnostics != nil {
					t.Fatalf("unexpected diagnostics: %#v", diagnostics)
				}
				return
			}
			want := map[string]any{"version": "0.1.2", "operation": test.operation, "nonInteractive": true}
			if !reflect.DeepEqual(diagnostics, want) {
				t.Fatalf("diagnostics = %#v, want %#v", diagnostics, want)
			}
		})
	}
}

func TestUnknownHelpTopic(t *testing.T) {
	for _, topic := range [][]string{{"private-value"}, {"profiles", "private-value"}, {"version", "private-value"}} {
		for _, jsonOutput := range []bool{false, true} {
			args := append([]string{"help"}, topic...)
			if jsonOutput {
				args = append(args, "--format=json")
			}
			result := execute(context.Background(), testOptions(nil), args...)
			if result.code != 2 || result.stdout != "" || strings.Contains(result.stderr, "private-value") || !strings.Contains(result.stderr, "unknown help topic") {
				t.Fatalf("%v: %#v", args, result)
			}
			if jsonOutput {
				assertFailure(t, result, 2, "INVALID_USAGE")
			}
		}
	}
}

func TestHelpDebugDiagnostics(t *testing.T) {
	for _, args := range [][]string{{"help", "profiles"}, {"version", "--help"}, {"profiles"}} {
		result := execute(context.Background(), testOptions(nil), append(args, "--debug", "--format=json")...)
		if result.code != 0 || !strings.Contains(result.stdout, "Usage:") || !strings.HasPrefix(result.stderr, "Diagnostics: ") {
			t.Fatalf("%v: %#v", args, result)
		}
	}
}

func TestHelpFlagRejectsUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"private-value", "--help"},
		{"profiles", "private-value", "--help"},
		{"tenant", "settings", "private-value", "-h"},
		{"help", "private-value", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			result := execute(context.Background(), testOptions(nil), append(args, "--format=json", "--debug")...)
			assertFailure(t, result, 2, "INVALID_USAGE")
			if strings.Contains(result.stderr, "private-value") {
				t.Fatalf("error echoed the argument: %s", result.stderr)
			}
		})
	}
}

func TestHelpFlagResolvesCommandAndAllowsLeafArguments(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	createCloudProfile(t, options)
	for _, test := range []struct {
		args []string
		path string
	}{
		{[]string{"--help", "tenant", "settings", "show"}, "tenant settings show"},
		{[]string{"profiles", "-h", "activate", "team"}, "profiles activate"},
		{[]string{"rpc", "getTenantDetails", "--help"}, "rpc"},
		{[]string{"rpc", "-h"}, "rpc"},
	} {
		result := execute(context.Background(), options, test.args...)
		if result.code != 0 || result.stderr != "" || !strings.Contains(result.stdout, "Usage:\n  datalens "+test.path) {
			t.Errorf("%v: %#v", test.args, result)
		}
	}
}

func TestCancellationExitCodes(t *testing.T) {
	for _, code := range []int{130, 143} {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(&output.Error{Code: "INTERRUPTED", Message: "command interrupted", ExitCode: code})
		assertFailure(t, execute(ctx, testOptions(nil), "version", "--format", "json"), code, "INTERRUPTED")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assertFailure(t, execute(ctx, testOptions(nil), "version", "--format", "json"), 130, "INTERRUPTED")
}

func TestUnimplementedCommandsAreNotExposed(t *testing.T) {
	for _, name := range []string{"auth", "update", "workbook", "lakehouse"} {
		assertFailure(t, execute(context.Background(), testOptions(nil), name, "--format", "json"), 2, "INVALID_USAGE")
	}
}

func TestOutputFailure(t *testing.T) {
	var stderr bytes.Buffer
	options := testOptions(nil)
	options.Out = brokenWriter{}
	options.Err = &stderr
	if code := cli.Execute(context.Background(), []string{"version", "--format", "json"}, options); code != 1 {
		t.Fatalf("output failure exit = %d", code)
	}
	parsed := document(t, stderr.String())
	if parsed["error"].(map[string]any)["code"] != "COMMAND_FAILED" || strings.Contains(stderr.String(), "sensitive") {
		t.Fatalf("unsafe failure: %s", &stderr)
	}
}

func testOptions(env map[string]string) cli.Options {
	return cli.Options{
		In: strings.NewReader(""), LookupEnv: func(name string) string { return env[name] },
		UserConfigDir: func() (string, error) { return "", errors.New("unavailable") },
		IsTerminal:    func(io.Reader) bool { return false },
		Build:         buildinfo.Info{APIVersion: buildinfo.APIVersion, Version: "0.1.2", Revision: "abcdef", OS: "linux", Arch: "amd64", GoVersion: "go-test"},
	}
}

type execution struct {
	code           int
	stdout, stderr string
}

func execute(ctx context.Context, options cli.Options, args ...string) execution {
	var stdout, stderr bytes.Buffer
	options.Out, options.Err = &stdout, &stderr
	code := cli.Execute(ctx, args, options)
	return execution{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func assertFailure(t *testing.T, result execution, exitCode int, code string) {
	t.Helper()
	if result.code != exitCode || result.stdout != "" {
		t.Fatalf("failure = %#v, want exit %d and empty stdout", result, exitCode)
	}
	parsed := document(t, result.stderr)
	failure, ok := parsed["error"].(map[string]any)
	if !ok || failure["code"] != code || failure["message"] == "" {
		t.Fatalf("error = %#v, want code %s and message", parsed, code)
	}
}

func document(t *testing.T, data string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(data))
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("invalid JSON %q: %v", data, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra output after JSON document: %#v, %v", extra, err)
	}
	return result
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("sensitive writer failure")
}
