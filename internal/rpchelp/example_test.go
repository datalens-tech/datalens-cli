package rpchelp

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestMinimalExampleArguments(t *testing.T) {
	c := exampleCatalog()
	for _, tc := range []struct {
		action string
		fields []string
	}{
		{"getDashboard", []string{"dashboardId"}},
		{"updateHtmlPage", []string{"content", "entryId"}},
		{"createDashboard", []string{"entry"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			raw, err := c.Schema(tc.action, "on-premises")
			if err != nil {
				t.Fatal(err)
			}
			schema, err := compileSchema(raw)
			if err != nil {
				t.Fatal(err)
			}
			example := minimalExample(schema)
			var fields map[string]any
			if err := json.Unmarshal(example, &fields); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(slices.Sorted(maps.Keys(fields)), tc.fields) {
				t.Fatalf("expected only %v in %s", tc.fields, example)
			}
			if tc.action == "getDashboard" && fields["dashboardId"] != "your-dashboard-id" {
				t.Fatalf("missing ID placeholder in %s", example)
			}
			if tc.action == "createDashboard" {
				entry := fields["entry"].(map[string]any)
				if !slices.Equal(slices.Sorted(maps.Keys(entry)), []string{"data", "meta"}) {
					t.Fatalf("expected only data and meta in %s", example)
				}
			}
		})
	}
}

func TestCatalogMinimalExamplesValidate(t *testing.T) {
	c := exampleCatalog()
	for action, op := range c.Actions {
		if len(op.Schema) == 0 {
			continue
		}
		t.Run(action, func(t *testing.T) {
			raw, err := c.argumentSchema(op)
			if err != nil {
				t.Fatal(err)
			}
			schema, err := compileSchema(raw)
			if err != nil {
				t.Fatal(err)
			}
			example := minimalExample(schema)
			var fields map[string]any
			if err := json.Unmarshal(example, &fields); err != nil || fields == nil {
				t.Fatalf("expected a JSON object, got %q", example)
			}
			if err := schema.Validate(fields); err != nil {
				t.Fatalf("invalid example %s: %v", example, err)
			}
		})
	}
}

func TestHostedCatalogVariantExample(t *testing.T) {
	c := Catalog{Actions: map[string]operation{
		"createSparkApplication": {Schema: json.RawMessage(`{
			"type": "object",
			"anyOf": [
				{"properties":{"clusterId":{"type":"string"},"file":{"type":"string"}},"required":["clusterId","file"]},
				{"properties":{"clusterId":{"type":"string"},"image":{"type":"string"}},"required":["image","clusterId"]}
			]
		}`)},
	}}
	var help strings.Builder
	if err := c.Write(&help, "createSparkApplication", "cloud", "work"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(help.String(), "Required: clusterId\nAdditional argument requirements depend on the variant.") {
		t.Fatal(help.String())
	}
	schema, err := c.compileOperation(c.Actions["createSparkApplication"])
	if err != nil {
		t.Fatal(err)
	}
	example := minimalExample(schema)
	var fields map[string]any
	if err := json.Unmarshal(example, &fields); err != nil || len(fields) != 2 || fields["clusterId"] != "your-cluster-id" {
		t.Fatalf("incomplete Spark argument example: %s", example)
	}
	if err := c.ValidateArgs("createSparkApplication", "work", example); err != nil {
		t.Fatal(err)
	}
}
