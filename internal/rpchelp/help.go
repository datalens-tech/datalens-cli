package rpchelp

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/datalens-tech/datalens-cli/internal/output"
)

//go:embed cloud.json
var cloudData []byte

//go:embed enterprise.json
var enterpriseData []byte

//go:embed yateam.json
var yateamData []byte

type operation struct {
	Description string          `json:"description"`
	Example     json.RawMessage `json:"example"`
	Schema      json.RawMessage `json:"schema"`
	Definitions []string        `json:"definitions"`
}

type catalog struct {
	Actions map[string]operation       `json:"actions"`
	Schemas map[string]json.RawMessage `json:"schemas"`
}

func bundled(data []byte) func() (catalog, error) {
	return sync.OnceValues(func() (catalog, error) {
		var c catalog
		err := json.Unmarshal(data, &c)
		return c, err
	})
}

var cloud = bundled(cloudData)
var enterprise = bundled(enterpriseData)
var yateam = bundled(yateamData)

func reference(installation string) (string, catalog, error) {
	var edition string
	var load func() (catalog, error)
	switch installation {
	case "cloud":
		edition, load = "Cloud", cloud
	case "on-premises", "stackland":
		edition, load = "On-premises", enterprise
	case "yateam":
		edition, load = "Yandex Team", yateam
	default:
		return "", catalog{}, fmt.Errorf("unknown installation type %q", installation)
	}
	c, err := load()
	return edition, c, err
}

func Schema(action, installation string) (json.RawMessage, error) {
	edition, c, err := reference(installation)
	if err != nil {
		return nil, err
	}
	op, ok := c.Actions[action]
	if !ok {
		return nil, &output.Error{Code: "RPC_ACTION_UNDOCUMENTED", Message: fmt.Sprintf("No bundled documentation for this RPC action in the %s API reference.", edition), ExitCode: 2}
	}
	if len(op.Schema) == 0 {
		return nil, &output.Error{Code: "RPC_SCHEMA_UNAVAILABLE", Message: fmt.Sprintf("The %s API reference does not describe an argument schema for %s.", edition, action), ExitCode: 2}
	}
	return c.argumentSchema(op)
}

func Write(w io.Writer, action, installation, explicitProfile string) error {
	edition, c, err := reference(installation)
	if err != nil {
		return err
	}
	profileFlag := profileOption(explicitProfile)
	op, ok := c.Actions[action]
	if !ok {
		_, err := fmt.Fprintf(w, "Usage:\n  datalens rpc %s [JSON]%s\n\nNo bundled documentation for this RPC action in the %s API reference.\n", action, profileFlag, edition)
		return err
	}
	body, err := c.format(action, edition, profileFlag, op)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, body)
	return err
}

func (c catalog) format(action, edition, profileFlag string, op operation) (string, error) {
	var text strings.Builder
	var fields arguments
	if len(op.Schema) > 0 {
		var err error
		fields, err = c.summarize(op.Schema, map[string]bool{})
		if err != nil {
			return "", err
		}
	}
	args := "[JSON]"
	if len(fields.required) > 0 {
		args = "JSON"
	}
	fmt.Fprintf(&text, "Usage:\n  datalens rpc %s %s%s\n\n%s API reference\n", action, args, profileFlag, edition)
	if op.Description != "" {
		fmt.Fprintf(&text, "\n%s\n", op.Description)
	}
	if len(fields.required) > 0 {
		fmt.Fprintf(&text, "\nRequired: %s\n", strings.Join(slices.Sorted(maps.Keys(fields.required)), ", "))
	}
	if fields.variants {
		text.WriteString("Additional argument requirements depend on the variant.\n")
	} else {
		for k := range fields.required {
			delete(fields.properties, k)
		}
		if len(fields.properties) > 0 {
			fmt.Fprintf(&text, "Optional: %s\n", strings.Join(slices.Sorted(maps.Keys(fields.properties)), ", "))
		}
	}
	example := op.Example
	if len(example) == 0 && len(op.Schema) != 0 {
		schema, err := c.compileOperation(op)
		if err != nil {
			return "", err
		}
		example = minimalExample(schema)
	} else if len(example) == 0 && action == "getTenantDetails" {
		example = json.RawMessage(`{}`)
	}
	if len(example) != 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, example); err != nil {
			return "", err
		}
		fmt.Fprintf(&text, "\nExample:\n  %s\n", exampleCommand(action, profileFlag, compact.String()))
	}
	if len(op.Schema) != 0 {
		fmt.Fprintf(&text, "\nFull argument schema (JSON):\n  datalens schema show %s%s\n", action, profileFlag)
	}
	return text.String(), nil
}

func profileOption(name string) string {
	if name == "" {
		return ""
	}
	return " --profile=" + name
}

func exampleCommand(action, profileFlag, body string) string {
	command := "datalens rpc " + action
	if body != "{}" {
		command += " '" + strings.ReplaceAll(body, "'", "'\\''") + "'"
	}
	return command + profileFlag
}

type arguments struct {
	required, properties map[string]bool
	variants             bool
}

func (c catalog) summarize(raw json.RawMessage, visiting map[string]bool) (arguments, error) {
	var node struct {
		Ref                 string `json:"$ref"`
		Required            []string
		Properties          map[string]json.RawMessage
		AllOf, AnyOf, OneOf []json.RawMessage
	}
	fields := arguments{required: map[string]bool{}, properties: map[string]bool{}}
	if err := json.Unmarshal(raw, &node); err != nil {
		return fields, err
	}
	for _, name := range node.Required {
		fields.required[name] = true
	}
	for name := range node.Properties {
		fields.properties[name] = true
	}
	if node.Ref != "" && !visiting[node.Ref] {
		name := strings.TrimPrefix(node.Ref, "#/$defs/")
		name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
		def, ok := c.Schemas[name]
		if !ok {
			return fields, fmt.Errorf("missing RPC schema definition %q", name)
		}
		visiting[node.Ref] = true
		defer delete(visiting, node.Ref)
		node.AllOf = append(node.AllOf, def)
	}
	for _, part := range node.AllOf {
		child, err := c.summarize(part, visiting)
		if err != nil {
			return fields, err
		}
		maps.Copy(fields.required, child.required)
		maps.Copy(fields.properties, child.properties)
		fields.variants = fields.variants || child.variants
	}
	for _, variants := range [][]json.RawMessage{node.OneOf, node.AnyOf} {
		var common map[string]bool
		for _, variant := range variants {
			fields.variants = true
			child, err := c.summarize(variant, visiting)
			if err != nil {
				return fields, err
			}
			if common == nil {
				common = child.required
			} else {
				maps.DeleteFunc(common, func(key string, _ bool) bool { return !child.required[key] })
			}
		}
		maps.Copy(fields.required, common)
	}
	return fields, nil
}

func (c catalog) argumentSchema(op operation) (json.RawMessage, error) {
	schema := op.Schema
	if len(op.Definitions) != 0 {
		defs := make(map[string]json.RawMessage, len(op.Definitions))
		for _, name := range op.Definitions {
			def, ok := c.Schemas[name]
			if !ok {
				return nil, fmt.Errorf("missing RPC schema definition %q", name)
			}
			defs[name] = def
		}
		root, ok := bytes.CutSuffix(bytes.TrimSpace(schema), []byte("}"))
		if !ok {
			return nil, fmt.Errorf("RPC schema with definitions must be an object")
		}
		var bundle bytes.Buffer
		bundle.Write(root)
		bundle.WriteString(`,"$defs":`)
		if err := json.NewEncoder(&bundle).Encode(defs); err != nil {
			return nil, err
		}
		bundle.WriteByte('}')
		schema = bundle.Bytes()
	}
	return schema, nil
}
