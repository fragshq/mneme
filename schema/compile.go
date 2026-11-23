package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	validator "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/jsonptr"
)

// Compiled is a Definition ready to validate records.
type Compiled struct {
	Definition Definition
	validator  *validator.Schema
	fields     []Field
}

// Compile checks a definition and compiles its schema for validation (JSON
// Schema draft 2020-12 unless the schema declares "$schema"). The root must
// be "type": "object", because records are JSON objects. Remote "$ref"s are
// not resolved. The ID strategy is not compiled here; see idgen.Compile.
func Compile(d Definition) (*Compiled, error) {
	if d.Schema == nil {
		return nil, errs.Invalidf("schema is required")
	}
	if d.Schema.Type != "object" {
		return nil, errs.Invalidf(`schema root must be "type": "object"`)
	}
	raw, err := json.Marshal(d.Schema)
	if err != nil {
		return nil, errs.Invalidf("schema cannot be encoded: %v", err)
	}
	doc, err := validator.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, errs.Invalidf("%v", err)
	}
	url := fmt.Sprintf("mneme:///%s/v%d.json", d.Name, d.Version)
	c := validator.NewCompiler()
	c.DefaultDraft(validator.Draft2020)
	if err := c.AddResource(url, doc); err != nil {
		return nil, errs.Invalidf("%v", err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return nil, errs.Invalidf("invalid JSON Schema: %v", err)
	}
	return &Compiled{Definition: d, validator: sch, fields: collectFields(d.Schema)}, nil
}

// Validate checks that data (a JSON document) is an object conforming to the
// schema. Failures are returned as *ValidationError.
func (c *Compiled) Validate(data json.RawMessage) error {
	inst, err := validator.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return c.invalid(Issue{Message: "record is not valid JSON: " + err.Error()})
	}
	if _, ok := inst.(map[string]any); !ok {
		return c.invalid(Issue{Message: "record must be a JSON object"})
	}
	err = c.validator.Validate(inst)
	if err == nil {
		return nil
	}
	ve := &validator.ValidationError{}
	if !errors.As(err, &ve) {
		return errs.Invalidf("%v", err)
	}
	out := c.invalid()
	collectIssues(ve.BasicOutput(), out)
	if len(out.Issues) == 0 {
		out.Issues = []Issue{{Message: ve.Error()}}
	}
	return out
}

// invalid builds a ValidationError against c's version.
func (c *Compiled) invalid(issues ...Issue) *ValidationError {
	return &ValidationError{RecordType: c.Definition.Name, Version: c.Definition.Version, Issues: issues}
}

func collectIssues(u *validator.OutputUnit, out *ValidationError) {
	if u == nil {
		return
	}
	if u.Error != nil && len(u.Errors) == 0 {
		out.Issues = append(out.Issues, Issue{Path: u.InstanceLocation, Message: u.Error.String()})
	}
	for i := range u.Errors {
		collectIssues(&u.Errors[i], out)
	}
}

// Field describes one property found in a schema, addressed by JSON pointer.
type Field struct {
	Path        string   `json:"path"`
	Types       []string `json:"types,omitempty"`
	Description string   `json:"description,omitempty"`
	Required    bool     `json:"required,omitempty"`
	// Versions lists the schema versions declaring the field (set by MergeFields).
	Versions []int `json:"versions,omitempty"`
}

// FieldsContain reports whether path (a JSON pointer) is one of fields, or
// lies beneath one (e.g. an object without declared properties).
func FieldsContain(fields []Field, path string) bool {
	for _, f := range fields {
		if f.Path == path || strings.HasPrefix(path, f.Path+"/") {
			return true
		}
	}
	return false
}

func collectFields(root *JSONSchema) []Field {
	out := []Field{}
	var walk func(prefix string, node *JSONSchema)
	walk = func(prefix string, node *JSONSchema) {
		req := map[string]bool{}
		for _, r := range node.Required {
			req[r] = true
		}
		for name, p := range node.Properties {
			if p == nil {
				continue
			}
			path := prefix + jsonptr.Pointer{name}.String()
			out = append(out, Field{Path: path, Required: req[name], Types: typesOf(p), Description: p.Description})
			walk(path, p)
		}
	}
	walk("", root)
	slices.SortFunc(out, func(a Field, b Field) int { return strings.Compare(a.Path, b.Path) })
	return out
}

func typesOf(p *JSONSchema) []string {
	if p.Type != "" {
		return []string{p.Type}
	}
	return append([]string(nil), p.Types...)
}

// MergeFields unions the fields of several compiled versions, recording which
// versions declare each field. Required is true only if every version
// requires it, i.e. every stored record is guaranteed to have it.
func MergeFields(versions []*Compiled) []Field {
	byPath := map[string]*Field{}
	for _, c := range versions {
		for _, f := range c.fields {
			m, ok := byPath[f.Path]
			if !ok {
				cp := f
				cp.Types = append([]string(nil), f.Types...)
				byPath[f.Path] = &cp
				m = &cp
			} else {
				m.Required = m.Required && f.Required
				for _, t := range f.Types {
					if !slices.Contains(m.Types, t) {
						m.Types = append(m.Types, t)
					}
				}
				if f.Description != "" {
					m.Description = f.Description
				}
			}
			m.Versions = append(m.Versions, c.Definition.Version)
		}
	}
	out := make([]Field, 0, len(byPath))
	for _, p := range slices.Sorted(maps.Keys(byPath)) {
		f := *byPath[p]
		f.Required = f.Required && len(f.Versions) == len(versions)
		out = append(out, f)
	}
	return out
}
