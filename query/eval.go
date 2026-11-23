package query

import (
	"cmp"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Doc is a record as seen by the reference evaluator. Lookup receives a
// normalized field and returns the value decoded by encoding/json
// (map[string]any, []any, string, float64, bool, nil). Meta fields return
// string ($id), float64 ($version), time.Time ($createdAt, $updatedAt) and
// []any of strings ($labels).
type Doc interface {
	Lookup(field string) (any, bool)
}

// Match evaluates a normalized filter against d with the reference semantics.
// A nil filter matches everything.
func Match(f *Filter, d Doc) bool {
	if f == nil {
		return true
	}
	switch f.Op {
	case OpAnd:
		for i := range f.Filters {
			if !Match(&f.Filters[i], d) {
				return false
			}
		}
		return true
	case OpOr:
		for i := range f.Filters {
			if Match(&f.Filters[i], d) {
				return true
			}
		}
		return false
	case OpNot:
		return !Match(&f.Filters[0], d)
	}
	got, ok := d.Lookup(f.Field)
	if !ok {
		return false
	}
	if f.Op == OpExists {
		return true
	}
	want := any(nil)
	if err := json.Unmarshal(f.Value, &want); err != nil {
		return false
	}
	switch f.Op {
	case OpEq:
		return equal(got, want)
	case OpNe:
		return !equal(got, want)
	case OpIn:
		for _, w := range want.([]any) {
			if equal(got, w) {
				return true
			}
		}
		return false
	case OpGt, OpGte, OpLt, OpLte:
		c, ok := compareScalar(got, want)
		if !ok {
			return false
		}
		switch f.Op {
		case OpGt:
			return c > 0
		case OpGte:
			return c >= 0
		case OpLt:
			return c < 0
		default:
			return c <= 0
		}
	case OpContains:
		switch g := got.(type) {
		case []any:
			for _, e := range g {
				if equal(e, want) {
					return true
				}
			}
		case string:
			if w, ok := want.(string); ok {
				return strings.Contains(g, w)
			}
		}
		return false
	case OpPrefix:
		g, ok1 := got.(string)
		w, ok2 := want.(string)
		return ok1 && ok2 && strings.HasPrefix(g, w)
	}
	return false
}

func asTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		t, err := time.Parse(time.RFC3339Nano, x)
		return t, err == nil
	}
	return time.Time{}, false
}

func equal(a any, b any) bool {
	if ta, ok := a.(time.Time); ok {
		tb, ok := asTime(b)
		return ok && ta.Equal(tb)
	}
	return reflect.DeepEqual(a, b)
}

// compareScalar compares two values of the same comparable kind.
func compareScalar(a any, b any) (int, bool) {
	if ta, ok := a.(time.Time); ok {
		tb, ok := asTime(b)
		if !ok {
			return 0, false
		}
		return ta.Compare(tb), true
	}
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)
		return cmp.Compare(x, y), ok
	case string:
		y, ok := b.(string)
		return strings.Compare(x, y), ok
	}
	return 0, false
}

// Type ranks for the total sort order; missing values sort after everything
// (last when ascending, first when descending), like SQL NULLs by default.
func rank(v any, present bool) int {
	if !present {
		return 6
	}
	switch v.(type) {
	case nil:
		return 0
	case string, time.Time:
		return 1
	case float64:
		return 2
	case bool:
		return 3
	case []any:
		return 4
	}
	return 5
}

// CompareValues is the total order used for sorting: values are ordered by
// type (null < string < number < bool < array < object < missing), then by
// value within the type.
func CompareValues(a any, aok bool, b any, bok bool) int {
	ra, rb := rank(a, aok), rank(b, bok)
	if ra != rb {
		return cmp.Compare(ra, rb)
	}
	if c, ok := compareScalar(a, b); ok {
		return c
	}
	switch x := a.(type) {
	case bool:
		switch y := b.(bool); {
		case x == y:
			return 0
		case x:
			return 1
		}
		return -1
	case []any, map[string]any:
		ja, _ := json.Marshal(x)
		jb, _ := json.Marshal(b)
		return strings.Compare(string(ja), string(jb))
	}
	return 0
}

// compare orders two docs by the (normalized) sort fields.
func compare(sorts []SortField, a Doc, b Doc) int {
	for _, s := range sorts {
		av, aok := a.Lookup(s.Field)
		bv, bok := b.Lookup(s.Field)
		c := CompareValues(av, aok, bv, bok)
		if s.Desc {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// Sort sorts docs in place by the sort fields.
func Sort[D Doc](sorts []SortField, docs []D) {
	slices.SortStableFunc(docs, func(a D, b D) int { return compare(sorts, a, b) })
}
