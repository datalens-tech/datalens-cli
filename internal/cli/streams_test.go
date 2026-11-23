package cli_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/cli"
)

func TestHelpReportsOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help", "profiles"}, {"profiles"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			for _, writer := range []io.Writer{brokenWriter{}, shortWriter{}} {
				var stderr bytes.Buffer
				options := testOptions(nil)
				options.Out, options.Err = writer, &stderr
				code := cli.Execute(context.Background(), args, options)
				if code != 1 || !strings.Contains(stderr.String(), "command failed") {
					t.Errorf("%T: exit %d, stderr %q", writer, code, &stderr)
				}
			}
		})
	}
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }
