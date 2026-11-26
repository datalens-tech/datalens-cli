package rpchelp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/datalens-tech/datalens-cli/internal/output"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

func ValidateArgs(action, installation, explicitProfile string, args json.RawMessage) error {
	_, c, err := reference(installation)
	if err != nil {
		return err
	}
	op := c.Actions[action]
	if len(op.Schema) == 0 {
		return nil
	}
	schema, err := c.compileOperation(op)
	if err != nil {
		return fmt.Errorf("invalid bundled schema for %s: %w", action, err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(args))
	if err != nil {
		return &output.Error{Code: "INVALID_USAGE", Message: "RPC arguments must be valid JSON.", ExitCode: 2}
	}
	if !numbersRepresentable(value) {
		return &output.Error{Code: "INVALID_USAGE", Message: "A number in the JSON object exceeds the range supported by schema validation.", ExitCode: 2}
	}
	if action == "getAuditEntriesUpdates" {
		fields, _ := value.(map[string]any)
		for _, key := range []string{"from", "to"} {
			timestamp, ok := fields[key].(string)
			// The audit API also accepts minute precision; validate it as zero seconds.
			if ok && len(timestamp) > 16 && timestamp[10] == 'T' && strings.ContainsRune("Z+-", rune(timestamp[16])) {
				fields[key] = timestamp[:16] + ":00" + timestamp[16:]
			}
		}
	}
	if err := schema.Validate(value); err != nil {
		var problem *jsonschema.ValidationError
		if !errors.As(err, &problem) {
			return err
		}
		profileFlag := profileOption(explicitProfile)
		message := fmt.Sprintf("Invalid arguments for %s: %s.", action, validationProblem(problem))
		if example := minimalExample(schema); len(example) != 0 {
			message += "\nMinimal example (from the schema):\n  " + ExampleCommands(action, string(example), profileFlag)
		} else {
			fields := summarize(schema, map[*jsonschema.Schema]bool{})
			if len(fields.required) > 0 {
				message += "\nRequired: " + strings.Join(slices.Sorted(maps.Keys(fields.required)), ", ")
			}
			if fields.variants {
				message += "\nAdditional required fields depend on the variant."
			}
		}
		message += "\nFull help: datalens rpc " + action + " --help" + profileFlag
		return &output.Error{Code: "INVALID_USAGE", Message: message, ExitCode: 2}
	}
	return nil
}

func (c catalog) compileOperation(op operation) (*jsonschema.Schema, error) {
	raw, err := c.argumentSchema(op)
	if err != nil {
		return nil, err
	}
	return compileSchema(raw)
}

// The validator compares JSON numbers as big.Rat without checking conversion errors.
func numbersRepresentable(value any) bool {
	switch v := value.(type) {
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			_, ok := new(big.Rat).SetString(string(v))
			return ok
		}
	case []any:
		for _, item := range v {
			if !numbersRepresentable(item) {
				return false
			}
		}
	case map[string]any:
		for _, field := range v {
			if !numbersRepresentable(field) {
				return false
			}
		}
	}
	return true
}

func compileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	const resource = "urn:datalens:rpc-arguments"
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	if err := compiler.AddResource(resource, doc); err != nil {
		return nil, err
	}
	return compiler.Compile(resource)
}

func validationProblem(err *jsonschema.ValidationError) string {
	field := "the JSON object"
	for {
		// Instance paths can contain user-supplied keys; use field names from the bundled schema.
		if i := strings.LastIndex(err.SchemaURL, "/properties/"); i >= 0 {
			field, _, _ = strings.Cut(err.SchemaURL[i+len("/properties/"):], "/")
		}
		switch k := err.ErrorKind.(type) {
		case *kind.Schema, *kind.Group, *kind.Reference, *kind.AllOf:
			if len(err.Causes) > 0 {
				err = err.Causes[0]
				continue
			}
		case *kind.Required:
			return fmt.Sprintf("%s is missing %s", field, strings.Join(k.Missing, ", "))
		case *kind.Type:
			return fmt.Sprintf("%s must be %s", field, strings.Join(k.Want, " or "))
		case *kind.AdditionalProperties:
			return field + " contains unrecognized properties"
		case *kind.Enum, *kind.Const:
			return field + " has an unsupported value"
		case *kind.OneOf, *kind.AnyOf:
			return field + " does not match the supported variants"
		}
		if keywords := err.ErrorKind.KeywordPath(); len(keywords) > 0 {
			return fmt.Sprintf("%s does not satisfy %s", field, keywords[0])
		}
		return "the supplied arguments do not match the schema"
	}
}
