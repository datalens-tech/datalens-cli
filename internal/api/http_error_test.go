package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/datalens-tech/datalens-cli/internal/config"
	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestHTTPErrorDiagnostics(t *testing.T) {
	const credential = "sensitive-token"
	for _, tc := range []struct {
		name, body, code, message, requestID string
	}{
		{"DataLens error", `{"status":500,"code":"ERR.NOT_FOUND","message":"Workbook\n not found","requestId":"body-id","details":{"token":"hidden-detail"}}`, "ERR.NOT_FOUND", "Workbook not found", "body-id"},
		{"missing ID", `{"code":"BAD_REQUEST","message":"Invalid workbook"}`, "BAD_REQUEST", "Invalid workbook", "header-id"},
		{"malformed", "<html>hidden-detail</html>", "", "", "header-id"},
		{"too large", `{"message":"` + strings.Repeat("a", 64<<10) + `"}`, "", "", "header-id"},
		{"reflected credentials", `{"code":"ERR.sensitive-token","message":"Bearer sensitive-token","requestId":"id-sensitive-token"}`, "", "", "header-id"},
		{"unsafe controls", `{"message":"bad\u001b[31m","code":"bad\u202ecode","requestId":"id\nline"}`, "", "", "header-id"},
		{"long message", `{"message":"` + strings.Repeat("я", 520) + `"}`, "", strings.Repeat("я", 512) + "…", "header-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := exchangeTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{"X-Request-Id": {"header-id"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			profile := config.Profile{Installation: "yateam"}
			_, err := Call(t.Context(), tr, profile, "getWorkbook", json.RawMessage("{}"), new(credential), nil, nil)
			var failure *output.Error
			if !errors.As(err, &failure) || failure.Code != "API_ERROR" || failure.ExitCode != 1 || failure.HTTPStatus != 400 ||
				failure.APICode != tc.code || failure.APIMessage != tc.message || failure.RequestID != tc.requestID {
				t.Fatalf("unexpected failure: %#v", failure)
			}
			for _, format := range []string{"text", "json"} {
				var stderr bytes.Buffer
				writer := output.Writer{Err: &stderr, Format: format}
				if err := writer.Failure(failure); err != nil {
					t.Fatal(err)
				}
				message := stderr.String()
				for _, want := range []string{tc.code, tc.message, tc.requestID} {
					if !strings.Contains(message, want) {
						t.Errorf("%s: missing expected diagnostic", format)
					}
				}
				for _, exclude := range []string{credential, "hidden-detail"} {
					if strings.Contains(message, exclude) {
						t.Errorf("%s: leaked excluded diagnostic", format)
					}
				}
				if format == "json" && !strings.Contains(message, `"httpStatus":400`) {
					t.Fatalf("missing numeric HTTP status: %s", message)
				}
			}
		})
	}
}

func TestStalledHTTPErrorBodyKeepsStatus(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	profile := config.Profile{Installation: "on-premises", Endpoint: srv.URL, APIEndpoint: srv.URL}
	_, err := Call(ctx, srv.Client().Transport, profile, "test", json.RawMessage("{}"), new("token"), nil, nil)
	requireCode(t, err, "AUTH_REJECTED", 3)
}
