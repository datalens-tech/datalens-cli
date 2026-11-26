package rpchelp

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

func TestMinimalExampleArguments(t *testing.T) {
	for _, tc := range []struct {
		action string
		fields []string
	}{
		{"getDashboard", []string{"dashboardId"}},
		{"getSparkApplication", []string{"applicationId", "clusterId"}},
		{"updateHtmlPage", []string{"content", "entryId"}},
		{"createDashboard", []string{"entry"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			raw, err := Schema(tc.action, "cloud")
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

func TestBundledMinimalExamplesValidate(t *testing.T) {
	for _, installation := range []string{"cloud", "on-premises", "yateam"} {
		_, c, err := reference(installation)
		if err != nil {
			t.Fatal(err)
		}
		for action, op := range c.Actions {
			if len(op.Schema) == 0 {
				continue
			}
			t.Run(installation+"/"+action, func(t *testing.T) {
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
}
