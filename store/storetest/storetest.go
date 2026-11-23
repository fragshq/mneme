// Package storetest is the conformance suite for Mneme storage
// implementations. Call Run from a test in the implementing package:
//
//	func TestConformance(t *testing.T) {
//		storetest.Run(t, func(t *testing.T) store.Store {
//			return newEmptyStore(t) // must start empty
//		})
//	}
//
// Stores with tenancy also call RunTenancy.
package storetest

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"mneme"
	"mneme/query"
	"mneme/schema"
	"mneme/store"
)

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) store.Store

// suite lists the conformance tests Run executes.
var suite = []struct {
	name string
	test func(t *testing.T, s store.Store)
}{
	{"Definitions", testDefinitions},
	{"Records", testRecords},
	{"UpdateAndDelete", testUpdateDelete},
	{"Sequences", testSequences},
	{"Filters", testFilters},
	{"Labels", testLabels},
	{"Count", testCount},
	{"DeleteRecordType", testDeleteRecordType},
	{"SortAndPaginate", testPagination},
	{"EndToEnd", testEndToEnd},
}

// Run executes the whole suite, each test on a fresh store.
func Run(t *testing.T, newStore Factory) {
	for _, c := range suite {
		t.Run(c.name, func(t *testing.T) { c.test(t, newStore(t)) })
	}
}

// Engine returns an engine over s with a strictly increasing clock (so
// $createdAt ordering is deterministic), for tests of stores and of code
// built on them.
func Engine(t *testing.T, s store.Store) *mneme.Engine {
	t.Helper()
	clock := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mu := sync.Mutex{}
	e, err := mneme.New(mneme.Config{
		Schemas: s, Records: s, Sequences: s,
		// A strictly increasing clock keeps $createdAt ordering deterministic.
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			clock = clock.Add(time.Millisecond)
			return clock
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func must[T any](t *testing.T) func(v T, err error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func def(name string, version int, title string) schema.Definition {
	return schema.Definition{Name: name, Version: version, Title: title,
		Schema: &schema.JSONSchema{Type: "object"}, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func normalized(t *testing.T, q query.Query) query.Query {
	t.Helper()
	n, err := q.Normalize(100, 100)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// IDs joins the IDs of records with commas, for compact assertions.
func IDs(records []store.Record) string {
	out := []string{}
	for _, r := range records {
		out = append(out, r.ID)
	}
	return strings.Join(out, ",")
}
