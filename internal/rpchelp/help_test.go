package rpchelp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestEditionSpecificDescriptions(t *testing.T) {
	for _, tc := range []struct{ installation, action, want, absent string }{
		{"cloud", "listSharedEntryAccessBindings", "specified common object", "specified shared entry"},
		{"on-premises", "listSharedEntryAccessBindings", "specified shared entry", "specified common object"},
		{"stackland", "getTenantDetails", "No bundled documentation", "current tenant state"},
		{"yateam", "getEditorChartSecrets", "Yandex Team API reference", "No bundled documentation"},
		{"cloud", "getEditorChartSecrets", "No bundled documentation", "Required:"},
	} {
		t.Run(tc.installation, func(t *testing.T) {
			var text strings.Builder
			if err := Write(&text, tc.action, tc.installation, ""); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text.String(), tc.want) || strings.Contains(text.String(), tc.absent) {
				t.Fatalf("unexpected help: %s", text.String())
			}
		})
	}
}

func TestExamplePreservesJSONInShell(t *testing.T) {
	op := operation{Example: json.RawMessage(`{"id": 9007199254740993, "name": "O'Brien"}`)}
	text, err := (catalog{}).format("test", "Cloud", "", op)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `datalens rpc test '{"id":9007199254740993,"name":"O'\''Brien"}'`) {
		t.Fatalf("example: %s", text)
	}
}

func TestSchemaIncludesTransitiveDefinitionsAfterRoot(t *testing.T) {
	op := operation{
		Schema:      json.RawMessage(`{"properties":{"item":{"$ref":"#/$defs/Item"}}}`),
		Definitions: []string{"Item", "Child"},
	}
	c := catalog{Schemas: map[string]json.RawMessage{
		"Item":  json.RawMessage(`{"items":{"$ref":"#/$defs/Child"}}`),
		"Child": json.RawMessage(`{"items":{"$ref":"#/$defs/Item"}}`),
	}}
	schema, err := c.argumentSchema(op)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(schema), &doc); err != nil || len(doc.Defs) != 2 ||
		doc.Defs["Child"] == nil || doc.Defs["Item"] == nil ||
		bytes.Index(schema, []byte(`"properties"`)) > bytes.Index(schema, []byte(`"$defs"`)) {
		t.Fatalf("incomplete or out-of-order schema: %s (%v)", schema, err)
	}
}

func TestCompactArgumentHelp(t *testing.T) {
	for _, tc := range []struct{ action, required, hint string }{
		{"getWorkbook", "workbookId", "Optional: includePermissionsInfo"},
		{"createWizardChart", "data", "Optional: annotation"},
		{"updateHtmlPage", "entryId", "Additional argument requirements depend on the variant."},
		{"createSparkApplication", "clusterId", "Additional argument requirements depend on the variant."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			var text strings.Builder
			if err := Write(&text, tc.action, "cloud", "work"); err != nil {
				t.Fatal(err)
			}
			for _, line := range []string{
				"datalens rpc " + tc.action + " JSON --profile=work",
				"Required: " + tc.required + "\n", tc.hint,
				"datalens schema show " + tc.action + " --profile=work",
			} {
				if !strings.Contains(text.String(), line) {
					t.Fatalf("missing %q in %s", line, text.String())
				}
			}
			if strings.Contains(text.String(), "$defs") {
				t.Fatal("full schema leaked into compact help")
			}
			if tc.action == "getWorkbook" && !strings.Contains(text.String(), `datalens rpc getWorkbook '{"workbookId":"your-workbook-id"}' --profile=work`) {
				t.Fatal("example lost explicit profile")
			}
		})
	}
}

func TestSchemaErrorsDistinguishMissingArgumentsAndAction(t *testing.T) {
	for action, code := range map[string]string{
		"getTenantDetails":      "RPC_SCHEMA_UNAVAILABLE",
		"getEditorChartSecrets": "RPC_ACTION_UNDOCUMENTED",
	} {
		data, err := Schema(action, "cloud")
		var failure *output.Error
		if len(data) != 0 || !errors.As(err, &failure) || failure.Code != code {
			t.Errorf("%s returned %s, %v; want %s", action, data, err, code)
		}
	}
}
