package rpchelp

import (
	"bytes"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

func TestEditionSpecificDescriptions(t *testing.T) {
	local := &Catalog{Actions: map[string]operation{
		"listSharedEntryAccessBindings": {Description: "Bindings for the specified shared entry."},
	}}
	hosted := &Catalog{Actions: map[string]operation{
		"listSharedEntryAccessBindings": {Description: "Bindings for the specified common object."},
		"getEditorChartSecrets":         {Description: "Secrets for the selected chart."},
	}}
	for _, tc := range []struct {
		catalog                            *Catalog
		installation, action, want, absent string
	}{
		{hosted, "cloud", "listSharedEntryAccessBindings", "specified common object", "specified shared entry"},
		{local, "on-premises", "listSharedEntryAccessBindings", "specified shared entry", "specified common object"},
		{local, "stackland", "getTenantDetails", "No documentation", "current tenant state"},
		{hosted, "yateam", "getEditorChartSecrets", "Yandex Team API reference", "No documentation"},
		{local, "on-premises", "getEditorChartSecrets", "No documentation", "Secrets for the selected chart"},
	} {
		t.Run(tc.installation, func(t *testing.T) {
			var text strings.Builder
			if err := tc.catalog.Write(&text, tc.action, tc.installation, ""); err != nil {
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
	text, err := (&Catalog{}).format("test", "Cloud", "", op)
	if err != nil {
		t.Fatal(err)
	}
	want := `datalens rpc test '{"id":9007199254740993,"name":"O'\''Brien"}'`
	if !strings.Contains(text, want) {
		t.Fatalf("missing POSIX example: %s", text)
	}
	if runtime.GOOS == "windows" {
		want = `datalens rpc test --% "{\"id\":9007199254740993,\"name\":\"O'Brien\"}"`
	}
	if !strings.Contains(text, want) {
		t.Fatalf("example: %s", text)
	}
}

func TestSchemaIncludesTransitiveDefinitionsAfterRoot(t *testing.T) {
	op := operation{
		Schema:      json.RawMessage(`{"properties":{"item":{"$ref":"#/$defs/Item"}}}`),
		Definitions: []string{"Item", "Child"},
	}
	c := Catalog{Schemas: map[string]json.RawMessage{
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
	c := exampleCatalog()
	for _, tc := range []struct{ action, required, hint string }{
		{"getWorkbook", "workbookId", "Optional: includePermissionsInfo"},
		{"createWizardChart", "data", "Optional: annotation"},
		{"updateHtmlPage", "entryId", "Additional argument requirements depend on the variant."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			var text strings.Builder
			if err := c.Write(&text, tc.action, "on-premises", "work"); err != nil {
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
			if tc.action == "getWorkbook" && !strings.Contains(text.String(), ExampleCommands("getWorkbook", `{"workbookId":"your-workbook-id"}`, " --profile=work")) {
				t.Fatal("example lost explicit profile")
			}
		})
	}
}

func TestSchemaErrorsDistinguishMissingArgumentsAndAction(t *testing.T) {
	c := Catalog{Actions: map[string]operation{"getTenantDetails": {}}}
	for action, code := range map[string]string{
		"getTenantDetails":      "RPC_SCHEMA_UNAVAILABLE",
		"getEditorChartSecrets": "RPC_ACTION_UNDOCUMENTED",
	} {
		data, err := c.Schema(action, "cloud")
		var failure *output.Error
		if len(data) != 0 || !errors.As(err, &failure) || failure.Code != code {
			t.Errorf("%s returned %s, %v; want %s", action, data, err, code)
		}
	}
}

func TestCachedCatalogPreservesMissingSchemaAndExample(t *testing.T) {
	data, err := json.Marshal(Catalog{Actions: map[string]operation{"getTenantDetails": {}}})
	if err != nil {
		t.Fatal(err)
	}
	var restored Catalog
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if op := restored.Actions["getTenantDetails"]; len(op.Schema) != 0 || len(op.Example) != 0 {
		t.Fatalf("absent schema or example became a JSON value after restoring the catalog: %s", data)
	}
}
