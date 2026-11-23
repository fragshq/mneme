package schema_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/schema"
)

func riskDef() schema.Definition {
	return schema.Definition{
		Name:       "risk",
		Version:    1,
		Title:      "Risk",
		IDStrategy: idgen.NewSpec(idgen.Fixed("RID-"), idgen.Sequence(idgen.SequenceOptions{Name: "rid"})),
		Indexes:    []string{"owner.id"},
		Schema: &schema.JSONSchema{
			Type:     "object",
			Required: []string{"title"},
			Properties: map[string]*schema.JSONSchema{
				"title":    {Type: "string", Description: "Short name"},
				"severity": {Types: []string{"integer", "null"}, Minimum: jsonschema.Ptr(1.0), Maximum: jsonschema.Ptr(5.0)},
				"owner": {Type: "object", Required: []string{"id"},
					Properties: map[string]*schema.JSONSchema{"id": {Type: "string"}}},
				"meta": {Type: "object"},
			},
		},
	}
}

func compile(t *testing.T, d schema.Definition) *schema.Compiled {
	t.Helper()
	c, err := schema.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestIDSpecDefault(t *testing.T) {
	if got := compile(t, riskDef()).Definition.IDSpec(); len(got.Segments) != 2 {
		t.Fatalf("explicit spec: %+v", got)
	}
	plain := schema.Definition{Name: "p", Version: 1, Schema: &schema.JSONSchema{Type: "object"}}
	if compile(t, plain).Definition.IDSpec().Segments[0].Kind != idgen.KindUUID {
		t.Fatal("default id spec should be uuid")
	}
}

func TestValidate(t *testing.T) {
	c := compile(t, riskDef())
	if err := c.Validate(json.RawMessage(`{"title":"x","severity":3,"owner":{"id":"u"}}`)); err != nil {
		t.Fatal(err)
	}
	err := c.Validate(json.RawMessage(`{"severity":9,"owner":{}}`))
	ve := &schema.ValidationError{}
	if !errors.As(err, &ve) || !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	paths := []string{}
	for _, is := range ve.Issues {
		paths = append(paths, is.Path)
	}
	joined := strings.Join(paths, ",")
	for _, want := range []string{"/severity", "/owner"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing issue at %s; issues: %+v", want, ve.Issues)
		}
	}
	if err := c.Validate(json.RawMessage(`[1]`)); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("array record: want ErrInvalid, got %v", err)
	}
}

func TestCompileRejects(t *testing.T) {
	for i, s := range []*schema.JSONSchema{
		nil,
		{Type: "string"},
		{Properties: map[string]*schema.JSONSchema{}},
		schema.MustParse(`{"type":"object","properties":{"a":{"type":"nonsense"}}}`),
		schema.MustParse(`{"type":"object","properties":{"a":{"pattern":"("}}}`),
	} {
		if _, err := schema.Compile(schema.Definition{Name: "x", Version: 1, Schema: s}); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("case %d: want ErrInvalid, got %v", i, err)
		}
	}
}

func TestFieldsAndMerge(t *testing.T) {
	v1 := compile(t, riskDef())
	fields := schema.MergeFields([]*schema.Compiled{v1})
	got := []string{}
	for _, f := range fields {
		got = append(got, f.Path)
	}
	if strings.Join(got, ",") != "/meta,/owner,/owner/id,/severity,/title" {
		t.Fatalf("fields: %v", got)
	}
	if !schema.FieldsContain(fields, "/meta/anything") || schema.FieldsContain(fields, "/nope") {
		t.Fatal("FieldsContain")
	}
	v2 := compile(t, schema.Definition{Name: "risk", Version: 2, Schema: schema.MustParse(
		`{"type":"object","properties":{"title":{"type":"string"},"status":{"type":"string"}},"required":["status"]}`)})
	merged := schema.MergeFields([]*schema.Compiled{v1, v2})
	byPath := map[string]schema.Field{}
	for _, f := range merged {
		byPath[f.Path] = f
	}
	if f := byPath["/title"]; len(f.Versions) != 2 || f.Required {
		t.Fatalf("/title: %+v", f)
	}
	// required in v2 only: v1 records may lack it, so not required overall
	if f := byPath["/status"]; len(f.Versions) != 1 || f.Versions[0] != 2 || f.Required {
		t.Fatalf("/status: %+v", f)
	}
	if f := byPath["/severity"]; strings.Join(f.Types, ",") != "integer,null" {
		t.Fatalf("/severity types: %+v", f)
	}
}

func TestCloneIsDeep(t *testing.T) {
	d := riskDef()
	c, err := d.Clone()
	if err != nil {
		t.Fatal(err)
	}
	d.Schema.Properties["title"].Type = "integer"
	d.IDStrategy.Segments[0] = idgen.Fixed("X")
	if c.Schema.Properties["title"].Type != "string" || string(c.IDStrategy.Segments[0].Config) != `"RID-"` {
		t.Fatal("Clone shares state with the original")
	}
}

func TestFromGoType(t *testing.T) {
	type Risk struct {
		Title    string `json:"title"`
		Severity int    `json:"severity,omitempty"`
	}
	s, err := jsonschema.For[Risk](nil)
	if err != nil {
		t.Fatal(err)
	}
	c := compile(t, schema.Definition{Name: "risk", Version: 1, Schema: s})
	if err := c.Validate(json.RawMessage(`{"title":"x"}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(json.RawMessage(`{"severity":2}`)); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("missing title: want ErrInvalid, got %v", err)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"risk", "risk_item", "a-1"} {
		if err := schema.ValidateName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Risk", "1risk", "risk item", strings.Repeat("a", 64)} {
		if err := schema.ValidateName(bad); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%q: want ErrInvalid", bad)
		}
	}
}
