package mneme_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mneme"
	"mneme/idgen"
	"mneme/query"
	"mneme/schema"
	"mneme/store"
	"mneme/store/memstore"
)

func newEngine(t *testing.T, mutate func(c *mneme.Config)) (*mneme.Engine, *memstore.Store) {
	t.Helper()
	m := memstore.New()
	cfg := mneme.Config{Schemas: m, Records: m, Sequences: m}
	if mutate != nil {
		mutate(&cfg)
	}
	e, err := mneme.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e, m
}

func TestDefineVersioning(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, nil)
	in := schema.Definition{Name: "doc", Schema: obj()}
	if d, err := e.Define(ctx, in); err != nil || d.Version != 1 {
		t.Fatalf("v1: %+v %v", d, err)
	}
	in.Version = 10
	if d, err := e.Define(ctx, in); err != nil || d.Version != 10 {
		t.Fatalf("explicit v10: %+v %v", d, err)
	}
	in.Version = 5
	if _, err := e.Define(ctx, in); !errors.Is(err, mneme.ErrConflict) {
		t.Fatalf("older explicit version: want ErrConflict, got %v", err)
	}
	in.Version = 0
	if d, _ := e.Define(ctx, in); d.Version != 11 {
		t.Fatalf("auto after explicit: got %d", d.Version)
	}
	vs, _ := e.ListVersions(ctx, "doc")
	if len(vs) != 3 {
		t.Fatalf("versions: %d", len(vs))
	}
}

func TestDefineRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, func(c *mneme.Config) { c.Sequences = nil })
	cases := []schema.Definition{
		{Name: "Bad Name", Schema: obj()},
		{Name: "x", Schema: &schema.JSONSchema{Type: "array"}},
		{Name: "x"},
		{Name: "x", Schema: obj(), IDStrategy: idgen.NewSpec(idgen.SegmentSpec{Kind: "nope"})},
		{Name: "x", Schema: obj(), IDStrategy: idgen.NewSpec(idgen.Random(0, ""))},
		// sequence without a SequenceProvider configured
		{Name: "x", Schema: obj(), IDStrategy: idgen.NewSpec(idgen.Sequence(idgen.SequenceOptions{}))},
	}
	for _, in := range cases {
		if _, err := e.Define(ctx, in); !errors.Is(err, mneme.ErrInvalid) {
			t.Errorf("%+v: want ErrInvalid, got %v", in, err)
		}
		if err := e.ValidateDefinition(in); !errors.Is(err, mneme.ErrInvalid) {
			t.Errorf("ValidateDefinition %+v: want ErrInvalid, got %v", in, err)
		}
	}
	if list, _ := e.ListRecordTypes(ctx); len(list) != 0 {
		t.Fatalf("nothing should have been stored, got %d", len(list))
	}
}

func TestCompatibilityHook(t *testing.T) {
	ctx := context.Background()
	calls := 0
	e, _ := newEngine(t, func(c *mneme.Config) {
		c.Compatibility = func(_ context.Context, prev *schema.Definition, next schema.Definition) error {
			calls++
			if prev != nil && next.Title != "keep" {
				return fmt.Errorf("%w: breaking change", mneme.ErrInvalid)
			}
			return nil
		}
	})
	if _, err := e.Define(ctx, schema.Definition{Name: "c", Schema: obj()}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Define(ctx, schema.Definition{Name: "c", Schema: obj()}); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("want rejection, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("hook calls: %d", calls)
	}
}

// conflictOnce makes the first insert fail with ErrConflict, as if another
// writer had taken that ID.
type conflictOnce struct {
	store.RecordStore
	tripped bool
}

func (c *conflictOnce) InsertRecord(ctx context.Context, r store.Record) error {
	if !c.tripped {
		c.tripped = true
		return fmt.Errorf("%w: simulated", store.ErrConflict)
	}
	return c.RecordStore.InsertRecord(ctx, r)
}

func TestInsertRetriesOnConflict(t *testing.T) {
	ctx := context.Background()
	m := memstore.New()
	e, err := mneme.New(mneme.Config{Schemas: m, Records: &conflictOnce{RecordStore: m}, Sequences: m})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.Define(ctx, schema.Definition{Name: "s", Schema: obj(),
		IDStrategy: idgen.NewSpec(idgen.Fixed("S"), idgen.Sequence(idgen.SequenceOptions{Name: "s"}))})
	r, err := e.Insert(ctx, "s", json.RawMessage(`{}`))
	if err != nil || r.ID != "S2" {
		t.Fatalf("want retry to S2, got %q %v", r.ID, err)
	}
	_, _ = e.Define(ctx, schema.Definition{Name: "f", Schema: obj(), IDStrategy: idgen.NewSpec(idgen.Field("/k"))})
	if _, err := e.Insert(ctx, "f", json.RawMessage(`{"k":"same"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Insert(ctx, "f", json.RawMessage(`{"k":"same"}`)); !errors.Is(err, mneme.ErrConflict) {
		t.Fatalf("deterministic id must not be retried: got %v", err)
	}
}

func TestPreviewDoesNotConsumeSequence(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, nil)
	_, _ = e.Define(ctx, schema.Definition{Name: "p", Schema: obj(),
		IDStrategy: idgen.NewSpec(idgen.Fixed("P-"), idgen.Sequence(idgen.SequenceOptions{Pad: 3}))})
	for i := 0; i < 3; i++ {
		if id, err := e.PreviewID(ctx, "p", nil); err != nil || id != "P-001" {
			t.Fatalf("preview: %q %v", id, err)
		}
	}
	if r, _ := e.Insert(ctx, "p", json.RawMessage(`{}`)); r.ID != "P-001" {
		t.Fatalf("first insert after previews: %s", r.ID)
	}
}

func TestStrictFilterFields(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, func(c *mneme.Config) { c.StrictFilterFields = true })
	_, _ = e.Define(ctx, schema.Definition{Name: "s", Schema: obj("a")})
	_, _ = e.Define(ctx, schema.Definition{Name: "s", Schema: obj("b")})
	ok := query.And(query.Eq("a", "x"), query.Eq("/b", "y"), query.Eq("$id", "z"))
	if _, err := e.List(ctx, "s", query.Query{Filter: &ok}); err != nil {
		t.Fatalf("fields from any version must be accepted: %v", err)
	}
	bad := query.Eq("/typo", "x")
	if _, err := e.List(ctx, "s", query.Query{Filter: &bad}); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("unknown field: want ErrInvalid, got %v", err)
	}
	if _, err := e.List(ctx, "s", query.Query{Sort: []query.SortField{{Field: "/typo"}}}); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("unknown sort field: want ErrInvalid, got %v", err)
	}
	if _, err := e.ListLabels(ctx, "s", &ok); err != nil {
		t.Fatalf("ListLabels: fields from any version must be accepted: %v", err)
	}
	if _, err := e.ListLabels(ctx, "s", &bad); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("ListLabels unknown field: want ErrInvalid, got %v", err)
	}
}

func TestListLabelsChecksInput(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, nil)
	if _, err := e.ListLabels(ctx, "ghost", nil); !errors.Is(err, mneme.ErrNotFound) {
		t.Fatalf("unknown record type: want ErrNotFound, got %v", err)
	}
	_, _ = e.Define(ctx, schema.Definition{Name: "s", Schema: obj("a")})
	if got, err := e.ListLabels(ctx, "s", nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no records: want an empty, non-nil slice, got %#v %v", got, err)
	}
	bad := query.Filter{Op: "nope", Field: "a"}
	if _, err := e.ListLabels(ctx, "s", &bad); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("bad filter: want ErrInvalid, got %v", err)
	}
}

func TestInsertStoresCompactJSON(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, nil)
	_, _ = e.Define(ctx, schema.Definition{Name: "c", Schema: obj()})
	r, err := e.Insert(ctx, "c", json.RawMessage(`{ "a" :
	1 }
`))
	if err != nil || string(r.Data) != `{"a":1}` {
		t.Fatalf("got %s %v", r.Data, err)
	}
}

// obj returns an object schema with the given string properties.
func obj(props ...string) *schema.JSONSchema {
	s := &schema.JSONSchema{Type: "object", Properties: map[string]*schema.JSONSchema{}}
	for _, p := range props {
		s.Properties[p] = &schema.JSONSchema{Type: "string"}
	}
	return s
}

func TestDefineCopiesInput(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, nil)
	in := schema.Definition{Name: "m", Schema: obj("a")}
	if _, err := e.Define(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Schema.Properties["a"].Type = "integer" // caller mutates after Define
	if _, err := e.Insert(ctx, "m", json.RawMessage(`{"a":"still a string"}`)); err != nil {
		t.Fatalf("stored definition must not change with the caller's copy: %v", err)
	}
	got, _ := e.GetRecordType(ctx, "m", 0)
	if got.Schema.Properties["a"].Type != "string" {
		t.Fatal("stored schema was mutated")
	}
}

func TestLabelLimits(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, nil)
	if _, err := e.Define(ctx, schema.Definition{Name: "l", Schema: obj()}); err != nil {
		t.Fatal(err)
	}
	many := []string{}
	for i := 0; i <= mneme.MaxLabels; i++ {
		many = append(many, fmt.Sprintf("l%03d", i))
	}
	long := strings.Repeat("é", mneme.MaxLabelLength+1)
	for name, labels := range map[string][]string{"too many": many, "too long": {long}, "empty": {"ok", ""}} {
		if _, err := e.Insert(ctx, "l", json.RawMessage(`{}`), labels...); !errors.Is(err, mneme.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	// Duplicates count once, and the length limit is in characters, not bytes.
	// 63 distinct + 1 duplicate + 1 max-length label = 64 distinct labels.
	ok := append(append([]string{}, many[:mneme.MaxLabels-1]...), many[0], strings.Repeat("é", mneme.MaxLabelLength))
	if _, err := e.Insert(ctx, "l", json.RawMessage(`{}`), ok...); err != nil {
		t.Fatalf("within limits: %v", err)
	}
}

type tenantKey struct{}

// tenantSchemas routes every call to one memstore per tenant found in ctx,
// the way multi-tenant stores scope definitions.
type tenantSchemas map[string]*memstore.Store

func (ts tenantSchemas) pick(ctx context.Context) *memstore.Store {
	return ts[ctx.Value(tenantKey{}).(string)]
}
func (ts tenantSchemas) PutDefinition(ctx context.Context, d schema.Definition) error {
	return ts.pick(ctx).PutDefinition(ctx, d)
}
func (ts tenantSchemas) GetDefinition(ctx context.Context, name string, version int) (schema.Definition, error) {
	return ts.pick(ctx).GetDefinition(ctx, name, version)
}
func (ts tenantSchemas) ListDefinitions(ctx context.Context) ([]schema.Definition, error) {
	return ts.pick(ctx).ListDefinitions(ctx)
}
func (ts tenantSchemas) ListVersions(ctx context.Context, name string) ([]schema.Definition, error) {
	return ts.pick(ctx).ListVersions(ctx, name)
}
func (ts tenantSchemas) DeleteDefinitions(ctx context.Context, name string) error {
	return ts.pick(ctx).DeleteDefinitions(ctx, name)
}

// Two tenants sharing one Engine may define the same name and version with
// different schemas; each must be validated against its own.
func TestCompileCacheIsolatesTenants(t *testing.T) {
	ts := tenantSchemas{"a": memstore.New(), "b": memstore.New()}
	e, err := mneme.New(mneme.Config{Schemas: ts, Records: memstore.New()})
	if err != nil {
		t.Fatal(err)
	}
	ctxA := context.WithValue(context.Background(), tenantKey{}, "a")
	ctxB := context.WithValue(context.Background(), tenantKey{}, "b")
	for _, c := range []struct {
		ctx context.Context
		req string
	}{{ctxA, "x"}, {ctxB, "y"}} {
		if _, err := e.Define(c.ctx, schema.Definition{Name: "doc", Schema: &schema.JSONSchema{Type: "object", Required: []string{c.req}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Insert(ctxA, "doc", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatalf("tenant a: %v", err)
	}
	if _, err := e.Insert(ctxB, "doc", json.RawMessage(`{"x":1}`)); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("tenant b must validate against its own schema: want ErrInvalid, got %v", err)
	}
	if _, err := e.Insert(ctxB, "doc", json.RawMessage(`{"y":1}`)); err != nil {
		t.Fatalf("tenant b: %v", err)
	}
}
