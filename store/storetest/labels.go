package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/store"
)

func testLabels(t *testing.T, s store.Store) {
	ctx := context.Background()
	if got := must[[]store.LabelCount](t)(s.ListLabels(ctx, "item", nil)); got == nil || len(got) != 0 {
		t.Fatalf("a record type without records must list no labels, as an empty non-nil slice: %#v", got)
	}
	e := Engine(t, s)
	filterFixture(t, e)
	// Labels sort in byte order: uppercase before lowercase.
	must[store.Record](t)(e.Update(ctx, "item", "I-05", json.RawMessage(`{"name":"eggplant","qty":3,"status":"closed"}`), "Zeta", "ops"))
	// A deleted record's labels no longer count.
	must[store.Record](t)(e.Insert(ctx, "item", json.RawMessage(`{"name":"fig","status":"open"}`), "gone", "urgent"))
	if err := e.Delete(ctx, "item", "I-06"); err != nil {
		t.Fatal(err)
	}
	f := func(f query.Filter) *query.Filter { return &f }
	cases := []struct {
		name   string
		filter *query.Filter
		want   string
	}{
		{"all records", nil, `[{"label":"Zeta","count":1},{"label":"ops","count":3},{"label":"urgent","count":2}]`},
		{"filtered on data", f(query.Eq("/status", "open")), `[{"label":"urgent","count":1}]`},
		{"filtered on meta", f(query.Eq("$version", 1)), `[{"label":"ops","count":2},{"label":"urgent","count":1}]`},
		{"filtered on labels", f(query.Contains("$labels", "urgent")), `[{"label":"ops","count":1},{"label":"urgent","count":2}]`},
		{"negated filter", f(query.Not(query.Exists("/status"))), `[{"label":"ops","count":2},{"label":"urgent","count":1}]`},
		{"no match", f(query.Eq("/name", "zzz")), `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := must[[]store.LabelCount](t)(e.ListLabels(ctx, "item", tc.filter))
			if j := asJSON(t, got); j != tc.want {
				t.Fatalf("want %s, got %s", tc.want, j)
			}
		})
	}
}
