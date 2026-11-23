package api

import (
	"io"
	"os"
	"strings"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

func ResolveToken(explicit *string, lookupEnv func(string) string) (string, error) {
	if explicit != nil {
		return validateToken(*explicit)
	}
	if token := lookupEnv("DATALENS_TOKEN"); token != "" {
		return validateToken(token)
	}
	path := lookupEnv("DATALENS_TOKEN_PATH")
	if path == "" {
		return "", &output.Error{Code: "AUTH_TOKEN_REQUIRED", Message: "Provide a token with --auth-token, DATALENS_TOKEN, or DATALENS_TOKEN_PATH.", ExitCode: 3}
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
	const maxTokenFileSize = 64 << 10
	data, err := io.ReadAll(io.LimitReader(file, maxTokenFileSize+1))
	if err != nil || len(data) > maxTokenFileSize {
		return "", unreadable
	}
	token := strings.TrimSuffix(string(data), "\n")
	return validateToken(strings.TrimSuffix(token, "\r"))
}

func validateToken(token string) (string, error) {
	if token == "" || strings.ContainsFunc(token, func(r rune) bool { return r < '!' || r > '~' }) {
		return "", &output.Error{Code: "AUTH_TOKEN_INVALID", Message: "Supply a nonempty token without whitespace or control characters.", ExitCode: 3}
	}
	return token, nil
}
