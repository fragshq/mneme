package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"mneme/jsonx"
	"mneme/query"
	"mneme/store"
)

// record builds a stored record for the RecordStore tests.
func record(recordType string, id string, version int, data string, at time.Time, labels ...string) store.Record {
	return store.Record{
		RecordType:    recordType,
		ID:            id,
		SchemaVersion: version,
		Data:          json.RawMessage(data),
		CreatedAt:     at,
		UpdatedAt:     at,
		Labels:        append([]string{}, labels...),
	}
}

func testRecords(t *testing.T, s store.Store) {
	ctx := context.Background()
	at := time.Date(2026, 3, 4, 5, 6, 7, 123456000, time.UTC)
	r := record("thing", "T-1", 3, `{"a":1,"b":{"c":"x"}}`, at, "ops", "urgent")
	r.UpdatedAt = at.Add(time.Second)
	if err := s.InsertRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertRecord(ctx, r); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate id: want ErrConflict, got %v", err)
	}
	other := r
	other.RecordType = "other"
	if err := s.InsertRecord(ctx, other); err != nil {
		t.Fatalf("same id in another record type must be allowed: %v", err)
	}

	got := must[store.Record](t)(s.GetRecord(ctx, "thing", "T-1"))
	if got.ID != "T-1" || got.RecordType != "thing" || got.SchemaVersion != 3 {
		t.Fatalf("GetRecord identity: got %+v", got)
	}
	if !got.CreatedAt.Equal(r.CreatedAt) || !got.UpdatedAt.Equal(r.UpdatedAt) {
		t.Fatalf("timestamps not preserved: created %v, updated %v", got.CreatedAt, got.UpdatedAt)
	}
	if !jsonx.Equal(got.Data, r.Data) {
		t.Fatalf("data not preserved: %s", got.Data)
	}
	if strings.Join(got.Labels, ",") != "ops,urgent" {
		t.Fatalf("labels not preserved: %#v", got.Labels)
	}

	if err := s.InsertRecord(ctx, record("thing", "T-0", 1, `{}`, at)); err != nil {
		t.Fatal(err)
	}
	if got := must[store.Record](t)(s.GetRecord(ctx, "thing", "T-0")); got.Labels == nil || len(got.Labels) != 0 {
		t.Fatalf("no labels must read back as an empty, non-nil slice: %#v", got.Labels)
	}
	if _, err := s.GetRecord(ctx, "thing", "T-2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing record: want ErrNotFound, got %v", err)
	}
	if _, err := s.GetRecord(ctx, "missing-type", "T-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing record type: want ErrNotFound, got %v", err)
	}
}

func testUpdateDelete(t *testing.T, s store.Store) {
	ctx := context.Background()
	at := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, recordType := range []string{"thing", "other"} {
		if err := s.InsertRecord(ctx, record(recordType, "T-1", 1, `{"a":1}`, at, "old")); err != nil {
			t.Fatal(err)
		}
	}
	list := func() []store.Record {
		t.Helper()
		return must[query.Page[store.Record]](t)(s.ListRecords(ctx, "thing", normalized(t, query.Query{}))).Items
	}

	// Update replaces the whole record, CreatedAt included, as given.
	later := at.Add(time.Hour)
	upd := record("thing", "T-1", 2, `{"b":"new"}`, at, "a", "b")
	upd.UpdatedAt = later
	if err := s.UpdateRecord(ctx, upd); err != nil {
		t.Fatal(err)
	}
	got := must[store.Record](t)(s.GetRecord(ctx, "thing", "T-1"))
	if got.SchemaVersion != 2 || !jsonx.Equal(got.Data, upd.Data) || strings.Join(got.Labels, ",") != "a,b" {
		t.Fatalf("UpdateRecord must replace version, data and labels: got %+v", got)
	}
	if !got.CreatedAt.Equal(at) || !got.UpdatedAt.Equal(later) {
		t.Fatalf("UpdateRecord must store the timestamps as given: created %v, updated %v", got.CreatedAt, got.UpdatedAt)
	}
	if o := must[store.Record](t)(s.GetRecord(ctx, "other", "T-1")); !jsonx.Equal(o.Data, json.RawMessage(`{"a":1}`)) {
		t.Fatalf("update leaked into another record type: %s", o.Data)
	}
	if items := list(); len(items) != 1 || !jsonx.Equal(items[0].Data, upd.Data) || !items[0].UpdatedAt.Equal(later) {
		t.Fatalf("ListRecords after update: %+v", items)
	}
	missing := upd
	missing.ID = "T-404"
	if err := s.UpdateRecord(ctx, missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("update missing record: want ErrNotFound, got %v", err)
	}
	if _, err := s.GetRecord(ctx, "thing", "T-404"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("updating a missing record must not create it")
	}

	// Delete is hard, and an ID can be inserted again afterwards.
	if err := s.DeleteRecord(ctx, "thing", "T-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRecord(ctx, "thing", "T-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted record still readable: %v", err)
	}
	if items := list(); len(items) != 0 {
		t.Fatalf("deleted record still listed: %+v", items)
	}
	if err := s.DeleteRecord(ctx, "thing", "T-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: want ErrNotFound, got %v", err)
	}
	if _, err := s.GetRecord(ctx, "other", "T-1"); err != nil {
		t.Fatalf("delete leaked into another record type: %v", err)
	}
	if err := s.InsertRecord(ctx, record("thing", "T-1", 1, `{}`, later)); err != nil {
		t.Fatalf("an ID must be reusable after a hard delete: %v", err)
	}
}
