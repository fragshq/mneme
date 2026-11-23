// Package store declares the storage contracts an application must implement
// to host Mneme. Mneme never decides how data is laid out: tenancy (carried
// in the context), table-per-record-type vs shared tables, and column choices are
// all up to the implementation.
//
// Implementations should pass the conformance suite in store/storetest.
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/schema"
)

// Sentinel errors stores must return (possibly wrapped).
var (
	ErrNotFound = errs.ErrNotFound
	ErrConflict = errs.ErrConflict
	ErrNoTenant = errs.ErrNoTenant
)

// Record is one stored item. SchemaVersion is the version of the record
// type's schema the data was validated against when last written (inserted
// or updated). UpdatedAt equals CreatedAt until the record is updated.
//
// Labels are technical metadata supplied by the caller, outside the schema:
// a sorted, de-duplicated set of non-empty strings, never nil (an empty
// slice when there are none). Stores must round-trip them unchanged.
type Record struct {
	RecordType    string          `json:"recordType"`
	ID            string          `json:"id"`
	SchemaVersion int             `json:"schemaVersion"`
	Data          json.RawMessage `json:"data"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	Labels        []string        `json:"labels"`
}

// Normalized returns r in the form stores must hand back on reads:
// timestamps in UTC, and Labels a non-nil copy (empty when there are none).
// Data is copied too, so the result shares no memory with r.
func (r Record) Normalized() Record {
	r.CreatedAt = r.CreatedAt.UTC()
	r.UpdatedAt = r.UpdatedAt.UTC()
	r.Labels = append([]string{}, r.Labels...)
	r.Data = append(json.RawMessage(nil), r.Data...)
	return r
}

// SchemaStore persists record type definitions. Definitions are immutable.
type SchemaStore interface {
	// PutDefinition stores a new version. It returns ErrConflict if that
	// (name, version) already exists.
	PutDefinition(ctx context.Context, d schema.Definition) error
	// GetDefinition returns one version; version 0 means the latest.
	// It returns ErrNotFound when absent.
	GetDefinition(ctx context.Context, name string, version int) (schema.Definition, error)
	// ListDefinitions returns the latest version of every record type, by name.
	ListDefinitions(ctx context.Context) ([]schema.Definition, error)
	// ListVersions returns every version of a record type, oldest first, or
	// ErrNotFound if the record type does not exist.
	ListVersions(ctx context.Context, name string) ([]schema.Definition, error)
	// DeleteDefinitions removes every version of a record type, or returns
	// ErrNotFound if it does not exist. Afterwards the name is free: a new
	// definition starts again from version 1.
	DeleteDefinitions(ctx context.Context, name string) error
}

// RecordStore persists records.
type RecordStore interface {
	// InsertRecord stores r. It returns ErrConflict if (r.RecordType, r.ID) exists.
	InsertRecord(ctx context.Context, r Record) error
	// GetRecord returns ErrNotFound when absent.
	GetRecord(ctx context.Context, recordType string, id string) (Record, error)
	// UpdateRecord replaces the stored record (r.RecordType, r.ID) with r as
	// given: SchemaVersion, Data, CreatedAt, UpdatedAt and Labels. It returns
	// ErrNotFound when the record does not exist. There is no concurrency
	// control: the last writer wins.
	UpdateRecord(ctx context.Context, r Record) error
	// DeleteRecord removes a record permanently. It returns ErrNotFound
	// when the record does not exist.
	DeleteRecord(ctx context.Context, recordType string, id string) error
	// ListRecords returns a page of the record type's records. q is always
	// normalized (see query.Query.Normalize): its filter uses canonical
	// fields, its sort ends with $id, and Limit is > 0. Implementations must
	// follow the reference semantics documented in package query and return
	// a cursor (query.CursorFor) when more results may follow. They fill
	// only Items and NextCursor; the engine sets the other Page fields.
	ListRecords(ctx context.Context, recordType string, q query.Query) (query.Page[Record], error)
	// CountRecords returns the number of the record type's records that
	// match filter (nil matches every record). filter is normalized (see
	// query.Filter.Normalize) and follows the reference semantics, like
	// ListRecords'. A record type without records counts 0.
	CountRecords(ctx context.Context, recordType string, filter *query.Filter) (int, error)
	// ListLabels returns the distinct labels carried by the record type's
	// records that match filter (nil matches every record), each with the
	// number of those records carrying it, sorted by label in byte order.
	// filter is normalized (see query.Filter.Normalize) and follows the
	// reference semantics, like ListRecords'. A record type without records
	// or labels yields an empty, non-nil slice.
	ListLabels(ctx context.Context, recordType string, filter *query.Filter) ([]LabelCount, error)
	// DeleteRecords removes every record of a record type permanently.
	// Deleting a record type without records is not an error.
	DeleteRecords(ctx context.Context, recordType string) error
}

// LabelCount is one distinct label and the number of records carrying it.
type LabelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// SequenceProvider hands out progressive counters for ID generation.
type SequenceProvider interface {
	idgen.SequenceProvider
	// DeleteSequences removes every counter of a record type, whatever its
	// name and scope, so the next Next for it returns 1 again. Deleting a
	// record type without counters is not an error.
	DeleteSequences(ctx context.Context, recordType string) error
}

// Store is a complete storage implementation: definitions, records and
// sequences. Storage plugins implement it, and the conformance suite
// (package storetest) tests it.
type Store interface {
	SchemaStore
	RecordStore
	SequenceProvider
}
