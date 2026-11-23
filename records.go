package mneme

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/jsonx"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/schema"
	"github.com/fragshq/mneme/store"
)

// Insert validates data against the record type's latest schema version, generates
// an ID with the record type's ID strategy, and stores the record together with
// the schema version used. Labels are optional technical metadata outside the
// schema; they are stored as a sorted, de-duplicated set.
func (e *Engine) Insert(ctx context.Context, recordType string, data json.RawMessage, labels ...string) (Record, error) {
	c, compact, set, err := e.prepareWrite(ctx, recordType, data, labels)
	if err != nil {
		return Record{}, err
	}
	if err := e.checkRecordQuota(ctx, recordType); err != nil {
		return Record{}, err
	}
	doc, err := decodeDoc(compact)
	if err != nil {
		return Record{}, err
	}
	now := e.now()
	attempts := e.cfg.MaxIDAttempts
	if c.ids.Deterministic() {
		attempts = 1
	}
	for i := 0; ; i++ {
		id, err := c.ids.Generate(ctx, idgen.GenContext{RecordType: recordType, Data: doc, Now: now, Sequences: e.cfg.Sequences})
		if err != nil {
			return Record{}, err
		}
		r := Record{RecordType: recordType, ID: id, SchemaVersion: c.schema.Definition.Version, Data: compact, CreatedAt: now, UpdatedAt: now, Labels: set}
		err = e.cfg.Records.InsertRecord(ctx, r)
		if err == nil {
			return r, nil
		}
		if !errors.Is(err, errs.ErrConflict) || i+1 >= attempts {
			return Record{}, err
		}
	}
}

// checkRecordQuota fails with ErrQuota when recordType already holds
// Config.MaxRecordsPerType records.
func (e *Engine) checkRecordQuota(ctx context.Context, recordType string) error {
	if e.cfg.MaxRecordsPerType <= 0 {
		return nil
	}
	n, err := e.cfg.Records.CountRecords(ctx, recordType, nil)
	if err != nil {
		return err
	}
	if n >= e.cfg.MaxRecordsPerType {
		return fmt.Errorf("%w: record type %q already holds %d records, the maximum; delete some to insert more", errs.ErrQuota, recordType, e.cfg.MaxRecordsPerType)
	}
	return nil
}

// decodeDoc decodes record data for ID generation, which reads fields from
// it. Empty data decodes to nil.
func decodeDoc(data json.RawMessage) (any, error) {
	doc := any(nil)
	if jsonx.IsNull(data) {
		return doc, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, errs.Invalidf("%v", err)
	}
	return doc, nil
}

// checkRef validates a record reference: a valid record type name and a
// non-empty ID.
func checkRef(recordType string, id string) error {
	if err := schema.ValidateName(recordType); err != nil {
		return err
	}
	if id == "" {
		return errs.Invalidf("id is required")
	}
	return nil
}

// prepareWrite readies an insert or update: it fetches the record type's
// latest version, normalizes the labels, and validates data against the
// schema, returning it compacted.
func (e *Engine) prepareWrite(ctx context.Context, recordType string, data json.RawMessage, labels []string) (*compiled, json.RawMessage, []string, error) {
	c, err := e.latest(ctx, recordType)
	if err != nil {
		return nil, nil, nil, err
	}
	set, err := normalizeLabels(labels)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := c.schema.Validate(data); err != nil {
		return nil, nil, nil, err
	}
	compact := bytes.Buffer{}
	if err := json.Compact(&compact, data); err != nil {
		return nil, nil, nil, errs.Invalidf("%v", err)
	}
	return c, compact.Bytes(), set, nil
}

// Update replaces a record's data and labels entirely: labels left out are
// removed. The new data must conform to the record type's latest schema
// version, which becomes the record's SchemaVersion: updating an old record
// migrates it to the latest version. The ID and CreatedAt never change;
// UpdatedAt is set to now. There is no concurrency control: the last writer
// wins.
func (e *Engine) Update(ctx context.Context, recordType string, id string, data json.RawMessage, labels ...string) (Record, error) {
	c, compact, set, err := e.prepareWrite(ctx, recordType, data, labels)
	if err != nil {
		return Record{}, err
	}
	if id == "" { // prepareWrite already checked recordType
		return Record{}, errs.Invalidf("id is required")
	}
	current, err := e.cfg.Records.GetRecord(ctx, recordType, id)
	if err != nil {
		return Record{}, err
	}
	current.SchemaVersion = c.schema.Definition.Version
	current.Data = compact
	current.UpdatedAt = e.now()
	current.Labels = set
	if err := e.cfg.Records.UpdateRecord(ctx, current); err != nil {
		return Record{}, err
	}
	return current, nil
}

// Delete removes a record permanently. It returns ErrNotFound when the
// record does not exist.
func (e *Engine) Delete(ctx context.Context, recordType string, id string) error {
	if err := checkRef(recordType, id); err != nil {
		return err
	}
	return e.cfg.Records.DeleteRecord(ctx, recordType, id)
}

// PreviewID renders an ID for data with the record type's latest ID strategy
// without consuming sequence values (sequence segments render as their first
// value) or storing anything. Use it to show users what IDs will look like.
func (e *Engine) PreviewID(ctx context.Context, recordType string, data json.RawMessage) (string, error) {
	c, err := e.latest(ctx, recordType)
	if err != nil {
		return "", err
	}
	doc, err := decodeDoc(data)
	if err != nil {
		return "", err
	}
	return c.ids.Generate(ctx, idgen.GenContext{RecordType: recordType, Data: doc, Now: e.now(), Sequences: previewSequences{}})
}

type previewSequences struct{}

func (previewSequences) Next(context.Context, string, string, string) (int64, error) { return 1, nil }

// Get fetches one record by ID. The record may conform to any version of the
// record type's schema; see Record.SchemaVersion.
func (e *Engine) Get(ctx context.Context, recordType string, id string) (Record, error) {
	if err := checkRef(recordType, id); err != nil {
		return Record{}, err
	}
	return e.cfg.Records.GetRecord(ctx, recordType, id)
}

// List returns a page of a record type's records, filtered across all schema
// versions. Pass Page.NextCursor back in Query.Cursor (with the same filter
// and sort) to fetch the next page. Besides the items, the page carries the
// applied Limit, HasMore, and the Total number of matching records, counted
// with a separate store call.
func (e *Engine) List(ctx context.Context, recordType string, q query.Query) (query.Page[Record], error) {
	nq, err := q.Normalize(e.cfg.DefaultPageSize, e.cfg.MaxPageSize)
	if err != nil {
		return query.Page[Record]{}, err
	}
	if err := e.checkFields(ctx, recordType, nq.Filter, nq.Sort); err != nil {
		return query.Page[Record]{}, err
	}
	page, err := e.cfg.Records.ListRecords(ctx, recordType, nq)
	if err != nil {
		return query.Page[Record]{}, err
	}
	if page.Items == nil {
		page.Items = []store.Record{}
	}
	total, err := e.cfg.Records.CountRecords(ctx, recordType, nq.Filter)
	if err != nil {
		return query.Page[Record]{}, err
	}
	page.Limit = nq.Limit
	page.Total = total
	page.HasMore = page.NextCursor != ""
	return page, nil
}

// Count returns the number of a record type's records that match filter
// (nil: every record). The filter follows the same semantics as List's.
func (e *Engine) Count(ctx context.Context, recordType string, filter *query.Filter) (int, error) {
	nf, err := e.checkFilter(ctx, recordType, filter)
	if err != nil {
		return 0, err
	}
	return e.cfg.Records.CountRecords(ctx, recordType, nf)
}

// LabelCount is one distinct label and the number of records carrying it.
type LabelCount = store.LabelCount

// ListLabels returns the distinct labels of a record type's records that
// match filter (nil: every record), each with the number of matching
// records carrying it, sorted by label in byte order. The filter follows the
// same semantics as List's.
func (e *Engine) ListLabels(ctx context.Context, recordType string, filter *query.Filter) ([]LabelCount, error) {
	nf, err := e.checkFilter(ctx, recordType, filter)
	if err != nil {
		return nil, err
	}
	return e.cfg.Records.ListLabels(ctx, recordType, nf)
}

// checkFilter normalizes filter (nil stays nil) and checks it with
// checkFields.
func (e *Engine) checkFilter(ctx context.Context, recordType string, filter *query.Filter) (*query.Filter, error) {
	if filter == nil {
		return nil, e.checkFields(ctx, recordType, nil, nil)
	}
	f, err := filter.Normalize()
	if err != nil {
		return nil, err
	}
	return &f, e.checkFields(ctx, recordType, &f, nil)
}

// checkFields fails when the record type does not exist or, with
// StrictFilterFields, when a field of the normalized filter or sort is
// declared by no version of it.
func (e *Engine) checkFields(ctx context.Context, recordType string, filter *query.Filter, sorts []query.SortField) error {
	if !e.cfg.StrictFilterFields {
		_, err := e.GetRecordType(ctx, recordType, 0)
		return err
	}
	cs, err := e.allVersions(ctx, recordType)
	if err != nil {
		return err
	}
	refs := []string{}
	if filter != nil {
		refs = filter.Fields()
	}
	for _, s := range sorts {
		refs = append(refs, s.Field)
	}
	fields := schema.MergeFields(cs)
	for _, f := range refs {
		if !query.IsMeta(f) && !schema.FieldsContain(fields, f) {
			return errs.Invalidf("record type %s has no field %s in any version", recordType, f)
		}
	}
	return nil
}
