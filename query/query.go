package query

import (
	"encoding/json"
	"fmt"
	"strings"

	"mneme/errs"
	"mneme/jsonx"
)

// SortField orders results by one field.
type SortField struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc,omitempty"`
}

// Sorts is an ordered list of sort fields. Its JSON form is lenient, to
// accept what people and LLMs naturally write: a field name ("total"), a
// field name prefixed with "-" for descending ("-total"), a {field, desc}
// object, or an array mixing those.
type Sorts []SortField

// UnmarshalJSON implements the lenient form described on Sorts.
func (s *Sorts) UnmarshalJSON(raw []byte) error {
	if jsonx.IsNull(raw) {
		*s = nil
		return nil
	}
	items := []json.RawMessage{raw}
	if jsonx.Kind(raw) == '[' {
		items = nil
		if err := json.Unmarshal(raw, &items); err != nil {
			return errs.Invalidf("sort: %v", err)
		}
	}
	out := Sorts{}
	for _, it := range items {
		f, err := parseSortField(it)
		if err != nil {
			return err
		}
		out = append(out, f)
	}
	*s = out
	return nil
}

func parseSortField(raw json.RawMessage) (SortField, error) {
	name := ""
	if jsonx.Kind(raw) == '"' {
		if err := json.Unmarshal(raw, &name); err != nil {
			return SortField{}, errs.Invalidf("sort: %v", err)
		}
		if field, desc := strings.CutPrefix(name, "-"); field != "" {
			return SortField{Field: field, Desc: desc}, nil
		}
		return SortField{}, errs.Invalidf("sort: empty field name")
	}
	f := SortField{}
	if err := jsonx.DecodeStrict(raw, &f); err != nil {
		return f, errs.Invalidf(`sort entries are "field", "-field" or {"field": ..., "desc": true}: %v`, err)
	}
	return f, nil
}

// Query selects a page of records of one record type.
type Query struct {
	Filter *Filter `json:"filter,omitempty"`
	Sort   Sorts   `json:"sort,omitempty"`
	Limit  int     `json:"limit,omitempty"`
	Cursor string  `json:"cursor,omitempty"`
}

// Page is one page of results with its metadata.
//
// Stores fill only Items and NextCursor; the engine adds Limit, Total and
// HasMore.
type Page[T any] struct {
	// Items are the page's results, at most Limit of them.
	Items []T `json:"items"`
	// Limit is the page size applied: the requested one, or the default
	// when none was requested, capped at the maximum.
	Limit int `json:"limit"`
	// Total is the number of records matching the filter across all pages,
	// cursor ignored. It is counted separately from the page, so concurrent
	// writes can make it differ from what paging returns.
	Total int `json:"total"`
	// HasMore reports whether more items follow; NextCursor is then set.
	HasMore bool `json:"hasMore"`
	// NextCursor, passed back as Query.Cursor with the same filter and sort,
	// fetches the next page. It is empty on the last page.
	NextCursor string `json:"nextCursor,omitempty"`
}

// Normalize validates q and returns the canonical form handed to stores:
//   - the filter is normalized (see Filter.Normalize);
//   - sort fields are canonical, default to $createdAt ascending, and always
//     end with $id ascending as a unique tiebreaker (unless $id is already
//     present, in which case anything after it is dropped);
//   - Limit is defaultLimit when <= 0, and capped at maxLimit.
func (q Query) Normalize(defaultLimit int, maxLimit int) (Query, error) {
	out := Query{Cursor: q.Cursor, Limit: q.Limit}
	if q.Filter != nil {
		f, err := q.Filter.Normalize()
		if err != nil {
			return out, err
		}
		out.Filter = &f
	}
	sorts := q.Sort
	if len(sorts) == 0 {
		sorts = []SortField{{Field: FieldCreatedAt}}
	}
	seen := map[string]bool{}
	hasID := false
	for _, s := range sorts {
		f, err := NormalizeField(s.Field)
		if err != nil {
			return out, fmt.Errorf("sort: %w", err)
		}
		if seen[f] {
			return out, errs.Invalidf("sort field %s listed twice", f)
		}
		seen[f] = true
		out.Sort = append(out.Sort, SortField{Field: f, Desc: s.Desc})
		if f == FieldID {
			hasID = true
			break
		}
	}
	if !hasID {
		out.Sort = append(out.Sort, SortField{Field: FieldID})
	}
	if out.Limit <= 0 {
		out.Limit = defaultLimit
	}
	if maxLimit > 0 && out.Limit > maxLimit {
		out.Limit = maxLimit
	}
	return out, nil
}
