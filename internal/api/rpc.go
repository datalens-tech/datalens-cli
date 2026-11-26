package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/datalens-tech/datalens-cli/internal/buildinfo"
	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
)

var actionName = regexp.MustCompile("^[A-Za-z][A-Za-z0-9_]*$")

func ValidateAction(action string) error {
	if !actionName.MatchString(action) {
		return &output.Error{Code: "INVALID_USAGE", Message: "Use an RPC action name beginning with a letter and containing only letters, digits, or underscores.", ExitCode: 2}
	}
	return nil
}

func ValidateRPC(action string, args json.RawMessage) error {
	if err := ValidateAction(action); err != nil {
		return err
	}
	body := bytes.TrimSpace(args)
	if len(body) == 0 || body[0] != '{' || !utf8.Valid(args) || !json.Valid(args) {
		return &output.Error{Code: "INVALID_USAGE", Message: "RPC arguments must be a UTF-8 JSON object. In bash or zsh, wrap the object in single quotes to preserve its double quotes.", ExitCode: 2}
	}
	return nil
}

func Call(ctx context.Context, transport http.RoundTripper, profile config.Profile, action string, args json.RawMessage, explicitToken *string, lookupEnv func(string) string) (json.RawMessage, error) {
	if err := ValidateRPC(action, args); err != nil {
		return nil, err
	}
	profile, err := config.NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	target := strings.TrimRight(profile.APIEndpoint, "/") + "/rpc/" + action
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, target, bytes.NewReader(args))
	if err != nil {
		return nil, &output.Error{Code: "PROFILE_INVALID", Message: "Cannot build a request with the configured API endpoint.", ExitCode: 2}
	}
	if profile.RequiresOrganization() {
		if profile.OrganizationID == "" {
			return nil, &output.Error{Code: "ORGANIZATION_REQUIRED", Message: "Set DATALENS_ORG_ID or specify --organization=ID when creating the profile.", ExitCode: 2}
		}
		req.Header.Set("x-dl-org-id", profile.OrganizationID)
	}
	token, err := ResolveToken(ctx, transport, explicitToken, profile, lookupEnv)
	if err != nil {
		return nil, err
	}
	scheme := "Bearer"
	if profile.Installation == "yateam" {
		scheme = "OAuth"
	}
	req.Header.Set("Authorization", scheme+" "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-dl-api-version", buildinfo.APIVersion)
	req.Header.Set("User-Agent", "datalens-cli/"+buildinfo.Version)
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, &output.Error{Code: "API_UNAVAILABLE", Message: "Cannot reach the DataLens API. Check the API endpoint and network connection.", ExitCode: 1}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		failure := &output.Error{Code: "API_ERROR", Message: fmt.Sprintf("The DataLens API returned HTTP %d.", resp.StatusCode), ExitCode: 1}
		if resp.StatusCode == http.StatusUnauthorized {
			failure = &output.Error{Code: "AUTH_REJECTED", Message: "The DataLens API rejected the token (HTTP 401).", ExitCode: 3}
		}
		return nil, readHTTPError(ctx, resp, cancelRequest, token, failure)
	}
	const maxResponseSize = 64 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		return nil, &output.Error{Code: "API_READ_FAILED", Message: "Cannot read the DataLens API response.", ExitCode: 1}
	}
	if len(data) > maxResponseSize {
		return nil, &output.Error{Code: "API_RESPONSE_TOO_LARGE", Message: "The DataLens API response exceeds 64 MiB.", ExitCode: 1}
	}
	if len(data) == 0 {
		return json.RawMessage("null"), nil
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return nil, &output.Error{Code: "API_INVALID_RESPONSE", Message: "The DataLens API response must be valid UTF-8 JSON.", ExitCode: 1}
	}
	return data, nil
}
