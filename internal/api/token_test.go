package api

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/config"
)

func TestProfileTokenCommand(t *testing.T) {
	lookup := func(string) string {
		t.Fatal("consulted the environment despite an explicit token or profile command")
		return ""
	}
	resolve := func(ctx context.Context, explicit *string, command []string) (string, error) {
		return ResolveToken(ctx, nil, explicit, config.Profile{Installation: "yateam", TokenCommand: command}, lookup)
	}
	missing := []string{filepath.Join(t.TempDir(), "private-argument")}
	explicit := "from-flag"
	got, err := resolve(t.Context(), &explicit, missing)
	if got != explicit || err != nil {
		t.Fatal("explicit token did not take precedence")
	}
	explicit = ""
	_, err = resolve(t.Context(), &explicit, missing)
	requireCode(t, err, "AUTH_TOKEN_INVALID", 3)

	for _, tc := range []struct{ name, value, code string }{
		{"plain", "a$b|c", ""},
		{"newline", "a$b|c\n", ""},
		{"CRLF", "a$b|c\r\n", ""},
		{"empty", "", "AUTH_TOKEN_INVALID"},
		{"multiline", "private-output\nextra\n", "AUTH_TOKEN_INVALID"},
		{"trailing space", "private-output ", "AUTH_TOKEN_INVALID"},
		{"bare carriage return", "private-output\r", "AUTH_TOKEN_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(t.Context(), nil, tokenHelper("echo", tc.value))
			if tc.code == "" {
				if err != nil || got != "a$b|c" {
					t.Fatal("did not read the token from stdout")
				}
				return
			}
			requireCode(t, err, tc.code, 3)
			if got != "" || strings.Contains(err.Error(), "private-") {
				t.Fatal("returned invalid token data")
			}
		})
	}

	for _, cmd := range [][]string{missing, tokenHelper("fail", "private-argument")} {
		got, err := resolve(t.Context(), nil, cmd)
		requireCode(t, err, "AUTH_TOKEN_COMMAND_FAILED", 3)
		if got != "" || strings.Contains(err.Error(), "private-") {
			t.Fatal("returned helper diagnostics, output, or arguments")
		}
	}
	_, err = resolve(t.Context(), nil, tokenHelper("oversized"))
	requireCode(t, err, "AUTH_TOKEN_INVALID", 3)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = resolve(ctx, nil, tokenHelper("sleep"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a timeout, got %v", err)
	}
}

func TestTokenCommandInheritedStdout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := ResolveToken(ctx, nil, nil, config.Profile{TokenCommand: tokenHelper("spawn")}, func(string) string { return "" })
	requireCode(t, err, "AUTH_TOKEN_COMMAND_FAILED", 3)
}

func tokenHelper(args ...string) []string {
	return append([]string{os.Args[0], "-test.run=^TestTokenCommandHelper$", "--"}, args...)
}

func TestTokenCommandHelper(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 {
		return
	}
	switch args[0] {
	case "echo":
		io.WriteString(os.Stdout, args[1])
	case "fail":
		io.WriteString(os.Stdout, "private-output")
		io.WriteString(os.Stderr, "private-diagnostics")
		os.Exit(7)
	case "oversized":
		io.WriteString(os.Stdout, strings.Repeat("x", maxTokenSize+1))
	case "sleep":
		time.Sleep(10 * time.Second)
	case "spawn":
		argv := tokenHelper("hold-pipe")
		child := exec.Command(argv[0], argv[1:]...)
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
	case "hold-pipe":
		for {
			if _, err := io.WriteString(os.Stdout, "."); err != nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	default:
		t.Fatal("unknown token test helper mode")
	}
	os.Exit(0)
}
