package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
)

const maxTokenSize = 64 << 10

func ResolveToken(ctx context.Context, explicit *string, profile config.Profile, lookupEnv func(string) string) (string, error) {
	if explicit != nil {
		return validateToken(*explicit)
	}
	if len(profile.TokenCommand) > 0 {
		return runTokenCommand(ctx, profile.TokenCommand)
	}
	tokenSources := "DATALENS_TOKEN or DATALENS_TOKEN_PATH"
	if profile.Installation == "yateam" {
		if token := lookupEnv("DATALENS_OAUTH_TOKEN"); token != "" {
			return validateToken(token)
		}
		tokenSources = "DATALENS_OAUTH_TOKEN, DATALENS_TOKEN, or DATALENS_TOKEN_PATH"
	}
	if token := lookupEnv("DATALENS_TOKEN"); token != "" {
		return validateToken(token)
	}
	path := lookupEnv("DATALENS_TOKEN_PATH")
	if path == "" {
		return "", &output.Error{Code: "AUTH_TOKEN_REQUIRED", Message: "Pass --auth-token, configure a profile token command, or set " + tokenSources + ".", ExitCode: 3}
	}
	unreadable := &output.Error{Code: "AUTH_TOKEN_FILE_INVALID", Message: "DATALENS_TOKEN_PATH must point to a readable, regular token file.", ExitCode: 2}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", unreadable
	}
	file, err := os.Open(path)
	if err != nil {
		return "", unreadable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTokenSize+1))
	if err != nil || len(data) > maxTokenSize {
		return "", unreadable
	}
	token := strings.TrimSuffix(string(data), "\n")
	return validateToken(strings.TrimSuffix(token, "\r"))
}

func runTokenCommand(ctx context.Context, argv []string) (string, error) {
	var stdout tokenCommandOutput
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = &stdout
	// A helper can exit while its descendants still hold stdout open.
	cmd.WaitDelay = 100 * time.Millisecond
	err := cmd.Run()
	if cause := context.Cause(ctx); cause != nil {
		return "", cause
	}
	if stdout.oversized {
		return "", &output.Error{Code: "AUTH_TOKEN_INVALID", Message: "The profile token command output exceeds 64 KiB.", ExitCode: 3}
	}
	if err != nil {
		message := "Could not get a token from the profile command. Check that it runs successfully."
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			message = fmt.Sprintf("The profile token command failed (exit code %d).", exitError.ExitCode())
		}
		return "", &output.Error{Code: "AUTH_TOKEN_COMMAND_FAILED", Message: message, ExitCode: 3}
	}
	token := stdout.data.String()
	if line, ok := strings.CutSuffix(token, "\n"); ok {
		token = strings.TrimSuffix(line, "\r")
	}
	return validateToken(token)
}

type tokenCommandOutput struct {
	data      strings.Builder
	oversized bool
}

func (w *tokenCommandOutput) Write(p []byte) (int, error) {
	if len(p) > maxTokenSize-w.data.Len() {
		w.oversized = true
		return 0, io.ErrShortBuffer
	}
	return w.data.Write(p)
}

func validateToken(token string) (string, error) {
	if token == "" || strings.ContainsFunc(token, func(r rune) bool { return r < '!' || r > '~' }) {
		return "", &output.Error{Code: "AUTH_TOKEN_INVALID", Message: "Supply a nonempty token without whitespace or control characters.", ExitCode: 3}
	}
	return token, nil
}
