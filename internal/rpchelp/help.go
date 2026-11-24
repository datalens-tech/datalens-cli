package rpchelp

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

//go:embed cloud.json
var cloudData []byte

//go:embed enterprise.json
var enterpriseData []byte

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

func Write(w io.Writer, action, installation string) error {
	usage := fmt.Sprintf("Usage:\n  datalens rpc %s [--args JSON]\n", action)
	var edition string
	var load func() (catalog, error)
	switch installation {
	case "cloud":
		edition, load = "Cloud", cloud
	case "onpremise-standalone", "stackland":
		edition, load = "Enterprise", enterprise
	case "yateam":
		_, err := fmt.Fprint(w, usage, "\nNo bundled documentation for Yandex Team RPC actions. You can still call the action directly.\n")
		return err
	default:
		return fmt.Errorf("unknown installation type %q", installation)
	}
	c, err := load()
	if err != nil {
		return err
	}
	op, ok := c.Actions[action]
	if !ok {
		_, err := fmt.Fprintf(w, "%s\nNo bundled documentation for this RPC action in the %s API reference.\n", usage, edition)
		return err
	}
	body, err := c.format(action, op)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n%s API reference\n%s", usage, edition, body)
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
