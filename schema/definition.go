// Package schema models Mneme record types: a JSON Schema plus Mneme
// settings (ID strategy, documentation, index hints), versioned under a
// mnemonic name.
//
// Schemas are *jsonschema.Schema values from github.com/google/jsonschema-go
// (aliased here as JSONSchema). Build them as Go literals, infer them from Go
// types with jsonschema.For[T], or unmarshal them from JSON.
package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
)

// JSONSchema is the schema type used by Mneme.
type JSONSchema = jsonschema.Schema

// Definition is one immutable version of a record type.
type Definition struct {
	Name    string `json:"name"`
	Version int    `json:"version"`
	// Title and Description document the record type for humans and LLM tooling.
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// IDStrategy builds record IDs. When nil, idgen.DefaultSpec (UUIDv7) is used.
	IDStrategy *idgen.Spec `json:"idStrategy,omitempty"`
	// Indexes are fields (JSON pointers or dotted paths) the application may
	// want to index. Mneme only passes them on; storage decides what to do.
	Indexes []string `json:"indexes,omitempty"`
	// Schema validates records. Its root must be "type": "object".
	Schema    *JSONSchema `json:"schema"`
	CreatedAt time.Time   `json:"createdAt"`
}

// IDSpec returns the effective ID strategy.
func (d Definition) IDSpec() idgen.Spec {
	if d.IDStrategy == nil || d.IDStrategy.IsZero() {
		return idgen.DefaultSpec()
	}
	return *d.IDStrategy
}

// Clone returns a deep copy, so stores can keep definitions safe from later
// mutation by callers.
func (d Definition) Clone() (Definition, error) {
	raw, err := json.Marshal(d)
	if err != nil {
		return Definition{}, err
	}
	out := Definition{}
	err = json.Unmarshal(raw, &out)
	return out, err
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

// ValidateName checks that name is a valid mnemonic record type name:
// lowercase, starting with a letter, then letters, digits, '_' or '-', at
// most 63 characters.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return errs.Invalidf("record type name %q must match %s", name, nameRe)
	}
	return nil
}

// MustParse unmarshals a JSON Schema document and panics on error. It is
// meant for constant schemas in tests and examples.
func MustParse(doc string) *JSONSchema {
	s := &JSONSchema{}
	if err := json.Unmarshal([]byte(doc), s); err != nil {
		panic(fmt.Sprintf("schema.MustParse: %v", err))
	}
	return s
}
