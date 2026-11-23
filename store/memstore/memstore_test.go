package memstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/store"
	"github.com/fragshq/mneme/store/memstore"
	"github.com/fragshq/mneme/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store { return memstore.New() })
}

// BenchmarkListPage lists one small page out of many records, the common
// case for a list call.
func BenchmarkListPage(b *testing.B) {
	ctx := context.Background()
	s := memstore.New()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 10000 {
		r := store.Record{
			RecordType: "order",
			ID:         fmt.Sprintf("O-%05d", i),
			Data:       json.RawMessage(fmt.Sprintf(`{"status":"open","total":%d,"lines":[{"sku":"A","qty":1}]}`, i)),
			CreatedAt:  at.Add(time.Duration(i) * time.Second),
		}
		if err := s.InsertRecord(ctx, r); err != nil {
			b.Fatal(err)
		}
	}
	f := query.Gte("/total", 5000)
	q, err := query.Query{Filter: &f, Limit: 10}.Normalize(10, 100)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.ListRecords(ctx, "order", q); err != nil {
			b.Fatal(err)
		}
	}
}
