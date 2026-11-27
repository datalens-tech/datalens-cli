package rpchelp

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestOpenAPIReferencesAndExamples(t *testing.T) {
	const doc = `{
		"openapi":"3.1.0","info":{"version":"3"},
		"paths":{
			"/rpc/save":{"post":{"summary":"Save an item","requestBody":{"$ref":"#/components/requestBodies/Body"}}},
			"/rpc/ping":{"post":{"description":"Ping"}},
			"/health":{"post":{}}
		},
		"components":{
			"requestBodies":{"Body":{"content":{"application/json":{
				"schema":{"$ref":"#/components/schemas/Arg"},
				"examples":{"saved":{"$ref":"#/components/examples/Saved"}}
			}}}},
			"examples":{"Saved":{"value":{"id":9007199254740993}}},
			"schemas":{
				"Arg":{"properties":{"next":{"$ref":"#/components/schemas/Node~1Child"},"metadata":{"const":{"$ref":"literal"}}}},
				"Node/Child":{"properties":{"child":{"$ref":"#/components/schemas/Leaf"}}},
				"Leaf":{"properties":{"child":{"$ref":"#/components/schemas/Node~1Child"}}},
				"Unused":{"type":"number"}
			}
		}
	}`
	c, err := ParseOpenAPI([]byte(doc), "3")
	if err != nil {
		t.Fatal(err)
	}
	op := c.Actions["save"]
	if len(c.Actions) != 2 || len(c.Actions["ping"].Schema) != 0 || op.Description != "Save an item" ||
		string(op.Example) != `{"id":9007199254740993}` || !slices.Equal(op.Definitions, []string{"Leaf", "Node/Child"}) {
		t.Fatalf("unexpected catalog: %+v", c)
	}
	if len(c.Schemas) != 2 || !bytes.Contains(op.Schema, []byte(`"const":{"$ref":"literal"}`)) {
		t.Fatalf("unexpected schema or definitions: %s, %v", op.Schema, op.Definitions)
	}
}

func TestOpenAPIDiscriminatorWrapper(t *testing.T) {
	const doc = `{"openapi":"3.1.0","info":{"version":"3"},
		"paths":{"/rpc/create":{"post":{"requestBody":{"content":{"application/json":{"schema":{
			"properties":{},"additionalProperties":false,
			"discriminator":{"propertyName":"type","mapping":{"db":"#/components/schemas/Db"}},
			"oneOf":[{"$ref":"#/components/schemas/Db"}]
		}}}}}}},
		"components":{"schemas":{"Db":{"properties":{"type":{"const":"db"}},"required":["type"]}}}
	}`
	c, err := ParseOpenAPI([]byte(doc), "3")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := c.compileOperation(c.Actions["create"])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value map[string]any
		valid bool
	}{{map[string]any{"type": "db"}, true}, {map[string]any{"type": "db", "extra": true}, false}} {
		if err := schema.Validate(tc.value); (err == nil) != tc.valid {
			t.Errorf("validation of %v: %v", tc.value, err)
		}
	}
}

func TestInvalidOpenAPI(t *testing.T) {
	const doc = `{"openapi":"3.1.0","info":{"version":"3"},"paths":{"/rpc/test":{"post":{
		"requestBody":{"content":{"application/json":{"schema":{"type":"string"}}}}
	}}}}`
	for _, text := range []string{
		doc + "{}",
		strings.Replace(doc, `"3"`, `"4"`, 1),
		strings.Replace(doc, "/rpc/test", "/health", 1),
		strings.Replace(doc, `"type":"string"`, `"type":"unknown"`, 1),
		strings.Replace(doc, `"type":"string"`, `"$ref":"#/components/schemas/Missing"`, 1),
		strings.Replace(doc, `"type":"string"`, `"$ref":"https://example.com/schema"`, 1),
	} {
		if _, err := ParseOpenAPI([]byte(text), "3"); err == nil {
			t.Errorf("invalid document accepted: %s", text)
		}
	}
}

func TestOpenAPICloudPromptPattern(t *testing.T) {
	const raw = `{"type":"string","pattern":"[^\\s\\u001c-\\u001f\\u0085]|\\ufeff"}`
	schema, err := compileSchema(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", " \t", "\u0085", "\u001f", "\u00a0", "\u2029"} {
		if err := schema.Validate(value); err == nil {
			t.Errorf("blank prompt %q accepted", value)
		}
	}
	for _, value := range []string{"\ufeff", " заполни "} {
		if err := schema.Validate(value); err != nil {
			t.Errorf("prompt %q rejected: %v", value, err)
		}
	}
}
