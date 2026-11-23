package store

import (
	"encoding/json"

	"github.com/fragshq/mneme/jsonptr"
	"github.com/fragshq/mneme/query"
)

// Doc adapts a Record to query.Doc so stores evaluating queries in Go can use
// the reference evaluator (query.Match, query.Sort, query.CursorFor).
type Doc struct {
	Record Record
	data   any
}

// NewDoc decodes the record's data once and wraps it.
func NewDoc(r Record) (*Doc, error) {
	data := any(nil)
	if err := json.Unmarshal(r.Data, &data); err != nil {
		return nil, err
	}
	return &Doc{Record: r, data: data}, nil
}

// Lookup implements query.Doc.
func (d *Doc) Lookup(field string) (any, bool) {
	switch field {
	case query.FieldID:
		return d.Record.ID, true
	case query.FieldVersion:
		return float64(d.Record.SchemaVersion), true
	case query.FieldCreatedAt:
		return d.Record.CreatedAt, true
	case query.FieldUpdatedAt:
		return d.Record.UpdatedAt, true
	case query.FieldLabels:
		out := make([]any, len(d.Record.Labels))
		for i, l := range d.Record.Labels {
			out[i] = l
		}
		return out, true
	}
	p, err := jsonptr.Parse(field)
	if err != nil {
		return nil, false
	}
	return p.Get(d.data)
}
