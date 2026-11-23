package mneme_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/schema"
)

func TestRecordTypeQuota(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, func(c *mneme.Config) { c.MaxRecordTypes = 2 })
	define := func(name string) error {
		_, err := e.Define(ctx, schema.Definition{Name: name, Schema: &schema.JSONSchema{Type: "object"}})
		return err
	}
	for _, name := range []string{"a", "b"} {
		if err := define(name); err != nil {
			t.Fatal(err)
		}
	}
	err := define("c")
	if !errors.Is(err, mneme.ErrQuota) || errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("a third type: want ErrQuota (not ErrInvalid), got %v", err)
	}
	if err := define("a"); err != nil {
		t.Fatalf("a new version of an existing type is always allowed: %v", err)
	}
	if err := e.DeleteRecordType(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if err := define("c"); err != nil {
		t.Fatalf("deleting a type makes room: %v", err)
	}
}

func TestRecordQuota(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, func(c *mneme.Config) { c.MaxRecordsPerType = 2 })
	for _, name := range []string{"a", "b"} {
		if _, err := e.Define(ctx, schema.Definition{Name: name, Schema: &schema.JSONSchema{Type: "object"}}); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(recordType string, n int) (mneme.Record, error) {
		return e.Insert(ctx, recordType, json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)))
	}
	first := mneme.Record{}
	for i := range 2 {
		r, err := insert("a", i)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = r
		}
	}
	_, err := insert("a", 2)
	if !errors.Is(err, mneme.ErrQuota) || errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("a third record: want ErrQuota (not ErrInvalid), got %v", err)
	}
	if _, err := insert("b", 0); err != nil {
		t.Fatalf("the quota is per record type: %v", err)
	}
	if _, err := e.Update(ctx, "a", first.ID, json.RawMessage(`{"n":9}`)); err != nil {
		t.Fatalf("updates are always allowed: %v", err)
	}
	if err := e.Delete(ctx, "a", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := insert("a", 3); err != nil {
		t.Fatalf("deleting a record makes room: %v", err)
	}
}

// TestQuotaInvalidFirst checks that a record that fails validation reports
// that, not the quota: the caller's mistake is the more useful error.
func TestQuotaInvalidFirst(t *testing.T) {
	ctx := context.Background()
	e, _ := newEngine(t, func(c *mneme.Config) { c.MaxRecordsPerType = 1 })
	required := &schema.JSONSchema{Type: "object", Required: []string{"x"}}
	if _, err := e.Define(ctx, schema.Definition{Name: "a", Schema: required}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Insert(ctx, "a", json.RawMessage(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Insert(ctx, "a", json.RawMessage(`{}`)); !errors.Is(err, mneme.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}
