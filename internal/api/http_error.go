package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

var errorIdentifier = regexp.MustCompile(`^[a-zA-Z0-9._:/-]{1,128}$`)

func safeErrorID(value, credential string) string {
	if !errorIdentifier.MatchString(value) || strings.Contains(value, credential) {
		return ""
	}
	return value
}

func safeErrorMessage(value, credential string) string {
	value = strings.Join(strings.Fields(value), " ")
	if strings.Contains(value, credential) || strings.ContainsFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return ""
	}
	if text := []rune(value); len(text) > 512 {
		return string(text[:512]) + "…"
	}
	return value
}

func readHTTPError(ctx context.Context, resp *http.Response, cancelRequest context.CancelFunc, credential string, failure *output.Error) error {
	failure.HTTPStatus = resp.StatusCode
	failure.RequestID = safeErrorID(resp.Header.Get("X-Request-Id"), credential)
	const limit = 64 << 10
	if resp.ContentLength > limit {
		return failure
	}
	// Once the status is known, a stalled diagnostic body should not hold up the command.
	timer := time.AfterFunc(time.Second, cancelRequest)
	defer timer.Stop()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	var body struct {
		Code, Message, RequestID string
	}
	if err == nil && len(data) <= limit && utf8.Valid(data) && json.Unmarshal(data, &body) == nil {
		failure.APICode = safeErrorID(body.Code, credential)
		failure.APIMessage = safeErrorMessage(body.Message, credential)
		if id := safeErrorID(body.RequestID, credential); id != "" {
			failure.RequestID = id
		}
	}
	return failure
}
