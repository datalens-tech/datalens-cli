package rpchelp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestValidateArgs(t *testing.T) {
	c := exampleCatalog()
	const connection = `{"type":"json_api","name":"test","host":"example.com","port":443,"dir_path":"Connection","allowed_methods":["PATCH"]}`
	for _, tc := range []struct {
		name, action, args string
		valid              bool
	}{
		{"required argument", "getDashboard", `{}`, false},
		{"referenced enum", "getDashboard", `{"dashboardId":"id","branch":"published"}`, true},
		{"invalid enum", "getDashboard", `{"dashboardId":"id","branch":"private-user-value"}`, false},
		{"nested array item", "getEntriesPermissions", `{"entryIds":["id",false]}`, false},
		{"disallowed property", "getEntriesPermissions", `{"entryIds":[],"unexpected":"private-user-value"}`, false},
		{"missing anyOf branch", "updateHtmlPage", `{"entryId":"id"}`, false},
		{"valid anyOf branch", "updateHtmlPage", `{"entryId":"id","revId":"version","mode":"publish"}`, true},
		{"stackland connector methods", "createConnection", connection, true},
		{"invalid connector method", "createConnection", strings.Replace(connection, "PATCH", "TRACE", 1), false},
		{"number above maximum", "setLicenseLimit", `{"value":10000.0000000000000001}`, false},
		{"unknown action", "privateAction", `{"private":"value"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.ValidateArgs(tc.action, "", json.RawMessage(tc.args))
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

func TestValidateActionWithoutSchema(t *testing.T) {
	c := Catalog{Actions: map[string]operation{"getTenantDetails": {}}}
	if err := c.ValidateArgs("getTenantDetails", "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
}
