package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/store"
)

type tenantKey struct{}

func TestTenant(t *testing.T) {
	fromCtx := store.TenantFunc(func(ctx context.Context) []string { v, _ := ctx.Value(tenantKey{}).([]string); return v })
	with := func(values ...string) context.Context {
		return context.WithValue(context.Background(), tenantKey{}, values)
	}
	cases := []struct {
		name string
		fn   store.TenantFunc
		ctx  context.Context
		want string
		err  error
	}{
		{"disabled", nil, context.Background(), "[]", nil},
		{"disabled ignores the context", nil, with("acme"), "[]", nil},
		{"one field", fromCtx, with("acme"), "[acme]", nil},
		{"several fields", fromCtx, with("acme", "alice"), "[acme alice]", nil},
		{"no tenant", fromCtx, context.Background(), "[]", store.ErrNoTenant},
		{"empty tenant", fromCtx, with(), "[]", store.ErrNoTenant},
		{"partial tenant", fromCtx, with("acme", ""), "[]", store.ErrNoTenant},
	}
	for _, tc := range cases {
		got, err := store.Tenant(tc.ctx, tc.fn)
		if fmt.Sprint(got) != tc.want || !errors.Is(err, tc.err) || (tc.err == nil && err != nil) {
			t.Errorf("%s: got %q %v", tc.name, got, err)
		}
	}
	if errors.Is(store.ErrNoTenant, errs.ErrInvalid) {
		t.Error("a missing tenant is not caller input")
	}
}

func TestTenantKey(t *testing.T) {
	cases := [][]string{{}, {"acme"}, {"acme", "alice"}, {"a/b", "c"}, {"a", "b/c"}, {"%2F", "x y", "été", "{}:"}}
	seen := map[string]bool{}
	for _, values := range cases {
		key := store.TenantKey(values)
		if seen[key] {
			t.Fatalf("%q: key %q is not unique", values, key)
		}
		seen[key] = true
		back, err := store.ParseTenantKey(key)
		if err != nil || fmt.Sprint(back) != fmt.Sprint(values) || len(back) != len(values) {
			t.Fatalf("%q: round trip gave %q, %v", values, back, err)
		}
	}
	if _, err := store.ParseTenantKey("%zz"); err == nil {
		t.Fatal("a malformed key must be rejected")
	}
}

func TestRecordNormalized(t *testing.T) {
	rome := time.FixedZone("CET", 3600)
	r := store.Record{
		Data:      json.RawMessage(`{"a":1}`),
		CreatedAt: time.Date(2026, 1, 2, 4, 0, 0, 0, rome),
		UpdatedAt: time.Date(2026, 1, 2, 5, 0, 0, 0, rome),
	}
	n := r.Normalized()
	if n.CreatedAt.Location() != time.UTC || n.UpdatedAt.Location() != time.UTC || !n.CreatedAt.Equal(r.CreatedAt) {
		t.Fatalf("times: %v %v", n.CreatedAt, n.UpdatedAt)
	}
	if n.Labels == nil || len(n.Labels) != 0 {
		t.Fatalf("labels: %#v", n.Labels)
	}
	r.Labels = []string{"x"}
	n = r.Normalized()
	n.Labels[0] = "changed"
	n.Data[2] = 'b'
	if r.Labels[0] != "x" || string(r.Data) != `{"a":1}` {
		t.Fatal("Normalized must not share memory with the original")
	}
}

// TestPaginateLeavesInputAlone checks the promise that lets stores share
// docs between concurrent lists: the slice keeps its order and the records
// their data.
func TestPaginateLeavesInputAlone(t *testing.T) {
	docs := []*store.Doc{}
	for _, id := range []string{"c", "a", "b"} {
		d, err := store.NewDoc(store.Record{ID: id, Data: json.RawMessage(`{"n":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, d)
	}
	q, err := query.Query{Sort: query.Sorts{{Field: "$id"}}, Limit: 2}.Normalize(10, 10)
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.Paginate(docs, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "a" || page.Items[1].ID != "b" || page.NextCursor == "" {
		t.Fatalf("page: %+v", page)
	}
	for i, id := range []string{"c", "a", "b"} {
		if docs[i].Record.ID != id || string(docs[i].Record.Data) != `{"n":1}` {
			t.Fatalf("input changed at %d: %+v", i, docs[i].Record)
		}
	}
}
