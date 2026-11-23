package storetest

import (
	"context"
	"encoding/json"
	"testing"

	"mneme"
	"mneme/idgen"
	"mneme/query"
	"mneme/schema"
	"mneme/store"
)

// filterFixture defines "item" v1 and v2 and inserts records across both.
func filterFixture(t *testing.T, e *mneme.Engine) {
	ctx := context.Background()
	itemIDs := idgen.NewSpec(idgen.Fixed("I-"), idgen.Sequence(idgen.SequenceOptions{Name: "item", Pad: 2}))
	str := &schema.JSONSchema{Type: "string"}
	num := &schema.JSONSchema{Type: "number"}
	arr := &schema.JSONSchema{Type: "array"}
	must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "item", IDStrategy: itemIDs, Schema: &schema.JSONSchema{
		Type: "object",
		Properties: map[string]*schema.JSONSchema{
			"name": str, "qty": num, "tags": arr, "note": {},
			"owner": {Type: "object", Properties: map[string]*schema.JSONSchema{"id": str}},
		},
		Required: []string{"name"},
	}}))
	for _, d := range []struct {
		body   string
		labels []string
	}{
		{`{"name":"apple","qty":5,"tags":["red","fruit"],"owner":{"id":"u1"}}`, []string{"urgent", "ops"}}, // I-01
		{`{"name":"banana","qty":12,"tags":["yellow","fruit"]}`, []string{"ops"}},                          // I-02
		{`{"name":"carrot","qty":0,"tags":["orange"],"owner":{"id":"u2"},"note":null}`, nil},               // I-03
	} {
		must[store.Record](t)(e.Insert(ctx, "item", json.RawMessage(d.body), d.labels...))
	}
	must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "item", IDStrategy: itemIDs, Schema: &schema.JSONSchema{
		Type: "object",
		Properties: map[string]*schema.JSONSchema{
			"name": str, "qty": num, "status": str, "tags": arr,
		},
		Required: []string{"name", "status"},
	}}))
	must[store.Record](t)(e.Insert(ctx, "item", json.RawMessage(`{"name":"date","qty":7.5,"status":"open"}`), "urgent"))            // I-04
	must[store.Record](t)(e.Insert(ctx, "item", json.RawMessage(`{"name":"eggplant","qty":3,"status":"closed","tags":["fruit"]}`))) // I-05
}

func testFilters(t *testing.T, s store.Store) {
	ctx := context.Background()
	e := Engine(t, s)
	filterFixture(t, e)
	cases := []struct {
		name string
		f    query.Filter
		want string // sorted IDs
	}{
		{"eq string", query.Eq("/name", "banana"), "I-02"},
		{"eq dotted nested", query.Eq("owner.id", "u2"), "I-03"},
		{"eq number", query.Eq("/qty", 5), "I-01"},
		{"eq decimal", query.Eq("/qty", 7.5), "I-04"},
		{"eq null", query.Eq("/note", nil), "I-03"},
		{"ne skips missing", query.Ne("/status", "open"), "I-05"},
		{"not eq includes missing", query.Not(query.Eq("/status", "open")), "I-01,I-02,I-03,I-05"},
		{"gt number", query.Gt("/qty", 5), "I-02,I-04"},
		{"gte number", query.Gte("/qty", 5), "I-01,I-02,I-04"},
		{"lt number", query.Lt("/qty", 3), "I-03"},
		{"lte number", query.Lte("/qty", 3), "I-03,I-05"},
		{"gt string", query.Gt("/name", "carrot"), "I-04,I-05"},
		{"type mismatch never matches", query.Gt("/name", 1), ""},
		{"in", query.In("/name", "apple", "date", "zzz"), "I-01,I-04"},
		{"contains array", query.Contains("/tags", "fruit"), "I-01,I-02,I-05"},
		{"contains substring", query.Contains("/name", "an"), "I-02,I-05"},
		{"prefix", query.Prefix("/name", "ca"), "I-03"},
		{"exists", query.Exists("/status"), "I-04,I-05"},
		{"exists null", query.Exists("/note"), "I-03"},
		{"not exists", query.Not(query.Exists("/owner")), "I-02,I-04,I-05"},
		{"and", query.And(query.Contains("/tags", "fruit"), query.Gt("/qty", 4)), "I-01,I-02"},
		{"or", query.Or(query.Eq("/name", "apple"), query.Eq("/status", "closed")), "I-01,I-05"},
		{"meta id", query.Eq("$id", "I-03"), "I-03"},
		{"meta id prefix", query.Prefix("$id", "I-0"), "I-01,I-02,I-03,I-04,I-05"},
		{"meta version", query.Eq("$version", 2), "I-04,I-05"},
		{"meta createdAt", query.Gt("$createdAt", "2026-01-02T03:04:05.005Z"), "I-04,I-05"},
		{"meta updatedAt", query.Lte("$updatedAt", "2026-01-02T03:04:05.003Z"), "I-01,I-02"},
		{"labels contains", query.Contains("$labels", "urgent"), "I-01,I-04"},
		{"labels all of", query.And(query.Contains("$labels", "urgent"), query.Contains("$labels", "ops")), "I-01"},
		{"labels any of", query.Or(query.Contains("$labels", "urgent"), query.Contains("$labels", "ops")), "I-01,I-02,I-04"},
		{"labels none of", query.Not(query.Contains("$labels", "ops")), "I-03,I-04,I-05"},
		{"labels empty", query.Eq("$labels", []string{}), "I-03,I-05"},
		{"labels exact set", query.Eq("$labels", []string{"ops", "urgent"}), "I-01"},
		{"labels always exist", query.Exists("$labels"), "I-01,I-02,I-03,I-04,I-05"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			p := must[query.Page[store.Record]](t)(e.List(ctx, "item", query.Query{Filter: &f, Sort: []query.SortField{{Field: "$id"}}}))
			if got := IDs(p.Items); got != tc.want {
				t.Fatalf("want [%s], got [%s]", tc.want, got)
			}
			if p.Total != len(p.Items) {
				t.Fatalf("page total %d, want %d", p.Total, len(p.Items))
			}
			n := must[int](t)(s.CountRecords(ctx, "item", normalized(t, query.Query{Filter: &f}).Filter))
			if n != len(p.Items) {
				t.Fatalf("CountRecords = %d, want %d", n, len(p.Items))
			}
		})
	}
}
