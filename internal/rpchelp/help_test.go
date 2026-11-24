package rpchelp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditionSpecificDescriptions(t *testing.T) {
	for _, tc := range []struct{ installation, action, want, absent string }{
		{"cloud", "listSharedEntryAccessBindings", "specified common object", "specified shared entry"},
		{"onpremise-standalone", "listSharedEntryAccessBindings", "specified shared entry", "specified common object"},
		{"stackland", "getTenantDetails", "No bundled documentation", "current tenant state"},
		{"yateam", "getWorkbook", "No bundled documentation for Yandex Team", "Returns the specified workbook"},
	} {
		t.Run(tc.installation, func(t *testing.T) {
			var text strings.Builder
			if err := Write(&text, tc.action, tc.installation); err != nil {
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
	text, err := (catalog{}).format("test", op)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `datalens rpc test --args '{"id":9007199254740993,"name":"O'\''Brien"}'`) {
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
		strings.Index(schema, `"properties"`) > strings.Index(schema, `"$defs"`) {
		t.Fatalf("incomplete or out-of-order schema: %s (%v)", schema, err)
	}
}
