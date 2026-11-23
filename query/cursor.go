package query

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"mneme/errs"
)

// Cursor is the decoded form of a keyset pagination cursor: the sort key of
// the last item returned. Stores create it with CursorFor (or by filling Keys
// themselves) and resume strictly after it.
type Cursor struct {
	// Sort is a signature of the sort the cursor was made for.
	Sort string `json:"s"`
	// Keys holds one entry per sort field, in order.
	Keys []CursorKey `json:"k"`
}

// CursorKey is one sort-key value. Missing is true when the record lacks the field.
type CursorKey struct {
	Value   json.RawMessage `json:"v,omitempty"`
	Missing bool            `json:"m,omitempty"`
}

// Decoded returns the key value decoded by encoding/json, and whether it is present.
func (k CursorKey) Decoded() (any, bool) {
	if k.Missing {
		return nil, false
	}
	v := any(nil)
	_ = json.Unmarshal(k.Value, &v)
	return v, true
}

func sortSignature(sorts []SortField) string {
	parts := make([]string, len(sorts))
	for i, s := range sorts {
		dir := "a"
		if s.Desc {
			dir = "d"
		}
		parts[i] = s.Field + ":" + dir
	}
	return strings.Join(parts, ",")
}

// CursorFor builds the cursor positioned at d.
func CursorFor(sorts []SortField, d Doc) Cursor {
	c := Cursor{Sort: sortSignature(sorts)}
	for _, s := range sorts {
		v, ok := d.Lookup(s.Field)
		if !ok {
			c.Keys = append(c.Keys, CursorKey{Missing: true})
			continue
		}
		if t, isTime := v.(time.Time); isTime {
			v = t.UTC().Format(time.RFC3339Nano)
		}
		raw, _ := json.Marshal(v)
		c.Keys = append(c.Keys, CursorKey{Value: raw})
	}
	return c
}

// Encode returns the opaque string form.
func (c Cursor) Encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses an opaque cursor and checks it was made for sorts.
func DecodeCursor(s string, sorts []SortField) (Cursor, error) {
	c := Cursor{}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err == nil {
		err = json.Unmarshal(raw, &c)
	}
	if err != nil {
		return c, errs.Invalidf("malformed cursor")
	}
	if c.Sort != sortSignature(sorts) || len(c.Keys) != len(sorts) {
		return c, errs.Invalidf("cursor does not match the query's sort")
	}
	return c, nil
}

// After reports whether d sorts strictly after the cursor position.
func (c Cursor) After(sorts []SortField, d Doc) bool {
	for i, s := range sorts {
		dv, dok := d.Lookup(s.Field)
		cv, cok := c.Keys[i].Decoded()
		cmp := CompareValues(dv, dok, cv, cok)
		if s.Desc {
			cmp = -cmp
		}
		if cmp != 0 {
			return cmp > 0
		}
	}
	return false
}
