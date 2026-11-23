package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/schema"
	"github.com/fragshq/mneme/store"
)

func testDefinitions(t *testing.T, s store.Store) {
	ctx := context.Background()
	if _, err := s.GetDefinition(ctx, "nope", 0); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing record type: want ErrNotFound, got %v", err)
	}
	if _, err := s.ListVersions(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ListVersions missing record type: want ErrNotFound, got %v", err)
	}
	for _, d := range []schema.Definition{
		def("beta", 1, ""),
		def("alpha", 1, "a1"),
		def("alpha", 2, "a2"),
	} {
		if err := s.PutDefinition(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PutDefinition(ctx, def("alpha", 2, "dup")); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate version: want ErrConflict, got %v", err)
	}
	latest := must[schema.Definition](t)(s.GetDefinition(ctx, "alpha", 0))
	if latest.Version != 2 || latest.Title != "a2" {
		t.Fatalf("latest alpha: got v%d %q", latest.Version, latest.Title)
	}
	if !latest.CreatedAt.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("CreatedAt not preserved: %v", latest.CreatedAt)
	}
	v1 := must[schema.Definition](t)(s.GetDefinition(ctx, "alpha", 1))
	if v1.Version != 1 || v1.Title != "a1" {
		t.Fatalf("alpha v1: got v%d %q", v1.Version, v1.Title)
	}
	// Every field of a definition must round-trip.
	full := schema.Definition{
		Name: "full", Version: 3, Title: "Full", Description: "All fields set",
		IDStrategy: idgen.NewSpec(idgen.Fixed("F-"), idgen.Sequence(idgen.SequenceOptions{Name: "f", Pad: 3})),
		Indexes:    []string{"/a", "b.c"},
		Schema: &schema.JSONSchema{
			Type:     "object",
			Required: []string{"a"},
			Properties: map[string]*schema.JSONSchema{
				"a": {Type: "string", Description: "the a", MinLength: jsonschema.Ptr(2)},
				"b": {Type: "object", Properties: map[string]*schema.JSONSchema{"c": {Types: []string{"integer", "null"}}}},
			},
		},
		CreatedAt: time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC),
	}
	if err := s.PutDefinition(ctx, full); err != nil {
		t.Fatal(err)
	}
	back := must[schema.Definition](t)(s.GetDefinition(ctx, "full", 3))
	if !back.CreatedAt.Equal(full.CreatedAt) {
		t.Fatalf("CreatedAt: got %v", back.CreatedAt)
	}
	back.CreatedAt = full.CreatedAt
	if got, want := asJSON(t, back), asJSON(t, full); got != want {
		t.Fatalf("definition does not round-trip\nwant %s\ngot  %s", want, got)
	}
	if _, err := s.GetDefinition(ctx, "alpha", 9); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing version: want ErrNotFound, got %v", err)
	}
	all := must[[]schema.Definition](t)(s.ListDefinitions(ctx))
	if len(all) != 3 || all[0].Name != "alpha" || all[0].Version != 2 || all[1].Name != "beta" || all[2].Name != "full" {
		t.Fatalf("ListDefinitions: got %+v", names(all))
	}
	vs := must[[]schema.Definition](t)(s.ListVersions(ctx, "alpha"))
	if len(vs) != 2 || vs[0].Version != 1 || vs[1].Version != 2 {
		t.Fatalf("ListVersions: got %+v", names(vs))
	}
}

func names(ds []schema.Definition) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, fmt.Sprintf("%s@%d", d.Name, d.Version))
	}
	return out
}
