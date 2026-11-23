package rpchelp

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

//go:embed cloud.json enterprise.json
var documents embed.FS

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

var load = sync.OnceValues(func() ([2]catalog, error) {
	var references [2]catalog
	for i, name := range []string{"cloud.json", "enterprise.json"} {
		data, err := documents.ReadFile(name)
		if err != nil {
			return references, err
		}
		if err := json.Unmarshal(data, &references[i]); err != nil {
			return references, err
		}
	}
	return references, nil
})

func Write(w io.Writer, action string) error {
	references, err := load()
	if err != nil {
		return err
	}
	var entries [2]string
	var present [2]bool
	for i, reference := range references {
		if op, ok := reference.Actions[action]; ok {
			present[i] = true
			entries[i], err = reference.format(action, op)
			if err != nil {
				return err
			}
		}
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Usage:\n  datalens rpc %s [--args JSON]\n", action)
	switch {
	case !present[0] && !present[1]:
		text.WriteString("\nNo bundled documentation for this RPC action.\n")
	case present[0] && present[1] && entries[0] == entries[1]:
		fmt.Fprintf(&text, "\nCloud and Enterprise API reference\n%s", entries[0])
	default:
		for i, edition := range []string{"Cloud", "Enterprise"} {
			if present[i] {
				fmt.Fprintf(&text, "\n%s API reference\n%s", edition, entries[i])
			}
		}
	}
	_, err = io.WriteString(w, text.String())
	return err
}

func (c catalog) format(action string, op operation) (string, error) {
	var text strings.Builder
	if op.Description != "" {
		fmt.Fprintf(&text, "\n%s\n", op.Description)
	}
	example := op.Example
	if len(example) == 0 {
		switch action {
		case "getTenantDetails":
			example = json.RawMessage(`{}`)
		case "getWorkbook":
			example = json.RawMessage(`{"workbookId":"your-workbook-id"}`)
		}
	}
	if len(example) != 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, example); err != nil {
			return "", err
		}
		fmt.Fprintf(&text, "\nExample:\n  datalens rpc %s", action)
		if compact.String() != "{}" {
			fmt.Fprintf(&text, " --args '%s'", strings.ReplaceAll(compact.String(), "'", "'\\''"))
		}
		text.WriteByte('\n')
	}
	if len(op.Schema) != 0 {
		schema, err := c.argumentSchema(op)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&text, "\nArguments schema:\n%s\n", schema)
	}
	return text.String(), nil
}

func (c catalog) argumentSchema(op operation) (string, error) {
	schema := op.Schema
	if len(op.Definitions) != 0 {
		defs := make(map[string]json.RawMessage, len(op.Definitions))
		for _, name := range op.Definitions {
			def, ok := c.Schemas[name]
			if !ok {
				return "", fmt.Errorf("missing RPC schema definition %q", name)
			}
			defs[name] = def
		}
		root, ok := bytes.CutSuffix(bytes.TrimSpace(schema), []byte("}"))
		if !ok {
			return "", fmt.Errorf("RPC schema with definitions must be an object")
		}
		var bundle bytes.Buffer
		bundle.Write(root)
		bundle.WriteString(`,"$defs":`)
		if err := json.NewEncoder(&bundle).Encode(defs); err != nil {
			return "", err
		}
		bundle.WriteByte('}')
		schema = bundle.Bytes()
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, schema, "", "  "); err != nil {
		return "", err
	}
	return indented.String(), nil
}
