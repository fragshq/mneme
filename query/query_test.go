package query_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/query"
)

func TestFilterJSONRoundTrip(t *testing.T) {
	f := query.And(query.Eq("/status", "open"), query.Not(query.Exists("closedAt")), query.In("/n", 1, 2))
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	back := query.Filter{}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	n, err := back.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if n.Filters[1].Filters[0].Field != "/closedAt" {
		t.Fatalf("dotted field not normalized: %+v", n.Filters[1])
	}
	want := `{"op":"and","filters":[{"op":"eq","field":"/status","value":"open"},{"op":"not","filters":[{"op":"exists","field":"/closedAt"}]},{"op":"in","field":"/n","value":[1,2]}]}`
	got, _ := json.Marshal(n)
	if string(got) != want {
		t.Fatalf("\nwant %s\ngot  %s", want, got)
	}
}

func TestNormalizeRejects(t *testing.T) {
	bad := []query.Filter{
		{Op: "bogus", Field: "/a"},
		{Op: query.OpEq, Field: "/a"},
		{Op: query.OpEq, Value: json.RawMessage(`1`)},
		{Op: query.OpEq, Field: "$nope", Value: json.RawMessage(`1`)},
		{Op: query.OpEq, Field: "a..b", Value: json.RawMessage(`1`)},
		query.In("/a"),
		{Op: query.OpIn, Field: "/a", Value: json.RawMessage(`1`)},
		query.Gt("/a", true),
		{Op: query.OpPrefix, Field: "/a", Value: json.RawMessage(`1`)},
		{Op: query.OpExists, Field: "/a", Value: json.RawMessage(`true`)},
		{Op: query.OpAnd},
		{Op: query.OpNot, Filters: []query.Filter{query.Exists("/a"), query.Exists("/b")}},
		query.Gt("$createdAt", "yesterday"),
		query.In("$updatedAt", "2026-01-02T03:04:05Z", "yesterday"),
		query.In("$createdAt", 5),
	}
	bad[5].Value = json.RawMessage(`null`)
	for i, f := range bad {
		if _, err := f.Normalize(); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("case %d (%+v): want ErrInvalid, got %v", i, f, err)
		}
	}
}

func TestQueryNormalizeSort(t *testing.T) {
	q, err := query.Query{}.Normalize(10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Sort) != 2 || q.Sort[0].Field != "$createdAt" || q.Sort[1].Field != "$id" || q.Limit != 10 {
		t.Fatalf("defaults: %+v", q)
	}
	q, _ = query.Query{Sort: []query.SortField{{Field: "a.b", Desc: true}, {Field: "$id"}, {Field: "/x"}}, Limit: 500}.Normalize(10, 100)
	if len(q.Sort) != 2 || q.Sort[0].Field != "/a/b" || q.Limit != 100 {
		t.Fatalf("explicit: %+v", q)
	}
	if _, err := (query.Query{Sort: []query.SortField{{Field: "/a"}, {Field: "a"}}}).Normalize(10, 100); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("duplicate sort field: want ErrInvalid, got %v", err)
	}
}

func TestCompareValuesTotalOrder(t *testing.T) {
	ordered := []any{nil, "a", "b", 1.0, 2.0, false, true, []any{1.0}, map[string]any{"a": 1.0}}
	for i := range ordered {
		for j := range ordered {
			c := query.CompareValues(ordered[i], true, ordered[j], true)
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if c != want {
				t.Errorf("compare(%v,%v)=%d want %d", ordered[i], ordered[j], c, want)
			}
		}
		if query.CompareValues(ordered[i], true, nil, false) != -1 {
			t.Errorf("missing must sort after %v", ordered[i])
		}
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	if _, err := query.DecodeCursor("!!!", nil); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestSortsJSON(t *testing.T) {
	cases := []struct {
		raw  string
		want query.Sorts
		ok   bool
	}{
		{`"total"`, query.Sorts{{Field: "total"}}, true},
		{`"-total"`, query.Sorts{{Field: "total", Desc: true}}, true},
		{`{"field":"total","desc":true}`, query.Sorts{{Field: "total", Desc: true}}, true},
		{`["status", "-total", {"field":"$id"}]`, query.Sorts{{Field: "status"}, {Field: "total", Desc: true}, {Field: "$id"}}, true},
		{`[]`, query.Sorts{}, true},
		{`null`, nil, true},
		{`"-"`, nil, false},
		{`""`, nil, false},
		{`3`, nil, false},
		{`{"field":"x","direction":"desc"}`, nil, false},
		{`[{"field":"x"}, 3]`, nil, false},
	}
	for _, tc := range cases {
		got := query.Sorts{}
		err := json.Unmarshal([]byte(tc.raw), &got)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.raw, err)
			continue
		}
		if err != nil {
			if !errors.Is(err, errs.ErrInvalid) {
				t.Errorf("%s: want ErrInvalid, got %v", tc.raw, err)
			}
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.want) || (got == nil) != (tc.want == nil) {
			t.Errorf("%s: got %#v, want %#v", tc.raw, got, tc.want)
		}
	}
	// a whole query accepts the lenient form
	q := query.Query{}
	if err := json.Unmarshal([]byte(`{"sort":"-total","limit":3}`), &q); err != nil || len(q.Sort) != 1 || !q.Sort[0].Desc {
		t.Fatalf("%+v %v", q, err)
	}
}

func TestParseFilter(t *testing.T) {
	for _, raw := range []string{"", "  ", "null"} {
		if f, err := query.ParseFilter([]byte(raw)); f != nil || err != nil {
			t.Fatalf("%q: want no filter, got %+v, %v", raw, f, err)
		}
	}
	f, err := query.ParseFilter([]byte(`{"op":"eq","field":"status","value":"open"}`))
	if err != nil || f.Op != query.OpEq || f.Field != "status" {
		t.Fatalf("got %+v, %v", f, err)
	}
	for _, raw := range []string{`{"op":"eq","bogus":1}`, `[1]`, `{"op":"eq"} {}`} {
		if _, err := query.ParseFilter([]byte(raw)); !errors.Is(err, errs.ErrInvalid) {
			t.Fatalf("%s: want ErrInvalid, got %v", raw, err)
		}
	}
}
