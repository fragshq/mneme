package idgen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
)

type counter map[string]int64

func (c counter) Next(_ context.Context, recordType string, name string, scope string) (int64, error) {
	k := recordType + "|" + name + "|" + scope
	c[k]++
	return c[k], nil
}

func compile(t *testing.T, spec string) *idgen.Generator {
	t.Helper()
	s := idgen.Spec{}
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		t.Fatal(err)
	}
	return compileSpec(t, s)
}

func gen(t *testing.T, g *idgen.Generator, seq idgen.SequenceProvider, data string, now time.Time) string {
	t.Helper()
	doc := any(nil)
	if data != "" {
		_ = json.Unmarshal([]byte(data), &doc)
	}
	id, err := g.Generate(context.Background(), idgen.GenContext{RecordType: "e", Data: doc, Now: now, Sequences: seq})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestComposedSegments(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	seq := counter{}
	g := compile(t, `{"segments":[{"fixed":"RID-"},{"date":"2006"},{"fixed":"-"},{"field":"/region"},{"fixed":"-"},{"sequence":{"name":"rid","pad":4}}]}`)
	if g.Deterministic() || !g.NeedsSequences() {
		t.Fatal("sequence generator must be non-deterministic and need sequences")
	}
	if got := gen(t, g, seq, `{"region":"EU"}`, now); got != "RID-2026-EU-0001" {
		t.Fatalf("got %s", got)
	}
	if got := gen(t, g, seq, `{"region":"US"}`, now); got != "RID-2026-US-0002" {
		t.Fatalf("got %s", got)
	}
}

func TestSequenceScopePrefixResetsPerPrefix(t *testing.T) {
	seq := counter{}
	g := compile(t, `{"segments":[{"date":"2006"},{"fixed":"/"},{"sequence":{"name":"n","scope":"prefix","start":100,"step":10}}]}`)
	y26 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	y27 := y26.AddDate(1, 0, 0)
	for _, c := range []struct {
		now  time.Time
		want string
	}{{y26, "2026/100"}, {y26, "2026/110"}, {y27, "2027/100"}, {y26, "2026/120"}} {
		if got := gen(t, g, seq, "", c.now); got != c.want {
			t.Fatalf("want %s, got %s", c.want, got)
		}
	}
}

func TestDefaultIsUUIDv7(t *testing.T) {
	g, err := idgen.Compile(idgen.Spec{})
	if err != nil {
		t.Fatal(err)
	}
	id := gen(t, g, nil, "", time.Now())
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("not a UUIDv7: %s", id)
	}
	v4 := compile(t, `{"segments":[{"uuid":"v4"}]}`)
	if id := gen(t, v4, nil, "", time.Now()); id[14] != '4' {
		t.Fatalf("not a UUIDv4: %s", id)
	}
}

func TestRandomAlphabet(t *testing.T) {
	g := compile(t, `{"segments":[{"fixed":"K"},{"random":{"length":12,"alphabet":"a-c9"}}]}`)
	id := gen(t, g, nil, "", time.Now())
	if !regexp.MustCompile(`^K[abc9]{12}$`).MatchString(id) {
		t.Fatalf("got %s", id)
	}
}

func TestFieldDefaultAndMissing(t *testing.T) {
	g := compile(t, `{"segments":[{"field":{"pointer":"/a/b","default":"X"}}]}`)
	if got := gen(t, g, nil, `{"a":{"b":7}}`, time.Now()); got != "7" {
		t.Fatalf("got %s", got)
	}
	if got := gen(t, g, nil, `{}`, time.Now()); got != "X" {
		t.Fatalf("got %s", got)
	}
	strict := compile(t, `{"segments":[{"field":"/a"}]}`)
	if !strict.Deterministic() {
		t.Fatal("field-only generator should be deterministic")
	}
	_, err := strict.Generate(context.Background(), idgen.GenContext{Data: map[string]any{}})
	if !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestSequenceWithoutProvider(t *testing.T) {
	g := compile(t, `{"segments":[{"sequence":"s"}]}`)
	if _, err := g.Generate(context.Background(), idgen.GenContext{}); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestInvalidSpecs(t *testing.T) {
	for _, spec := range []string{
		`{"segments":[{"bogus":1}]}`,
		`{"segments":[{"fixed":""}]}`,
		`{"segments":[{"fixed":3}]}`,
		`{"segments":[{"sequence":{"pad":99}}]}`,
		`{"segments":[{"sequence":{"step":0}}]}`,
		`{"segments":[{"sequence":{"scope":"year"}}]}`,
		`{"segments":[{"sequence":{"nam":"typo"}}]}`,
		`{"segments":[{"uuid":"v1"}]}`,
		`{"segments":[{"random":{"length":0}}]}`,
		`{"segments":[{"random":{"alphabet":"z-a"}}]}`,
		`{"segments":[{"random":{"alphabet":"a"}}]}`,
		`{"segments":[{"field":""}]}`,
		`{"segments":[{"field":"nope"}]}`,
		`{"segments":[{"uuid":{"version":"v7"}}]}`,
		`{"segments":[{"date":{"layout":"2006"}}]}`,
		`{"segments":[{"random":"x"}]}`,
	} {
		s := idgen.Spec{}
		if err := json.Unmarshal([]byte(spec), &s); err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if _, err := idgen.Compile(s); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", spec, err)
		}
	}
	s := idgen.Spec{}
	if err := json.Unmarshal([]byte(`{"segments":[{"fixed":"a","uuid":"v4"}]}`), &s); err == nil {
		t.Error("two keys in one segment must be rejected")
	}
}

func TestSpecRoundTrip(t *testing.T) {
	spec := idgen.Spec{}
	if err := json.Unmarshal([]byte(`{"segments":[{"fixed":"T"},{"fixed":":"},{"sequence":"x"}]}`), &spec); err != nil {
		t.Fatal(err)
	}
	id, err := compileSpec(t, spec).Generate(context.Background(), idgen.GenContext{Sequences: counter{}})
	if err != nil || id != "T:1" {
		t.Fatalf("got %q, %v", id, err)
	}
	out, _ := json.Marshal(spec)
	if string(out) != `{"segments":[{"fixed":"T"},{"fixed":":"},{"sequence":"x"}]}` {
		t.Fatalf("round trip: %s", out)
	}
}

func TestConstructorsMatchJSONForm(t *testing.T) {
	spec := idgen.NewSpec(
		idgen.Fixed("RID-"), idgen.Date("2006"), idgen.Fixed("-"),
		idgen.Sequence(idgen.SequenceOptions{Name: "rid", Pad: 4, Scope: idgen.ScopePrefix}),
		idgen.Fixed("-"), idgen.FieldOr("/region", "XX"), idgen.Fixed("-"), idgen.Random(3, "A-C"),
		idgen.UUIDv4(), idgen.UUIDv7(),
	)
	raw, _ := json.Marshal(spec)
	pretty := `{"segments": [
		{"fixed": "RID-"},
		{"date": "2006"},
		{"fixed": "-"},
		{"sequence": {"name": "rid", "pad": 4, "scope": "prefix"}},
		{"fixed": "-"},
		{"field": {"pointer": "/region", "default": "XX"}},
		{"fixed": "-"},
		{"random": {"length": 3, "alphabet": "A-C"}},
		{"uuid": "v4"},
		{"uuid": "v7"}
	]}`
	compact := bytes.Buffer{}
	if err := json.Compact(&compact, []byte(pretty)); err != nil {
		t.Fatal(err)
	}
	want := compact.String()
	if string(raw) != want {
		t.Fatalf("\nwant %s\ngot  %s", want, raw)
	}
	id := gen(t, compileSpec(t, *spec), counter{}, `{}`, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !regexp.MustCompile(`^RID-2026-0001-XX-[A-C]{3}[0-9a-f-]{36}[0-9a-f-]{36}$`).MatchString(id) {
		t.Fatalf("got %s", id)
	}
}

func compileSpec(t *testing.T, s idgen.Spec) *idgen.Generator {
	t.Helper()
	g, err := idgen.Compile(s)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestSpecRejectsUnknownKeys(t *testing.T) {
	s := idgen.Spec{}
	if err := json.Unmarshal([]byte(`{"segmentz":[]}`), &s); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}
