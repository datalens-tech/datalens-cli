package cli_test

import (
	"io"
	"strings"
	"testing"
)

func TestUpdateNoticeEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		pipedIn  bool
		pipedOut bool
		version  string
		want     bool
		code     int
	}{
		{name: "terminal", args: []string{"version"}, want: true},
		{name: "debug", args: []string{"version", "--debug"}, want: true},
		{name: "beta", args: []string{"version"}, version: "0.2.0-beta.1", want: true},
		{name: "development", args: []string{"version"}, version: "dev"},
		{name: "redirected input", args: []string{"version"}, pipedIn: true},
		{name: "redirected output", args: []string{"version"}, pipedOut: true},
		{name: "noninteractive", args: []string{"version", "--non-interactive"}},
		{name: "json", args: []string{"version", "--format=json"}},
		{name: "opt out", args: []string{"version"}, env: map[string]string{"DATALENS_NO_UPDATE_CHECK": "1"}},
		{name: "CI", args: []string{"version"}, env: map[string]string{"CI": "true"}},
		{name: "CI disabled", args: []string{"version"}, env: map[string]string{"CI": "false"}, want: true},
		{name: "root help", args: []string{}},
		{name: "help command", args: []string{"help", "profiles"}},
		{name: "group help", args: []string{"profiles"}},
		{name: "rpc help", args: []string{"rpc"}},
		{name: "rpc action help", args: []string{"rpc", "getWorkbook", "--help"}},
		{name: "completion", args: []string{"__complete", "ver"}},
		{name: "error", args: []string{"version", "unexpected"}, code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := testOptions(tc.env)
			options.UserConfigDir = func() (string, error) { return t.TempDir(), nil }
			if tc.version != "" {
				options.Build.Version = tc.version
			}
			options.IsTerminal = func(io.Reader) bool { return !tc.pipedIn }
			options.OutputsAreTerminals = !tc.pipedOut
			calls := 0
			const notice = "A new DataLens CLI release is available.\n"
			options.NotifyUpdate = func(current string, out io.Writer) {
				calls++
				if current != options.Build.Version {
					t.Errorf("notification version = %q, want %q", current, options.Build.Version)
				}
				_, _ = io.WriteString(out, notice)
			}
			result := execute(t.Context(), options, tc.args...)
			if result.code != tc.code || calls > 1 || (calls == 1) != tc.want ||
				strings.HasSuffix(result.stderr, notice) != tc.want || strings.Contains(result.stdout, notice) {
				t.Errorf("calls=%d, exit=%d, stdout=%q, stderr=%q", calls, result.code, result.stdout, result.stderr)
			}
		})
	}
}
