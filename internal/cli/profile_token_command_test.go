package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/config"
)

func TestProfileTokenCommandRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir})
	command := `"C:\Program Files\yc.exe" iam create-token --profile='work cloud' "" '$HOME' | \\host\share`
	result := execute(t.Context(), options, "profiles", "create", "work", "--token-cmd="+command)
	if result.code != 0 {
		t.Fatalf("create: %#v", result)
	}
	doc, err := (config.Store{Dir: dir}).Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`C:\Program Files\yc.exe`, "iam", "create-token", "--profile=work cloud", "", "$HOME", "|", `\\host\share`}
	if got := doc.Profiles["work"].TokenCommand; !slices.Equal(got, want) {
		t.Fatalf("saved command = %#v, want %#v", got, want)
	}
}

func TestInvalidTokenCommandLeavesConfigUntouched(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	options := testOptions(map[string]string{"DATALENS_CONFIG_DIR": dir})
	for _, command := range []string{"", " \t ", `'' --test`, `yc "private-value`, "yc private-value\nextra"} {
		result := execute(t.Context(), options, "profiles", "create", "work", "--token-cmd="+command, "--debug", "--format=json")
		if result.code != 2 || strings.Contains(result.stdout+result.stderr, "private-value") {
			t.Fatalf("invalid token command: %#v", result)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration directory after failure: %v", err)
	}
}
