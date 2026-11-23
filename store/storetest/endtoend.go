package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/jsonx"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/schema"
	"github.com/fragshq/mneme/store"
)

// testEndToEnd drives the engine through a record type's life: two schema
// versions, inserts with sequence IDs and labels, filtering across
// versions, update, delete and describe. Each step builds on the previous
// ones, so the scenario stops at the first failing step.
func testEndToEnd(t *testing.T, s store.Store) {
	ctx := context.Background()
	e := Engine(t, s)
	riskIDs := idgen.NewSpec(idgen.Fixed("RID-"), idgen.Sequence(idgen.SequenceOptions{Name: "rid", Pad: 4}))
	str := &schema.JSONSchema{Type: "string"}
	r1, r3, up := store.Record{}, store.Record{}, store.Record{}

	step := func(name string, fn func(t *testing.T)) {
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}

	step("define v1", func(t *testing.T) {
		v1 := must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "risk", Title: "Risk", IDStrategy: riskIDs,
			Schema: &schema.JSONSchema{Type: "object", Required: []string{"title"},
				Properties: map[string]*schema.JSONSchema{"title": str}}}))
		if v1.Version != 1 {
			t.Fatalf("first version: want 1, got %d", v1.Version)
		}
	})

	step("insert with sequence IDs and labels", func(t *testing.T) {
		r1 = must[store.Record](t)(e.Insert(ctx, "risk", json.RawMessage(`{"title":"flood"}`), "water", "urgent", "water"))
		if strings.Join(r1.Labels, ",") != "urgent,water" {
			t.Fatalf("labels must be a sorted, de-duplicated set: %#v", r1.Labels)
		}
		if !r1.UpdatedAt.Equal(r1.CreatedAt) {
			t.Fatalf("a fresh record must have UpdatedAt == CreatedAt: %+v", r1)
		}
		r2 := must[store.Record](t)(e.Insert(ctx, "risk", json.RawMessage(`{"title":"fire"}`)))
		if r1.ID != "RID-0001" || r2.ID != "RID-0002" || r1.SchemaVersion != 1 {
			t.Fatalf("ids: got %s, %s (v%d)", r1.ID, r2.ID, r1.SchemaVersion)
		}
		if got := must[store.Record](t)(e.Get(ctx, "risk", "RID-0002")); got.Labels == nil || len(got.Labels) != 0 {
			t.Fatalf("a record inserted without labels must read back with []: %#v", got.Labels)
		}
	})

	step("reject invalid inserts", func(t *testing.T) {
		if _, err := e.Insert(ctx, "risk", json.RawMessage(`{"title":"x"}`), ""); !errors.Is(err, mneme.ErrInvalid) {
			t.Fatalf("empty label: want ErrInvalid, got %v", err)
		}
		if _, err := e.Insert(ctx, "risk", json.RawMessage(`{"nope":1}`)); !errors.Is(err, mneme.ErrInvalid) {
			t.Fatalf("invalid record: want ErrInvalid, got %v", err)
		}
	})

	step("define v2, which validates new inserts", func(t *testing.T) {
		v2 := must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "risk", Title: "Risk", IDStrategy: riskIDs,
			Schema: &schema.JSONSchema{Type: "object", Required: []string{"title", "severity"},
				Properties: map[string]*schema.JSONSchema{"title": str, "severity": {Type: "integer"}}}}))
		if v2.Version != 2 {
			t.Fatalf("second version: want 2, got %d", v2.Version)
		}
		if _, err := e.Insert(ctx, "risk", json.RawMessage(`{"title":"old shape"}`)); !errors.Is(err, mneme.ErrInvalid) {
			t.Fatalf("v1-shaped insert after v2: want ErrInvalid, got %v", err)
		}
		r3 = must[store.Record](t)(e.Insert(ctx, "risk", json.RawMessage(`{"title":"quake","severity":5}`)))
		if r3.ID != "RID-0003" || r3.SchemaVersion != 2 {
			t.Fatalf("v2 insert: got %s v%d", r3.ID, r3.SchemaVersion)
		}
	})

	step("old records keep their version and match filters", func(t *testing.T) {
		if old := must[store.Record](t)(e.Get(ctx, "risk", "RID-0001")); old.SchemaVersion != 1 {
			t.Fatalf("v1 record read back with version %d", old.SchemaVersion)
		}
		f := query.Contains("/title", "f")
		p := must[query.Page[store.Record]](t)(e.List(ctx, "risk", query.Query{Filter: &f, Sort: query.Sorts{{Field: "$id"}}}))
		if IDs(p.Items) != "RID-0001,RID-0002" {
			t.Fatalf("filter across versions: got %s", IDs(p.Items))
		}
	})

	step("missing records and record types", func(t *testing.T) {
		if _, err := e.Get(ctx, "risk", "RID-9999"); !errors.Is(err, mneme.ErrNotFound) {
			t.Fatalf("missing record: want ErrNotFound, got %v", err)
		}
		if _, err := e.List(ctx, "ghost", query.Query{}); !errors.Is(err, mneme.ErrNotFound) {
			t.Fatalf("list unknown record type: want ErrNotFound, got %v", err)
		}
	})

	step("update is a full replace against the latest schema", func(t *testing.T) {
		if _, err := e.Update(ctx, "risk", "RID-0001", json.RawMessage(`{"title":"flood v1 shape"}`)); !errors.Is(err, mneme.ErrInvalid) {
			t.Fatalf("update must validate against the latest schema: want ErrInvalid, got %v", err)
		}
		up = must[store.Record](t)(e.Update(ctx, "risk", "RID-0001", json.RawMessage(`{"title":"flood","severity":2}`), "reviewed"))
		if strings.Join(up.Labels, ",") != "reviewed" {
			t.Fatalf("update must replace labels: %#v", up.Labels)
		}
		if up.ID != "RID-0001" || up.SchemaVersion != 2 {
			t.Fatalf("update must keep the ID and move to the latest version: got %s v%d", up.ID, up.SchemaVersion)
		}
		if !up.CreatedAt.Equal(r1.CreatedAt) || !up.UpdatedAt.After(r1.UpdatedAt) {
			t.Fatalf("update must keep CreatedAt and advance UpdatedAt: got %+v (original %+v)", up, r1)
		}
		back := must[store.Record](t)(e.Get(ctx, "risk", "RID-0001"))
		if strings.Join(back.Labels, ",") != "reviewed" || back.SchemaVersion != 2 || !back.UpdatedAt.Equal(up.UpdatedAt) {
			t.Fatalf("get after update: %+v", back)
		}
		if !jsonx.Equal(back.Data, json.RawMessage(`{"title":"flood","severity":2}`)) {
			t.Fatalf("data after update: %s", back.Data)
		}
		if _, err := e.Update(ctx, "risk", "RID-9999", json.RawMessage(`{"title":"x","severity":1}`)); !errors.Is(err, mneme.ErrNotFound) {
			t.Fatalf("update missing record: want ErrNotFound, got %v", err)
		}
	})

	step("filter on $updatedAt", func(t *testing.T) {
		f := query.Gt("$updatedAt", r3.UpdatedAt.Format(time.RFC3339Nano))
		if p := must[query.Page[store.Record]](t)(e.List(ctx, "risk", query.Query{Filter: &f})); IDs(p.Items) != "RID-0001" {
			t.Fatalf("filter on $updatedAt: got %s", IDs(p.Items))
		}
	})

	step("delete is hard and IDs are never reused", func(t *testing.T) {
		if err := e.Delete(ctx, "risk", "RID-0002"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Get(ctx, "risk", "RID-0002"); !errors.Is(err, mneme.ErrNotFound) {
			t.Fatalf("get deleted record: want ErrNotFound, got %v", err)
		}
		if err := e.Delete(ctx, "risk", "RID-0002"); !errors.Is(err, mneme.ErrNotFound) {
			t.Fatalf("delete twice: want ErrNotFound, got %v", err)
		}
		if _, err := e.Update(ctx, "risk", "RID-0002", json.RawMessage(`{"title":"x","severity":1}`)); !errors.Is(err, mneme.ErrNotFound) {
			t.Fatalf("update deleted record: want ErrNotFound, got %v", err)
		}
		if r4 := must[store.Record](t)(e.Insert(ctx, "risk", json.RawMessage(`{"title":"next","severity":1}`))); r4.ID != "RID-0004" {
			t.Fatalf("sequences must not reuse deleted IDs: got %s", r4.ID)
		}
	})

	step("describe merges fields across versions", func(t *testing.T) {
		desc := must[mneme.RecordTypeDescription](t)(e.DescribeRecordType(ctx, "risk"))
		paths := []string{}
		for _, f := range desc.Fields {
			paths = append(paths, f.Path)
		}
		if strings.Join(paths, ",") != "/severity,/title" || desc.LatestVersion != 2 {
			t.Fatalf("describe: got %v latest %d", paths, desc.LatestVersion)
		}
	})
}
