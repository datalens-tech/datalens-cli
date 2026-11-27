package cli_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type readConfirmation func([]byte) (int, error)

func (f readConfirmation) Read(p []byte) (int, error) { return f(p) }

func TestProfileDeleteTimeout(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flag         string
		shouldDelete bool
	}{
		{"wait for confirmation", "", true},
		{"explicit timeout includes confirmation", "--timeout=1s", false},
		{"preconfirmed delete has default timeout", "--confirm=yes", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(base, "datalens")})
			if result := execute(t.Context(), options, "profiles", "create", "work"); result.code != 0 {
				t.Fatal(result)
			}
			synctest.Test(t, func(t *testing.T) {
				options.IsTerminal = func(io.Reader) bool { return true }
				stop := make(chan struct{})
				defer close(stop)
				options.In = readConfirmation(func(p []byte) (int, error) {
					select {
					case <-stop:
						return 0, io.EOF
					case <-time.After(time.Minute):
						return copy(p, "yes\n"), io.EOF
					}
				})
				if tc.flag == "--confirm=yes" {
					options.LookupEnv = func(string) string { return "" }
					options.UserConfigDir = func() (string, error) {
						time.Sleep(time.Minute)
						return base, nil
					}
				}
				args := []string{"profiles", "delete", "work"}
				if tc.flag != "" {
					args = append(args, tc.flag)
				}
				result := execute(t.Context(), options, args...)
				if tc.shouldDelete {
					if result.code != 0 || !strings.Contains(result.stdout, "Deleted profile work") {
						t.Fatal(result)
					}
				} else if result.code != 1 || !strings.Contains(result.stderr, "command timed out") {
					t.Fatal(result)
				}
			})
		})
	}
}

func TestProfileDeleteKeepsReplacementDuringConfirmation(t *testing.T) {
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": filepath.Join(t.TempDir(), "config")})
	if result := execute(t.Context(), options, "profiles", "create", "work"); result.code != 0 {
		t.Fatal(result)
	}
	options.IsTerminal = func(io.Reader) bool { return true }
	options.In = readConfirmation(func(p []byte) (int, error) {
		if result := execute(t.Context(), options, "profiles", "delete", "work", "--confirm=yes"); result.code != 0 {
			t.Error(result)
		}
		if result := execute(t.Context(), options, "profiles", "create", "work", "--organization=new-org"); result.code != 0 {
			t.Error(result)
		}
		return copy(p, "yes\n"), io.EOF
	})
	result := execute(t.Context(), options, "profiles", "delete", "work")
	if result.code == 0 || !strings.Contains(result.stderr, "changed") {
		t.Fatalf("deletion after replacement: %#v", result)
	}
	result = execute(t.Context(), options, "profiles", "list", "--format=json")
	if result.code != 0 || !strings.Contains(result.stdout, `"organizationId":"new-org"`) {
		t.Fatalf("replacement profile: %#v", result)
	}
}

func TestListAndDeleteInvalidProfiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir})
	data := `{"version":2,"currentProfile":"bad","profiles":{"bad":{"new":"secret"},"odd\nname":null,"work":{"installationType":"cloud","tokenCommand":["tool","--key=secret"]}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	listed := execute(t.Context(), options, "profiles", "list", "--format=json")
	if listed.code != 0 || strings.Contains(listed.stdout+listed.stderr, "secret") {
		t.Fatalf("list: %#v", listed)
	}
	result := document(t, listed.stdout)
	profiles := result["profiles"].([]any)
	if result["selectedProfile"] != "bad" || len(profiles) != 3 || profiles[0].(map[string]any)["problem"] == nil ||
		profiles[2].(map[string]any)["hasTokenCommand"] != true || profiles[2].(map[string]any)["tokenCommand"] != nil {
		t.Fatalf("unexpected summaries: %v", result)
	}
	text := execute(t.Context(), options, "profiles", "list")
	if !strings.Contains(text.stdout, "* bad\tinvalid:") || strings.Contains(text.stdout, "odd\nname") {
		t.Fatalf("text list: %q", text.stdout)
	}
	for _, name := range []string{"bad", "odd\nname"} {
		deleted := execute(t.Context(), options, "profiles", "delete", name, "--confirm=yes")
		if deleted.code != 0 || strings.Contains(deleted.stdout, "odd\nname") {
			t.Fatalf("delete: %#v", deleted)
		}
	}
}
