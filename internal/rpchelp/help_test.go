package rpchelp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEditionSpecificDescriptions(t *testing.T) {
	var text strings.Builder
	if err := Write(&text, "listSharedEntryAccessBindings"); err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{
		"Cloud API reference", "Enterprise API reference",
		"specified common object", "specified shared entry",
	} {
		if !strings.Contains(text.String(), phrase) {
			t.Errorf("missing %q in help", phrase)
		}
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
