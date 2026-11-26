package rpchelp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestValidateArgs(t *testing.T) {
	const connection = `{"type":"json_api","name":"test","host":"example.com","port":443,"dir_path":"Connection","allowed_methods":["PATCH"]}`
	for _, tc := range []struct {
		name, action, installation, args string
		valid                            bool
	}{
		{"required argument", "getDashboard", "cloud", `{}`, false},
		{"referenced enum", "getDashboard", "cloud", `{"dashboardId":"id","branch":"published"}`, true},
		{"invalid enum", "getDashboard", "cloud", `{"dashboardId":"id","branch":"private-user-value"}`, false},
		{"nested array item", "getEntriesPermissions", "yateam", `{"entryIds":["id",false]}`, false},
		{"disallowed property", "getEntriesPermissions", "yateam", `{"entryIds":[],"unexpected":"private-user-value"}`, false},
		{"missing anyOf branch", "updateHtmlPage", "yateam", `{"entryId":"id"}`, false},
		{"valid anyOf branch", "updateHtmlPage", "yateam", `{"entryId":"id","revId":"version","mode":"publish"}`, true},
		{"cloud connector", "createConnection", "cloud", connection, true},
		{"enterprise connector methods", "createConnection", "stackland", connection, true},
		{"invalid connector method", "createConnection", "stackland", strings.Replace(connection, "PATCH", "TRACE", 1), false},
		{"number above maximum", "setLicenseLimit", "cloud", `{"value":10000.0000000000000001}`, false},
		{"unknown action", "privateAction", "cloud", `{"private":"value"}`, true},
		{"action without schema", "getTenantDetails", "cloud", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArgs(tc.action, tc.installation, "", json.RawMessage(tc.args))
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *output.Error
			if !errors.As(err, &failure) || failure.ExitCode != 2 || failure.Code != "INVALID_USAGE" {
				t.Fatalf("expected a usage error, got %v", err)
			}
			if strings.Contains(failure.Message, "private-user-value") {
				t.Fatal("error echoed the argument value")
			}
		})
	}
}
