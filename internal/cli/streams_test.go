package cli_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

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

type resultPipe struct {
	delay time.Duration
	err   error
}

func (p resultPipe) Write(b []byte) (int, error) {
	return p.WriteContext(context.Background(), b)
}

func (p resultPipe) WriteContext(ctx context.Context, b []byte) (int, error) {
	select {
	case <-ctx.Done():
		return 0, context.Cause(ctx)
	case <-time.After(p.delay):
		if p.err != nil {
			return 0, p.err
		}
		return len(b), nil
	}
}

func TestResultPipeAfterCommandFinishes(t *testing.T) {
	for _, pipe := range []resultPipe{{delay: 75 * time.Millisecond}, {err: syscall.EPIPE}} {
		var stderr bytes.Buffer
		options := testOptions(nil)
		options.Out, options.Err = pipe, &stderr
		if code := cli.Execute(t.Context(), []string{"version", "--timeout=25ms"}, options); code != 0 || stderr.Len() != 0 {
			t.Fatalf("exit %d, stderr %q", code, &stderr)
		}
	}
}
