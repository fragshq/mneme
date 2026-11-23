// Package query defines the storage-neutral query model used to list records:
// a JSON-serialisable filter AST, sorting, and opaque keyset cursors.
//
// Field references are either JSON pointers into the record data ("/owner/id"),
// dotted shorthands ("owner.id", converted to pointers by Normalize), or one of
// the reserved meta fields $id, $version, $createdAt, $updatedAt and $labels
// (an array of strings: filter it with contains).
//
// The reference semantics (implemented by Match and Compare, and used by the
// in-memory store) are:
//   - a filter on a field that is missing from a record never matches, except
//     through Not and Exists;
//   - eq/ne/in use JSON equality (numbers compare numerically);
//   - gt/gte/lt/lte compare numbers with numbers and strings with strings
//     (byte order); mismatched types never match. $createdAt and $updatedAt
//     compare as time, with values given as RFC 3339 strings;
//   - contains matches an array holding an element equal to the value, or a
//     string holding the value as a substring;
//   - prefix matches strings starting with the value.
package query

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/jsonptr"
	"github.com/fragshq/mneme/jsonx"
)

// Op is a filter operator.
type Op string

// Filter operators.
const (
	OpEq       Op = "eq"
	OpNe       Op = "ne"
	OpGt       Op = "gt"
	OpGte      Op = "gte"
	OpLt       Op = "lt"
	OpLte      Op = "lte"
	OpIn       Op = "in"
	OpContains Op = "contains"
	OpPrefix   Op = "prefix"
	OpExists   Op = "exists"
	OpAnd      Op = "and"
	OpOr       Op = "or"
	OpNot      Op = "not"
)

// Meta fields addressable in filters and sorts.
const (
	FieldID        = "$id"
	FieldVersion   = "$version"
	FieldCreatedAt = "$createdAt"
	FieldUpdatedAt = "$updatedAt"
	FieldLabels    = "$labels"
)

// Filter is one node of the filter AST. Its JSON form is, for example:
//
//	{"op":"and","filters":[
//	  {"op":"eq","field":"/status","value":"open"},
//	  {"op":"not","filters":[{"op":"exists","field":"/closedAt"}]}]}
type Filter struct {
	Op      Op              `json:"op"`
	Field   string          `json:"field,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Filters []Filter        `json:"filters,omitempty"`
}

func leaf(op Op, field string, v any) Filter {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("query: value for %s %s is not JSON-encodable: %v", op, field, err))
	}
	return Filter{Op: op, Field: field, Value: raw}
}

// Eq matches records whose field equals v.
func Eq(field string, v any) Filter { return leaf(OpEq, field, v) }

// Ne matches records whose field exists and differs from v.
func Ne(field string, v any) Filter { return leaf(OpNe, field, v) }

// Gt matches records whose field is greater than v.
func Gt(field string, v any) Filter { return leaf(OpGt, field, v) }

// Gte matches records whose field is greater than or equal to v.
func Gte(field string, v any) Filter { return leaf(OpGte, field, v) }

// Lt matches records whose field is less than v.
func Lt(field string, v any) Filter { return leaf(OpLt, field, v) }

// Lte matches records whose field is less than or equal to v.
func Lte(field string, v any) Filter { return leaf(OpLte, field, v) }

// In matches records whose field equals any of vs.
func In(field string, vs ...any) Filter { return leaf(OpIn, field, vs) }

// Contains matches arrays holding v, or strings containing v.
func Contains(field string, v any) Filter { return leaf(OpContains, field, v) }

// Prefix matches strings starting with p.
func Prefix(field string, p string) Filter { return leaf(OpPrefix, field, p) }

// Exists matches records where field is present (even if null).
func Exists(field string) Filter { return Filter{Op: OpExists, Field: field} }

// And matches records matching all filters.
func And(fs ...Filter) Filter { return Filter{Op: OpAnd, Filters: fs} }

// Or matches records matching any filter.
func Or(fs ...Filter) Filter { return Filter{Op: OpOr, Filters: fs} }

// Not negates f.
func Not(f Filter) Filter { return Filter{Op: OpNot, Filters: []Filter{f}} }

// NormalizeField converts a field reference to canonical form: a meta field
// or a non-empty JSON pointer.
func NormalizeField(field string) (string, error) {
	switch {
	case field == FieldID || field == FieldVersion || field == FieldLabels || IsTimeField(field):
		return field, nil
	case strings.HasPrefix(field, "$"):
		return "", errs.Invalidf("unknown meta field %q", field)
	case field == "":
		return "", errs.Invalidf("field is required")
	case strings.HasPrefix(field, "/"):
		p, err := jsonptr.Parse(field)
		if err != nil {
			return "", errs.Invalidf("%v", err)
		}
		return p.String(), nil
	}
	p := jsonptr.FromDotted(field)
	if slices.Contains(p, "") {
		return "", errs.Invalidf("invalid dotted field %q", field)
	}
	return p.String(), nil
}

// ParseFilter decodes a filter from its JSON form. Empty input and null
// yield nil (no filter); anything else must be a filter object, decoded
// strictly. Every problem matches errs.ErrInvalid. The result is not
// normalized: the engine does that.
func ParseFilter(raw []byte) (*Filter, error) {
	if jsonx.IsNull(raw) {
		return nil, nil
	}
	f := Filter{}
	if err := jsonx.DecodeStrict(raw, &f); err != nil {
		return nil, errs.Invalidf("filter (a filter looks like {op, field, value}): %v", err)
	}
	return &f, nil
}

// IsTimeField reports whether a normalized field is a timestamp meta field
// ($createdAt or $updatedAt).
func IsTimeField(field string) bool { return field == FieldCreatedAt || field == FieldUpdatedAt }

// IsMeta reports whether a normalized field is a meta field.
func IsMeta(field string) bool { return strings.HasPrefix(field, "$") }

// Normalize validates the filter tree and returns a copy whose fields are in
// canonical form. Stores always receive normalized filters.
func (f Filter) Normalize() (Filter, error) {
	out := Filter{Op: f.Op}
	switch f.Op {
	case OpAnd, OpOr, OpNot:
		if f.Field != "" || len(f.Value) != 0 {
			return out, errs.Invalidf("%s takes only filters", f.Op)
		}
		if f.Op == OpNot && len(f.Filters) != 1 {
			return out, errs.Invalidf("not needs exactly one filter")
		}
		if len(f.Filters) == 0 {
			return out, errs.Invalidf("%s needs at least one filter", f.Op)
		}
		for _, c := range f.Filters {
			n, err := c.Normalize()
			if err != nil {
				return out, err
			}
			out.Filters = append(out.Filters, n)
		}
		return out, nil
	case OpEq, OpNe, OpGt, OpGte, OpLt, OpLte, OpIn, OpContains, OpPrefix, OpExists:
	default:
		return out, errs.Invalidf("unknown filter op %q", f.Op)
	}
	if len(f.Filters) != 0 {
		return out, errs.Invalidf("%s does not take nested filters", f.Op)
	}
	field, err := NormalizeField(f.Field)
	if err != nil {
		return out, err
	}
	out.Field = field
	if f.Op == OpExists {
		if len(f.Value) != 0 {
			return out, errs.Invalidf("exists takes no value")
		}
		return out, nil
	}
	if len(f.Value) == 0 {
		return out, errs.Invalidf("%s %s needs a value", f.Op, f.Field)
	}
	v := any(nil)
	if err := json.Unmarshal(f.Value, &v); err != nil {
		return out, errs.Invalidf("%s %s: value is not valid JSON: %v", f.Op, f.Field, err)
	}
	switch f.Op {
	case OpIn:
		if _, ok := v.([]any); !ok {
			return out, errs.Invalidf("in %s needs an array value", f.Field)
		}
	case OpGt, OpGte, OpLt, OpLte:
		switch v.(type) {
		case float64, string:
		default:
			return out, errs.Invalidf("%s %s needs a number or string value", f.Op, f.Field)
		}
	case OpPrefix:
		if _, ok := v.(string); !ok {
			return out, errs.Invalidf("prefix %s needs a string value", f.Field)
		}
	}
	if IsTimeField(field) {
		vals := []any{v}
		if f.Op == OpIn {
			vals = v.([]any)
		}
		for _, tv := range vals {
			s, ok := tv.(string)
			if !ok {
				return out, errs.Invalidf("%s compares with RFC 3339 strings", field)
			}
			if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				return out, errs.Invalidf("%s: %v", field, err)
			}
		}
	}
	out.Value = append(json.RawMessage(nil), f.Value...)
	return out, nil
}

// Fields returns the distinct normalized fields referenced by the filter.
func (f Filter) Fields() []string {
	seen := map[string]bool{}
	out := []string{}
	var walk func(n Filter)
	walk = func(n Filter) {
		if n.Field != "" && !seen[n.Field] {
			seen[n.Field] = true
			out = append(out, n.Field)
		}
		for _, c := range n.Filters {
			walk(c)
		}
	}
	walk(f)
	return out
}
