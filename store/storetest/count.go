package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/store"
)

// testCount covers what testFilters, which counts every filter case, does
// not: no filter, empty and unknown record types, and writes.
func testCount(t *testing.T, s store.Store) {
	ctx := context.Background()
	if n := must[int](t)(s.CountRecords(ctx, "item", nil)); n != 0 {
		t.Fatalf("a record type without records must count 0, got %d", n)
	}
	e := Engine(t, s)
	filterFixture(t, e)
	if n := must[int](t)(e.Count(ctx, "item", nil)); n != 5 {
		t.Fatalf("no filter counts every record: want 5, got %d", n)
	}
	if n := must[int](t)(s.CountRecords(ctx, "other", nil)); n != 0 {
		t.Fatalf("an unknown record type must count 0, got %d", n)
	}
	open := query.Eq("/status", "open")
	r := must[store.Record](t)(e.Insert(ctx, "item", json.RawMessage(`{"name":"fig","status":"open"}`)))
	if n := must[int](t)(e.Count(ctx, "item", &open)); n != 2 {
		t.Fatalf("after insert: want 2, got %d", n)
	}
	if err := e.Delete(ctx, "item", r.ID); err != nil {
		t.Fatal(err)
	}
	if n := must[int](t)(e.Count(ctx, "item", nil)); n != 5 {
		t.Fatalf("after delete: want 5, got %d", n)
	}
}
