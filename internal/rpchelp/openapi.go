package rpchelp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type apiExample struct {
	Ref   string `json:"$ref"`
	Value json.RawMessage
}

type apiMedia struct {
	Schema   json.RawMessage
	Example  json.RawMessage
	Examples map[string]apiExample
}

type apiRequestBody struct {
	Ref     string `json:"$ref"`
	Content map[string]apiMedia
}

type apiOperation struct {
	Description string
	Summary     string
	RequestBody apiRequestBody
}

type openAPIDocument struct {
	OpenAPI    string
	Info       struct{ Version string }
	Paths      map[string]struct{ Post *apiOperation }
	Components struct {
		Schemas       map[string]json.RawMessage
		RequestBodies map[string]apiRequestBody
		Examples      map[string]apiExample
	}
}

func ParseOpenAPI(data []byte, version string) (*Catalog, error) {
	var doc openAPIDocument
	if !utf8.Valid(data) || json.Unmarshal(data, &doc) != nil {
		return nil, errors.New("expected a UTF-8 OpenAPI JSON document")
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.1.") || doc.Info.Version != version {
		return nil, fmt.Errorf("expected OpenAPI 3.1 for DataLens API v%s", version)
	}
	c := &Catalog{Actions: make(map[string]operation), Schemas: make(map[string]json.RawMessage)}
	deps := make(map[string][]string)
	for path, route := range doc.Paths {
		action, rpc := strings.CutPrefix(path, "/rpc/")
		if !rpc || route.Post == nil || action == "" || strings.Contains(action, "/") {
			continue
		}
		op, err := doc.operation(route.Post, c.Schemas, deps)
		if err != nil {
			return nil, fmt.Errorf("invalid OpenAPI operation %s: %w", action, err)
		}
		if len(op.Schema) > 0 {
			if _, err := c.compileOperation(op); err != nil {
				return nil, fmt.Errorf("invalid schema for %s: %w", action, err)
			}
		}
		c.Actions[action] = op
	}
	if len(c.Actions) == 0 {
		return nil, errors.New("OpenAPI document has no DataLens RPC actions")
	}
	return c, nil
}

var unescapePointerToken = strings.NewReplacer("~1", "/", "~0", "~")

func localComponent[T any](items map[string]T, section, ref string) (T, error) {
	name, ok := strings.CutPrefix(ref, "#/components/"+section+"/")
	name = unescapePointerToken.Replace(name)
	item, found := items[name]
	if !ok || !found {
		var zero T
		return zero, fmt.Errorf("unresolved %s reference %q", section, ref)
	}
	return item, nil
}

func (doc *openAPIDocument) operation(post *apiOperation, schemas map[string]json.RawMessage, deps map[string][]string) (operation, error) {
	op := operation{Description: post.Description}
	if op.Description == "" {
		op.Description = post.Summary
	}
	body := post.RequestBody
	if body.Ref != "" {
		var err error
		body, err = localComponent(doc.Components.RequestBodies, "requestBodies", body.Ref)
		if err != nil {
			return op, err
		}
	}
	media := body.Content["application/json"]
	schema := media.Schema
	var root map[string]json.RawMessage
	if len(schema) > 0 && schema[0] == '{' {
		if err := json.Unmarshal(schema, &root); err != nil {
			return op, err
		}
		if len(root) == 1 && root["$ref"] != nil {
			var ref string
			if err := json.Unmarshal(root["$ref"], &ref); err != nil {
				return op, err
			}
			var err error
			schema, err = localComponent(doc.Components.Schemas, "schemas", ref)
			if err != nil {
				return op, err
			}
			root = nil
			if schema[0] == '{' {
				if err := json.Unmarshal(schema, &root); err != nil {
					return op, err
				}
			}
		}
	}
	example, err := doc.example(media, root)
	if err != nil {
		return op, err
	}
	op.Example = example
	if len(schema) == 0 {
		return op, nil
	}

	var pending []string
	op.Schema, pending, err = rewriteOpenAPISchema(schema)
	if err != nil {
		return op, err
	}
	seen := map[string]bool{}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if schemas[name] == nil {
			source, ok := doc.Components.Schemas[name]
			if !ok {
				return op, fmt.Errorf("missing schema component %q", name)
			}
			schemas[name], deps[name], err = rewriteOpenAPISchema(source)
			if err != nil {
				return op, err
			}
		}
		pending = append(pending, deps[name]...)
	}
	op.Definitions = slices.Sorted(maps.Keys(seen))
	return op, nil
}

func (doc *openAPIDocument) example(media apiMedia, root map[string]json.RawMessage) (json.RawMessage, error) {
	candidates := []json.RawMessage{media.Example}
	for _, name := range slices.Sorted(maps.Keys(media.Examples)) {
		example := media.Examples[name]
		if example.Ref != "" {
			var err error
			example, err = localComponent(doc.Components.Examples, "examples", example.Ref)
			if err != nil {
				return nil, err
			}
		}
		candidates = append(candidates, example.Value)
	}
	candidates = append(candidates, root["example"])
	if raw := root["examples"]; raw != nil {
		var examples []json.RawMessage
		if err := json.Unmarshal(raw, &examples); err != nil {
			return nil, err
		}
		candidates = append(candidates, examples...)
	}
	for _, candidate := range candidates {
		if len(candidate) > 0 && candidate[0] == '{' {
			return candidate, nil
		}
	}
	return nil, nil
}

func rewriteOpenAPISchema(raw json.RawMessage) (json.RawMessage, []string, error) {
	var schema any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&schema); err != nil {
		return nil, nil, err
	}

	var refs []string
	rewriteRef := func(value any) (string, error) {
		ref, _ := value.(string)
		key, ok := strings.CutPrefix(ref, "#/components/schemas/")
		if !ok || key == "" || strings.Contains(key, "/") {
			return "", fmt.Errorf("unsupported schema reference %q", ref)
		}
		refs = append(refs, unescapePointerToken.Replace(key))
		return "#/$defs/" + key, nil
	}
	var walk func(any) error
	walk = func(node any) error {
		switch node := node.(type) {
		case []any:
			for _, child := range node {
				if err := walk(child); err != nil {
					return err
				}
			}
		case map[string]any:
			props, object := node["properties"].(map[string]any)
			if node["additionalProperties"] == false && object && len(props) == 0 && node["discriminator"] != nil && node["oneOf"] != nil {
				// DataLens connection wrappers declare their properties inside the selected variant.
				delete(node, "additionalProperties")
				node["unevaluatedProperties"] = false
			}
			for key, val := range node {
				switch key {
				case "$ref":
					ref, err := rewriteRef(val)
					if err != nil {
						return err
					}
					node[key] = ref
				case "discriminator":
					discriminator, _ := val.(map[string]any)
					mapping, _ := discriminator["mapping"].(map[string]any)
					for variant, value := range mapping {
						ref, err := rewriteRef(value)
						if err != nil {
							return err
						}
						mapping[variant] = ref
					}
				case "properties", "patternProperties", "$defs", "dependentSchemas":
					children, _ := val.(map[string]any)
					for _, child := range children {
						if err := walk(child); err != nil {
							return err
						}
					}
				case "default", "enum", "const", "examples", "example":
				default:
					if err := walk(val); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := walk(schema); err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(schema)
	return data, refs, err
}

func compileOpenAPIPattern(pattern string) (jsonschema.Regexp, error) {
	// Cloud uses ECMAScript whitespace and Unicode escapes to require a nonblank subscription prompt.
	if pattern == `[^\s\u001c-\u001f\u0085]|\ufeff` {
		return regexp.Compile(`[^\p{Z}\x09-\x0D\x1C-\x1F\x85]`)
	}
	return regexp.Compile(pattern)
}
