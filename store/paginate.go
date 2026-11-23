package store

import "github.com/fragshq/mneme/query"

// The helpers below evaluate queries in Go with the reference semantics, for
// stores that cannot evaluate filters natively (in-memory maps, key-value
// stores). They work on Docs, so records are decoded once (NewDocs) and may
// be kept decoded between calls. They read docs without modifying them or
// the slice, so docs may be shared by concurrent calls. Each call costs
// O(n), plus O(n log n) to sort in Paginate.

// NewDocs wraps records in Docs, decoding each record's data once.
func NewDocs(records []Record) ([]*Doc, error) {
	docs := make([]*Doc, 0, len(records))
	for _, r := range records {
		d, err := NewDoc(r)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, nil
}

// Paginate computes ListRecords' result: it filters docs with the normalized
// query, sorts them, resumes after q.Cursor and cuts a page of q.Limit
// items, setting NextCursor only when more items follow. The returned
// records share memory with docs: a store that keeps docs must hand out
// copies (Record.Normalized).
func Paginate(docs []*Doc, q query.Query) (query.Page[Record], error) {
	page := query.Page[Record]{Items: []Record{}}
	cursor := (*query.Cursor)(nil)
	if q.Cursor != "" {
		c, err := query.DecodeCursor(q.Cursor, q.Sort)
		if err != nil {
			return page, err
		}
		cursor = &c
	}
	matched := []*Doc{}
	for _, d := range docs {
		if query.Match(q.Filter, d) && (cursor == nil || cursor.After(q.Sort, d)) {
			matched = append(matched, d)
		}
	}
	query.Sort(q.Sort, matched)
	if len(matched) > q.Limit {
		matched = matched[:q.Limit]
		page.NextCursor = query.CursorFor(q.Sort, matched[len(matched)-1]).Encode()
	}
	for _, d := range matched {
		page.Items = append(page.Items, d.Record)
	}
	return page, nil
}

// Count computes CountRecords' result: the number of docs matching filter
// (nil matches all).
func Count(docs []*Doc, filter *query.Filter) int {
	n := 0
	for _, d := range docs {
		if query.Match(filter, d) {
			n++
		}
	}
	return n
}
