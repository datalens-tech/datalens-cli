package rpchelp

import (
	"encoding/json"
	"maps"
	"math/big"
	"slices"
	"strings"
	"unicode"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func minimalExample(schema *jsonschema.Schema) json.RawMessage {
	value, ok := exampleValue(schema, "", 0)
	if !ok {
		return nil
	}
	data, _ := json.Marshal(value)
	return data
}

func exampleValue(s *jsonschema.Schema, name string, depth int) (any, bool) {
	if depth > 32 || s == nil {
		return nil, false
	}
	if s.Ref != nil {
		value, ok := exampleValue(s.Ref, name, depth+1)
		return value, ok && s.Validate(value) == nil
	}
	if s.Const != nil {
		return *s.Const, s.Validate(*s.Const) == nil
	}
	if s.Enum != nil {
		for _, value := range s.Enum.Values {
			if s.Validate(value) == nil {
				return value, true
			}
		}
		return nil, false
	}
	if s.Default != nil {
		switch value := (*s.Default).(type) {
		case bool, string, json.Number:
			if s.Validate(value) == nil {
				return value, true
			}
		}
	}

	object := make(map[string]any, len(s.Required))
	for _, key := range s.Required {
		value, ok := exampleValue(s.Properties[key], key, depth+1)
		if !ok {
			return nil, false
		}
		object[key] = value
	}
	for _, part := range s.AllOf {
		value, ok := exampleValue(part, name, depth+1)
		fields, obj := value.(map[string]any)
		if !ok || !obj {
			return nil, false
		}
		maps.Copy(object, fields)
	}
	variants := s.AnyOf
	if len(s.OneOf) > 0 {
		variants = s.OneOf
	}
	if len(variants) > 0 {
		var best any
		bestFields, bestSize := 0, 0
		for _, variant := range variants {
			value, ok := exampleValue(variant, name, depth+1)
			if !ok {
				continue
			}
			if fields, obj := value.(map[string]any); obj && len(object) > 0 {
				merged := maps.Clone(object)
				maps.Copy(merged, fields)
				value = merged
			}
			data, _ := json.Marshal(value)
			fields := exampleFieldCount(value)
			if (bestSize == 0 || fields < bestFields || fields == bestFields && len(data) < bestSize) && s.Validate(value) == nil {
				best, bestFields, bestSize = value, fields, len(data)
			}
		}
		return best, bestSize != 0
	}

	if s.Types == nil || slices.Contains(s.Types.ToStrings(), "object") {
		if s.Validate(object) == nil {
			return object, true
		}
	}
	if s.Types == nil {
		return nil, false
	}
	for _, typ := range s.Types.ToStrings() {
		var candidates []any
		switch typ {
		case "string":
			label := "your-" + strings.ReplaceAll(splitCamelCase(name), "_", "-")
			if s.Format != nil {
				switch s.Format.Name {
				case "date":
					label = "2025-01-01"
				case "date-time":
					label = "2025-01-01T00:00:00Z"
				}
			}
			if s.MinLength != nil && len(label) < *s.MinLength && *s.MinLength <= 256 {
				label += strings.Repeat("x", *s.MinLength-len(label))
			}
			if s.MaxLength != nil && len(label) > *s.MaxLength {
				label = label[:*s.MaxLength]
			}
			candidates = []any{label, "example", "1"}
		case "array":
			if empty := []any{}; !strings.HasSuffix(name, "Ids") && s.Validate(empty) == nil {
				return empty, true
			}
			size := 1
			if s.MinItems != nil {
				size = max(size, *s.MinItems)
			}
			if size <= 32 {
				items := make([]any, 0, size)
				for i := range size {
					itemSchema := s.Items2020
					if i < len(s.PrefixItems) {
						itemSchema = s.PrefixItems[i]
					}
					item, ok := exampleValue(itemSchema, strings.TrimSuffix(name, "s"), depth+1)
					if !ok {
						break
					}
					items = append(items, item)
				}
				candidates = append(candidates, items)
			}
			candidates = append(candidates, []any{})
		case "number", "integer":
			candidates = []any{1, 0}
			for _, bound := range []*big.Rat{s.Minimum, s.Maximum} {
				if bound != nil && bound.IsInt() {
					candidates = append(candidates, json.Number(bound.Num().String()))
				}
			}
		case "boolean":
			candidates = []any{false}
		}
		for _, candidate := range candidates {
			if s.Validate(candidate) == nil {
				return candidate, true
			}
		}
	}
	return nil, s.Validate(nil) == nil
}

func exampleFieldCount(value any) int {
	total := 0
	switch v := value.(type) {
	case map[string]any:
		total = len(v)
		for _, child := range v {
			total += exampleFieldCount(child)
		}
	case []any:
		for _, child := range v {
			total += exampleFieldCount(child)
		}
	}
	return total
}

func splitCamelCase(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) && b.Len() > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}
