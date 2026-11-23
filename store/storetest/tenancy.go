package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"mneme"
	"mneme/idgen"
	"mneme/jsonx"
	"mneme/query"
	"mneme/schema"
	"mneme/store"
)

// TenancyFactory returns a fresh, empty store with tenancy enabled, scoping
// every operation to the tenant that tenant(ctx) returns. fields names the
// tenancy fields the suite sends, in order: a store that keeps them apart
// (say, one column each) declares them; one that encodes the whole tenant
// as one key may ignore them. Pass tenant to the store's tenancy option as
// it is.
type TenancyFactory func(t *testing.T, tenant store.TenantFunc, fields []string) store.Store

type tenantKey struct{}

// TenantOf is the TenantFunc RunTenancy hands to TenancyFactory: it returns
// the tenant set by WithTenant, or nil.
func TenantOf(ctx context.Context) []string {
	v, _ := ctx.Value(tenantKey{}).([]string)
	return v
}

// WithTenant returns a context naming the tenant made of values, for
// TenantOf.
func WithTenant(values ...string) context.Context {
	return context.WithValue(context.Background(), tenantKey{}, values)
}

// tenancyShapes are the tenancy fields RunTenancy tests, each with three
// tenants for the isolation test. With two fields the tenants differ in one
// field only. similar holds two tenants that a naive encoding (joining or
// unescaping the values) would confuse.
var tenancyShapes = []struct {
	name    string
	fields  []string
	tenants [][]string
	similar [][]string
}{
	{"OneField", []string{"tenant"},
		[][]string{{"acme"}, {"globex"}, {"initech"}},
		[][]string{{"a/b"}, {"a%2Fb"}}},
	{"TwoFields", []string{"org_id", "user_id"},
		[][]string{{"acme", "alice"}, {"acme", "bob"}, {"globex", "alice"}},
		[][]string{{"a/b", "c"}, {"a", "b/c"}}},
}

// RunTenancy checks a store's tenancy, for stores that support it, with one
// tenancy field and with two:
//   - the whole Run suite passes with every operation scoped to one tenant;
//   - tenants are isolated from each other (definitions, records,
//     sequences), even when they differ in one field only;
//   - an operation whose context names no tenant, or a partial one, fails
//     with store.ErrNoTenant on every method, changing nothing.
func RunTenancy(t *testing.T, newStore TenancyFactory) {
	for _, shape := range tenancyShapes {
		t.Run(shape.name, func(t *testing.T) {
			suite := shape.tenants[0]
			t.Run("Conformance", func(t *testing.T) {
				Run(t, func(t *testing.T) store.Store {
					return newStore(t, func(context.Context) []string { return suite }, shape.fields)
				})
			})
			t.Run("Isolation", func(t *testing.T) {
				testTenantIsolation(t, newStore(t, TenantOf, shape.fields), shape.tenants)
			})
			t.Run("SimilarTenants", func(t *testing.T) {
				testTenantIsolation(t, newStore(t, TenantOf, shape.fields), [][]string{shape.similar[0], shape.similar[1], shape.tenants[2]})
			})
			t.Run("MissingTenant", func(t *testing.T) {
				testMissingTenant(t, newStore(t, TenantOf, shape.fields), shape.tenants[0])
			})
		})
	}
}

// testTenantIsolation checks that three tenants share nothing.
func testTenantIsolation(t *testing.T, s store.Store, tenants [][]string) {
	e := Engine(t, s)
	acme, globex, other := WithTenant(tenants[0]...), WithTenant(tenants[1]...), WithTenant(tenants[2]...)
	ids := idgen.NewSpec(idgen.Fixed("R-"), idgen.Sequence(idgen.SequenceOptions{Name: "r"}))
	for _, c := range []struct {
		ctx      context.Context
		required string
	}{{acme, "x"}, {globex, "y"}} {
		if _, err := e.Define(c.ctx, schema.Definition{Name: "risk", IDStrategy: ids,
			Schema: &schema.JSONSchema{Type: "object", Required: []string{c.required}}}); err != nil {
			t.Fatal(err)
		}
	}
	ra := must[store.Record](t)(e.Insert(acme, "risk", json.RawMessage(`{"x":1}`), "acme-only"))
	if _, err := e.Insert(globex, "risk", json.RawMessage(`{"x":1}`)); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("globex must validate against its own schema, got %v", err)
	}
	rb := must[store.Record](t)(e.Insert(globex, "risk", json.RawMessage(`{"y":1}`)))
	if ra.ID != "R-1" || rb.ID != "R-1" {
		t.Fatalf("sequences must be per tenant: %s, %s", ra.ID, rb.ID)
	}
	if got, err := e.Get(globex, "risk", "R-1"); err != nil || !jsonx.Equal(got.Data, json.RawMessage(`{"y":1}`)) {
		t.Fatalf("globex reads its own R-1: %s %v", got.Data, err)
	}
	if p, err := e.List(acme, "risk", query.Query{}); err != nil || len(p.Items) != 1 || !jsonx.Equal(p.Items[0].Data, json.RawMessage(`{"x":1}`)) {
		t.Fatalf("acme lists only its records: %+v %v", p.Items, err)
	}
	if n, err := e.Count(globex, "risk", nil); err != nil || n != 1 {
		t.Fatalf("globex counts only its own record: %d %v", n, err)
	}
	if ls, err := e.ListLabels(globex, "risk", nil); err != nil || len(ls) != 0 {
		t.Fatalf("globex must not see acme's labels: %+v %v", ls, err)
	}
	if ls, err := e.ListLabels(acme, "risk", nil); err != nil || len(ls) != 1 || ls[0].Label != "acme-only" {
		t.Fatalf("acme lists its own labels: %+v %v", ls, err)
	}
	if vs, err := e.ListVersions(acme, "risk"); err != nil || len(vs) != 1 {
		t.Fatalf("acme has one version of its own: %d %v", len(vs), err)
	}
	if err := e.Delete(acme, "risk", "R-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Get(globex, "risk", "R-1"); err != nil {
		t.Fatalf("acme's delete must not touch globex: %v", err)
	}
	if err := e.DeleteRecordType(acme, "risk"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Get(globex, "risk", "R-1"); err != nil {
		t.Fatalf("acme deleting its record type must not touch globex's records: %v", err)
	}
	if r, err := e.Insert(globex, "risk", json.RawMessage(`{"y":2}`)); err != nil || r.ID != "R-2" {
		t.Fatalf("nor globex's definitions and sequences: %+v %v", r, err)
	}
	if _, err := e.GetRecordType(other, "risk", 0); !errors.Is(err, mneme.ErrNotFound) {
		t.Fatalf("a third tenant has no definitions: %v", err)
	}
	if ds, err := e.ListRecordTypes(other); err != nil || len(ds) != 0 {
		t.Fatalf("a third tenant lists no record types: %d %v", len(ds), err)
	}
}

// CallsWithoutTenant stores record type "risk" (v1) and its record "R-1"
// for the tenant made of owner, then returns one call per store method,
// each made with a context that names no tenant. A store with tenancy must
// fail every call with store.ErrNoTenant and change nothing. RunTenancy
// checks both through the store; a store can run the calls again to check
// its backend directly.
func CallsWithoutTenant(t *testing.T, s store.Store, owner ...string) map[string]func() error {
	t.Helper()
	return callsAs(t, s, WithTenant(owner...), context.Background())
}

// callsAs stores owner's record type "risk" and record "R-1", then returns
// one call per store method, made with ctx.
func callsAs(t *testing.T, s store.Store, owner context.Context, ctx context.Context) map[string]func() error {
	t.Helper()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	def := schema.Definition{Name: "risk", Version: 1, Schema: &schema.JSONSchema{Type: "object"}, CreatedAt: at}
	if err := s.PutDefinition(owner, def); err != nil {
		t.Fatal(err)
	}
	rec := store.Record{RecordType: "risk", ID: "R-1", SchemaVersion: 1, Data: json.RawMessage(`{}`), CreatedAt: at, UpdatedAt: at, Labels: []string{}}
	if err := s.InsertRecord(owner, rec); err != nil {
		t.Fatal(err)
	}
	q := normalized(t, query.Query{})
	other := def
	other.Name = "other"
	newRec := rec
	newRec.ID = "R-2"
	return map[string]func() error{
		"PutDefinition":     func() error { return s.PutDefinition(ctx, other) },
		"GetDefinition":     func() error { _, err := s.GetDefinition(ctx, "risk", 0); return err },
		"ListDefinitions":   func() error { _, err := s.ListDefinitions(ctx); return err },
		"ListVersions":      func() error { _, err := s.ListVersions(ctx, "risk"); return err },
		"InsertRecord":      func() error { return s.InsertRecord(ctx, newRec) },
		"GetRecord":         func() error { _, err := s.GetRecord(ctx, "risk", "R-1"); return err },
		"UpdateRecord":      func() error { return s.UpdateRecord(ctx, rec) },
		"DeleteRecord":      func() error { return s.DeleteRecord(ctx, "risk", "R-1") },
		"ListRecords":       func() error { _, err := s.ListRecords(ctx, "risk", q); return err },
		"ListLabels":        func() error { _, err := s.ListLabels(ctx, "risk", nil); return err },
		"CountRecords":      func() error { _, err := s.CountRecords(ctx, "risk", nil); return err },
		"DeleteRecords":     func() error { return s.DeleteRecords(ctx, "risk") },
		"DeleteDefinitions": func() error { return s.DeleteDefinitions(ctx, "risk") },
		"DeleteSequences":   func() error { return s.DeleteSequences(ctx, "risk") },
		"Next":              func() error { _, err := s.Next(ctx, "risk", "seq", ""); return err },
	}
}

// testMissingTenant rejects every call made without a tenant, with an empty
// one, or with a partial one (a blank field), and checks that the owner's
// data survives them.
func testMissingTenant(t *testing.T, s store.Store, owner []string) {
	acme := WithTenant(owner...)
	partial := append(append([]string{}, owner[:len(owner)-1]...), "")
	for _, c := range []struct {
		name string
		ctx  context.Context
	}{{"NoTenant", context.Background()}, {"EmptyTenant", WithTenant()}, {"PartialTenant", WithTenant(partial...)}} {
		t.Run(c.name, func(t *testing.T) {
			for name, call := range callsAs(t, s, acme, c.ctx) {
				if err := call(); !errors.Is(err, store.ErrNoTenant) {
					t.Errorf("%s: want store.ErrNoTenant, got %v", name, err)
				}
			}
			if ds, err := s.ListDefinitions(acme); err != nil || len(ds) != 1 {
				t.Fatalf("the owner's definitions: %d, %v", len(ds), err)
			}
			if _, err := s.GetRecord(acme, "risk", "R-1"); err != nil {
				t.Fatalf("the owner's record must survive the rejected delete: %v", err)
			}
			if _, err := Engine(t, s).Insert(c.ctx, "risk", json.RawMessage(`{}`)); !errors.Is(err, store.ErrNoTenant) {
				t.Fatalf("engine insert: want store.ErrNoTenant, got %v", err)
			}
			// Clear the owner's data: the next case stores it again.
			if err := s.DeleteRecords(acme, "risk"); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteDefinitions(acme, "risk"); err != nil {
				t.Fatal(err)
			}
		})
	}
	if n, err := s.Next(acme, "risk", "seq", ""); err != nil || n != 1 {
		t.Fatalf("rejected Next calls must not consume the owner's sequence: %d, %v", n, err)
	}
}
