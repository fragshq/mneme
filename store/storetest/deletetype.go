package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/schema"
	"github.com/fragshq/mneme/store"
)

// testDeleteRecordType checks that deleting a record type removes its
// definitions, records and sequence counters, and nothing of other types.
func testDeleteRecordType(t *testing.T, s store.Store) {
	ctx := context.Background()
	if err := s.DeleteRecords(ctx, "ghost"); err != nil {
		t.Fatalf("DeleteRecords of a type without records: %v", err)
	}
	if err := s.DeleteSequences(ctx, "ghost"); err != nil {
		t.Fatalf("DeleteSequences of a type without counters: %v", err)
	}
	if err := s.DeleteDefinitions(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("DeleteDefinitions of an unknown type: want ErrNotFound, got %v", err)
	}

	e := Engine(t, s)
	filterFixture(t, e) // "item" v1+v2, I-01..I-05, with labels
	otherIDs := idgen.NewSpec(idgen.Fixed("O-"), idgen.Sequence(idgen.SequenceOptions{Name: "o"}))
	must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "other", IDStrategy: otherIDs, Schema: &schema.JSONSchema{Type: "object"}}))
	must[store.Record](t)(e.Insert(ctx, "other", json.RawMessage(`{}`), "keep"))

	if err := e.DeleteRecordType(ctx, "item"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.GetRecordType(ctx, "item", 0); !errors.Is(err, mneme.ErrNotFound) {
		t.Fatalf("deleted definitions: %v", err)
	}
	if _, err := s.ListVersions(ctx, "item"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted versions: %v", err)
	}
	if ds := must[[]schema.Definition](t)(s.ListDefinitions(ctx)); len(ds) != 1 || ds[0].Name != "other" {
		t.Fatalf("only other remains: %v", names(ds))
	}
	if _, err := s.GetRecord(ctx, "item", "I-01"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted record: %v", err)
	}
	if p := must[query.Page[store.Record]](t)(s.ListRecords(ctx, "item", normalized(t, query.Query{}))); len(p.Items) != 0 {
		t.Fatalf("deleted records still listed: %s", IDs(p.Items))
	}
	if ls := must[[]store.LabelCount](t)(s.ListLabels(ctx, "item", nil)); len(ls) != 0 {
		t.Fatalf("deleted records' labels still listed: %+v", ls)
	}

	// Other record types are untouched, counters included.
	if _, err := e.Get(ctx, "other", "O-1"); err != nil {
		t.Fatalf("another type's record: %v", err)
	}
	if r := must[store.Record](t)(e.Insert(ctx, "other", json.RawMessage(`{}`))); r.ID != "O-2" {
		t.Fatalf("another type's sequence must continue: %s", r.ID)
	}

	// The name is free again: versions and sequences start over.
	itemIDs := idgen.NewSpec(idgen.Fixed("I-"), idgen.Sequence(idgen.SequenceOptions{Name: "item", Pad: 2}))
	d := must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "item", IDStrategy: itemIDs, Schema: &schema.JSONSchema{Type: "object"}}))
	if d.Version != 1 {
		t.Fatalf("a recreated type starts at version 1, got %d", d.Version)
	}
	if r := must[store.Record](t)(e.Insert(ctx, "item", json.RawMessage(`{}`))); r.ID != "I-01" {
		t.Fatalf("a recreated type's sequence starts at 1: %s", r.ID)
	}
	if p := must[query.Page[store.Record]](t)(e.List(ctx, "item", query.Query{})); IDs(p.Items) != "I-01" {
		t.Fatalf("a recreated type holds only its new records: %s", IDs(p.Items))
	}

	if err := e.DeleteRecordType(ctx, "item"); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteRecordType(ctx, "item"); !errors.Is(err, mneme.ErrNotFound) {
		t.Fatalf("deleting twice: want ErrNotFound, got %v", err)
	}
	if err := e.DeleteRecordType(ctx, "Bad Name"); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("invalid name: want ErrInvalid, got %v", err)
	}
	if got := strings.Join(names(must[[]schema.Definition](t)(s.ListDefinitions(ctx))), ","); got != "other@1" {
		t.Fatalf("remaining types: %s", got)
	}
}
