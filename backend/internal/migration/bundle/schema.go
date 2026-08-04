package bundle

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schema/bundle.schema.json
var schemaBytes []byte

// SchemaBytes returns the embedded JSON Schema as raw bytes.
func SchemaBytes() []byte {
	out := make([]byte, len(schemaBytes))
	copy(out, schemaBytes)
	return out
}

var compiledSchema *jsonschema.Schema
var compiledSchemaErr error
var compileOnce sync.Once

func compile() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		var raw any
		if err := json.Unmarshal(schemaBytes, &raw); err != nil {
			compiledSchemaErr = fmt.Errorf("bundle: parse schema: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("bundle.schema.json", raw); err != nil {
			compiledSchemaErr = fmt.Errorf("bundle: add schema: %w", err)
			return
		}
		sc, err := c.Compile("bundle.schema.json")
		if err != nil {
			compiledSchemaErr = fmt.Errorf("bundle: compile schema: %w", err)
			return
		}
		compiledSchema = sc
	})
	if compiledSchemaErr != nil {
		return nil, compiledSchemaErr
	}
	return compiledSchema, nil
}

// Validate runs the embedded JSON Schema against a bundle. It first
// serialises the bundle through encoding/json to reuse Go tag rules,
// then decodes into a generic tree for jsonschema/v6.
func Validate(b *CanonicalMigrationBundle) error {
	if b == nil {
		return fmt.Errorf("bundle: cannot validate nil bundle")
	}
	data, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("bundle: marshal for validate: %w", err)
	}
	return ValidateJSON(data)
}

// ValidateJSON validates a raw JSON payload against the embedded schema.
func ValidateJSON(data []byte) error {
	sc, err := compile()
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("bundle: parse for validate: %w", err)
	}
	if err := sc.Validate(v); err != nil {
		return fmt.Errorf("bundle: schema violation: %s", oneLineErr(err))
	}
	return nil
}

func oneLineErr(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), "\n", "; ")
}
