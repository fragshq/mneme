package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"mneme"
	"mneme/query"
	"mneme/schema"
	"mneme/store"
)

// paginationFixture inserts 23 "row" records with repeating values, so sorts
// have ties, and with "g" missing from every fifth record.
func paginationFixture(t *testing.T, e *mneme.Engine) []store.Record {
	ctx := context.Background()
	// Schemas can also be written as JSON.
	must[schema.Definition](t)(e.Define(ctx, schema.Definition{Name: "row", Schema: schema.MustParse(
		`{"type":"object","properties":{"n":{"type":"integer"},"g":{"type":"string"}},"required":["n"]}`)}))
	all := []store.Record{}
	for i := 0; i < 23; i++ {
		body := fmt.Sprintf(`{"n":%d,"g":"%c"}`, i%7, 'a'+rune(i%3))
		if i%5 == 0 {
			body = fmt.Sprintf(`{"n":%d}`, i%7) // missing g
		}
		all = append(all, must[store.Record](t)(e.Insert(ctx, "row", json.RawMessage(body))))
	}
	return all
}

// pageThrough lists every "row" page by page with the given sort and page
// size, checking that no page exceeds it, and returns the IDs in order.
func pageThrough(t *testing.T, e *mneme.Engine, sort query.Sorts, size int) []string {
	t.Helper()
	ids := []string{}
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 30 {
			t.Fatal("pagination does not terminate")
		}
		p := must[query.Page[store.Record]](t)(e.List(context.Background(), "row", query.Query{Sort: sort, Limit: size, Cursor: cursor}))
		if len(p.Items) > size {
			t.Fatalf("page has %d items, limit %d", len(p.Items), size)
		}
		if p.Limit != size || p.Total != 23 || p.HasMore != (p.NextCursor != "") {
			t.Fatalf("page metadata: limit %d, total %d, hasMore %v, cursor %q", p.Limit, p.Total, p.HasMore, p.NextCursor)
		}
		for _, r := range p.Items {
			ids = append(ids, r.ID)
		}
		if p.NextCursor == "" {
			return ids
		}
		cursor = p.NextCursor
	}
}

// expectedOrder sorts recs with the reference implementation.
func expectedOrder(t *testing.T, recs []store.Record, sorts query.Sorts) []string {
	docs := must[[]*store.Doc](t)(store.NewDocs(recs))
	query.Sort(sorts, docs)
	out := []string{}
	for _, d := range docs {
		out = append(out, d.Record.ID)
	}
	return out
}

func testPagination(t *testing.T, s store.Store) {
	ctx := context.Background()
	e := Engine(t, s)
	all := paginationFixture(t, e)

	sorts := []query.Sorts{
		nil, // default: $createdAt
		{{Field: "/n"}},
		{{Field: "/n", Desc: true}},
		{{Field: "g"}, {Field: "/n", Desc: true}},
		{{Field: "/g", Desc: true}},
		{{Field: "$updatedAt", Desc: true}},
	}
	for _, srt := range sorts {
		t.Run(fmt.Sprint(srt), func(t *testing.T) {
			nq := must[query.Query](t)(query.Query{Sort: srt}.Normalize(1000, 1000))
			want := expectedOrder(t, all, nq.Sort)
			if got := pageThrough(t, e, srt, 4); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("paged order differs\nwant %v\ngot  %v", want, got)
			}
		})
	}

	t.Run("ExactMultipleHasNoEmptyTail", func(t *testing.T) {
		f := query.Lt("/n", 100)
		p := must[query.Page[store.Record]](t)(e.List(ctx, "row", query.Query{Filter: &f, Limit: 23}))
		if len(p.Items) != 23 || p.NextCursor != "" || p.HasMore || p.Total != 23 {
			t.Fatalf("want 23 items, total 23 and no more, got %d items, total %d, hasMore %v, cursor %q",
				len(p.Items), p.Total, p.HasMore, p.NextCursor)
		}
	})
	t.Run("PageMetadata", func(t *testing.T) {
		f := query.Eq("/n", 1) // i%7 == 1: i = 1, 8, 15, 22
		cases := []struct {
			name    string
			q       query.Query
			limit   int
			items   int
			total   int
			hasMore bool
		}{
			{"default page size", query.Query{}, 50, 23, 23, false},
			{"clamped to the maximum", query.Query{Limit: 5000}, 1000, 23, 23, false},
			{"partial page", query.Query{Limit: 10}, 10, 10, 23, true},
			{"filtered", query.Query{Filter: &f, Limit: 3}, 3, 3, 4, true},
			{"no match", query.Query{Filter: func() *query.Filter { f := query.Eq("/n", 99); return &f }()}, 50, 0, 0, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				p := must[query.Page[store.Record]](t)(e.List(ctx, "row", tc.q))
				if p.Limit != tc.limit || len(p.Items) != tc.items || p.Total != tc.total || p.HasMore != tc.hasMore {
					t.Fatalf("want limit %d, %d items, total %d, hasMore %v; got limit %d, %d items, total %d, hasMore %v",
						tc.limit, tc.items, tc.total, tc.hasMore, p.Limit, len(p.Items), p.Total, p.HasMore)
				}
			})
		}
	})
	t.Run("CursorIsStableAcrossInserts", func(t *testing.T) {
		p1 := must[query.Page[store.Record]](t)(e.List(ctx, "row", query.Query{Limit: 5}))
		must[store.Record](t)(e.Insert(ctx, "row", json.RawMessage(`{"n":99}`)))
		p2 := must[query.Page[store.Record]](t)(e.List(ctx, "row", query.Query{Limit: 5, Cursor: p1.NextCursor}))
		if p2.Items[0].ID != all[5].ID {
			t.Fatalf("second page should start at %s, got %s", all[5].ID, p2.Items[0].ID)
		}
	})
	t.Run("CursorBoundToSort", func(t *testing.T) {
		p1 := must[query.Page[store.Record]](t)(e.List(ctx, "row", query.Query{Limit: 5}))
		_, err := e.List(ctx, "row", query.Query{Limit: 5, Cursor: p1.NextCursor, Sort: query.Sorts{{Field: "/n"}}})
		if !errors.Is(err, mneme.ErrInvalid) {
			t.Fatalf("want ErrInvalid for a cursor from another sort, got %v", err)
		}
	})
}
