package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/cli"
	"github.com/datalens-tech/datalens-cli/internal/config"
)

const setupConfirmation = "Create and activate this profile? [Y/n] "

func setupOptions(t *testing.T, input string) (cli.Options, config.Store) {
	t.Helper()
	store := config.Store{Dir: filepath.Join(t.TempDir(), "config")}
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": store.Dir, "DATALENS_TOKEN": "test-token-should-stay-private"})
	options.IsTerminal = func(io.Reader) bool { return true }
	options.In = strings.NewReader(input)
	options.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("setup made a network request")
		return nil, errors.New("unexpected request")
	})
	return options, store
}

func TestSetupCreatesActiveProfile(t *testing.T) {
	for _, tc := range []struct {
		name, answers, profile, installation, apiEndpoint, org, authentication string
		command                                                                []string
	}{
		{
			name: "Cloud defaults", answers: "\n\norg-id\n\n\n", profile: "cloud", installation: "cloud",
			apiEndpoint: "https://api.datalens.tech", org: "org-id", authentication: "yc iam create-token", command: []string{"yc", "iam", "create-token"},
		},
		{
			name: "Cloud environment", answers: "1\n2\n\nwork\nyes\n", profile: "work", installation: "cloud",
			apiEndpoint: "https://api.datalens.tech", authentication: "DATALENS_TOKEN or DATALENS_TOKEN_PATH",
		},
		{
			name: "Cloud command", answers: "1\n3\nmissing-helper token\norg-id\nwork\nyes\n", profile: "work", installation: "cloud",
			apiEndpoint: "https://api.datalens.tech", org: "org-id", authentication: "missing-helper token", command: []string{"missing-helper", "token"},
		},
		{
			name: "Team environment", answers: "2\n\ninternal\nyes\n", profile: "internal", installation: "yateam",
			apiEndpoint: "https://api.datalens.yandex.net", authentication: "DATALENS_OAUTH_TOKEN or DATALENS_TOKEN_PATH",
		},
		{
			name: "On-premises command", answers: "3\n2\nmissing-helper \"an argument\"\nhttps://example.org/dl\ncorp\nyes\n",
			profile: "corp", installation: "on-premises", apiEndpoint: "https://example.org/dl/api",
			authentication: `missing-helper "an argument"`, command: []string{"missing-helper", "an argument"},
		},
		{
			name: "Stackland with retries", answers: "9\n4\n9\n1\nhttp://example.org\nhttps://stack.example.org\nstack-org\nbad/name\nstack\nyes\n",
			profile: "stack", installation: "stackland", apiEndpoint: "https://stack.example.org/api", org: "stack-org", authentication: "DATALENS_TOKEN or DATALENS_TOKEN_PATH",
		},
		{
			name: "Stackland organization from environment", answers: "4\n1\nhttps://stack.example.org/dl\n\n\nyes\n",
			profile: "stackland", installation: "stackland", apiEndpoint: "https://stack.example.org/dl/api", authentication: "DATALENS_TOKEN or DATALENS_TOKEN_PATH",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, store := setupOptions(t, tc.answers)
			result := execute(t.Context(), options, "setup")
			transcript := result.stdout + result.stderr
			if result.code != 0 || strings.Contains(transcript, "test-token-should-stay-private") {
				t.Fatalf("setup: %#v", result)
			}
			if offered := strings.Contains(result.stderr, "Service account (RSA private key file)"); offered != (tc.installation == "on-premises") {
				t.Fatalf("unexpected service-account option for %s: %t", tc.installation, offered)
			}
			doc, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			profile := doc.Profiles[tc.profile]
			if doc.ActiveName() != tc.profile || profile.Installation != tc.installation ||
				profile.APIEndpoint != tc.apiEndpoint || profile.OrganizationID != tc.org || !slices.Equal(profile.TokenCommand, tc.command) {
				t.Fatalf("unexpected profile after setup: %#v", doc)
			}
			preview, _, ok := strings.Cut(result.stderr, setupConfirmation)
			if !ok {
				t.Fatal("missing confirmation prompt")
			}
			fields := []string{
				"Profile name: " + tc.profile,
				"Installation type: " + tc.installation,
				"UI endpoint: " + profile.Endpoint,
				"API endpoint: " + tc.apiEndpoint,
				"Authentication:",
			}
			if tc.installation == "cloud" || tc.installation == "stackland" {
				org := tc.org
				if org == "" {
					org = "from DATALENS_ORG_ID"
				}
				fields = append(fields, "Organization ID: "+org)
			}
			for _, field := range fields {
				if !strings.Contains(preview, "\n"+field) {
					t.Errorf("missing %q before confirmation: %s", field, preview)
				}
			}
			_, auth, _ := strings.Cut(preview, "\nAuthentication:")
			if !strings.Contains(auth, tc.authentication) {
				t.Errorf("expected authentication %q, got %q", tc.authentication, auth)
			}
		})
	}
}

func TestSetupServiceAccount(t *testing.T) {
	t.Chdir(t.TempDir())
	const keyFile = "rsa key.pem"
	const contents = "private file contents"
	if err := os.WriteFile(keyFile, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	keyPath, err := filepath.Abs(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, confirm := range []string{"n", "y"} {
		options, store := setupOptions(t, "3\n3\n\nsvc\nkid\n"+keyFile+"\nhttps://bi.example.org\n\n"+confirm+"\n")
		result := execute(t.Context(), options, "setup")
		if result.code != 0 || strings.Contains(result.stdout+result.stderr, contents) {
			t.Fatalf("setup: %#v", result)
		}
		preview, _, ok := strings.Cut(result.stderr, setupConfirmation)
		if !ok || !strings.Contains(preview, "Service account ID: svc\nKey ID: kid\nPrivate key file: "+keyPath+"\n") {
			t.Fatalf("service account missing from confirmation: %s", preview)
		}
		doc, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if confirm == "n" {
			if len(doc.Profiles) != 0 {
				t.Fatal("cancelled setup saved a profile")
			}
			continue
		}
		got := doc.Profiles["on-premises"].ServiceAccount
		want := config.ServiceAccount{ID: "svc", KeyID: "kid", PrivateKeyFile: keyPath}
		if got == nil || *got != want {
			t.Fatalf("service account = %#v, want %#v", got, want)
		}
	}
}

func TestSetupLeavesExistingProfileUntouched(t *testing.T) {
	options, store := setupOptions(t, "2\n1\nwork\n")
	if result := execute(t.Context(), options, "profiles", "create", "work", "--installation-type=cloud", "--organization=original"); result.code != 0 {
		t.Fatalf("create: %#v", result)
	}
	path := filepath.Join(store.Dir, "config.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	result := execute(t.Context(), options, "setup")
	if result.code == 0 || strings.Contains(result.stderr, setupConfirmation) {
		t.Fatalf("setup accepted an existing profile name: %#v", result)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("setup changed existing configuration: %v", err)
	}
}

func TestSetupDeclinePreservesActiveProfile(t *testing.T) {
	for _, answer := range []string{"n", "no", "maybe\nn"} {
		t.Run("answer="+answer, func(t *testing.T) {
			options, store := setupOptions(t, "2\n1\nnew-profile\n"+answer+"\n")
			if err := store.CreateAndUse(t.Context(), "original", config.Profile{Installation: "cloud"}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Dir, "config.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			result := execute(t.Context(), options, "setup")
			if result.code != 0 || !strings.Contains(result.stderr, setupConfirmation) {
				t.Fatalf("decline: %#v", result)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("declining setup changed the active profile or configuration: %v", err)
			}
		})
	}
}

func TestSetupRetriesInvalidConfirmation(t *testing.T) {
	options, store := setupOptions(t, "2\n1\nwork\nmaybe\ny\n")
	result := execute(t.Context(), options, "setup")
	doc, err := store.Load()
	if result.code != 0 || err != nil || doc.ActiveName() != "work" {
		t.Fatalf("confirmation retry: %#v, profile=%q, err=%v", result, doc.ActiveName(), err)
	}
}

func TestSetupRequiresInteractiveInput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		isTerminal bool
		args       []string
	}{
		{name: "piped stdin"},
		{name: "explicit noninteractive", isTerminal: true, args: []string{"--non-interactive"}},
		{name: "JSON", isTerminal: true, args: []string{"--format=json"}},
		{name: "profile selector", isTerminal: true, args: []string{"--profile=work"}},
		{name: "per-request token", isTerminal: true, args: []string{"--auth-token=placeholder"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options, store := setupOptions(t, "")
			input := strings.NewReader("2\n1\nwork\n")
			options.In = input
			options.IsTerminal = func(io.Reader) bool { return tc.isTerminal }
			result := execute(t.Context(), options, append([]string{"setup"}, tc.args...)...)
			if result.code != 2 || int64(input.Len()) != input.Size() || result.stdout != "" {
				t.Fatalf("setup should reject input before prompting: %#v", result)
			}
			if tc.name == "JSON" {
				document(t, result.stderr)
			}
			if _, err := os.Stat(store.Dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("noninteractive setup created config dir: %v", err)
			}
		})
	}
}

func TestSetupStopsBeforeFinalAnswer(t *testing.T) {
	for _, waiting := range []bool{false, true} {
		options, store := setupOptions(t, "2\n1\nwork\n")
		args := []string{"setup"}
		if waiting {
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			options.In = io.MultiReader(options.In, reader)
			args = append(args, "--timeout=50ms")
		}
		result := execute(context.Background(), options, args...)
		if result.code == 0 || !strings.Contains(result.stderr, setupConfirmation) || (waiting && !strings.Contains(result.stderr, "command timed out")) {
			t.Fatalf("waiting=%t: %#v", waiting, result)
		}
		if _, err := os.Stat(store.Dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("incomplete setup created configuration: %v", err)
		}
	}
}
