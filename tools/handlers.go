package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mneme"
	"mneme/query"
	"mneme/script"
)

// handlers implements every tool on an engine. Each method takes the
// decoded input and returns a result that encodes to a JSON object.
type handlers struct {
	e       *mneme.Engine
	scripts script.Options
}

func (h handlers) listRecordTypes(ctx context.Context, _ ListRecordTypesInput) (any, error) {
	defs, err := h.e.ListRecordTypes(ctx)
	if err != nil {
		return nil, err
	}
	return RecordTypeList{RecordTypes: mneme.Summaries(defs)}, nil
}

func (h handlers) defineRecordType(ctx context.Context, in DefineRecordTypeInput) (any, error) {
	d, err := in.definition()
	if err != nil {
		return nil, err
	}
	return h.e.Define(ctx, d)
}

func (h handlers) getRecordType(ctx context.Context, in GetRecordTypeInput) (any, error) {
	d, err := h.e.GetRecordType(ctx, in.Name, in.Version)
	if err != nil {
		return nil, err
	}
	desc, err := h.e.DescribeRecordType(ctx, in.Name)
	if err != nil {
		return nil, err
	}
	return RecordTypeDetails{Definition: d, Versions: desc.Versions, Fields: desc.Fields}, nil
}

func (h handlers) validateRecordType(_ context.Context, in DefineRecordTypeInput) (any, error) {
	d, err := in.definition()
	if err != nil {
		return nil, err
	}
	if err := h.e.ValidateDefinition(d); err != nil {
		return nil, err
	}
	return Validation{Valid: true}, nil
}

func (h handlers) previewID(ctx context.Context, in PreviewIDInput) (any, error) {
	id, err := h.e.PreviewID(ctx, in.RecordType, rawData(in.Data))
	if err != nil {
		return nil, err
	}
	return PreviewedID{ID: id}, nil
}

func (h handlers) listRecords(ctx context.Context, in ListRecordsInput) (any, error) {
	f, err := filter(in.Filter)
	if err != nil {
		return nil, err
	}
	return h.e.List(ctx, in.RecordType, query.Query{Filter: f, Sort: in.Sort, Limit: in.Limit, Cursor: in.Cursor})
}

func (h handlers) countRecords(ctx context.Context, in FilterInput) (any, error) {
	f, err := filter(in.Filter)
	if err != nil {
		return nil, err
	}
	n, err := h.e.Count(ctx, in.RecordType, f)
	if err != nil {
		return nil, err
	}
	return Count{Count: n}, nil
}

func (h handlers) listLabels(ctx context.Context, in FilterInput) (any, error) {
	f, err := filter(in.Filter)
	if err != nil {
		return nil, err
	}
	labels, err := h.e.ListLabels(ctx, in.RecordType, f)
	if err != nil {
		return nil, err
	}
	return LabelList{Labels: labels}, nil
}

// filter decodes an optional filter input; nil stays nil. in was decoded
// from JSON, so it always encodes.
func filter(in map[string]any) (*query.Filter, error) {
	if in == nil {
		return nil, nil
	}
	raw, _ := json.Marshal(in)
	return query.ParseFilter(raw)
}

func (h handlers) getRecord(ctx context.Context, in RecordRef) (any, error) {
	return h.e.Get(ctx, in.RecordType, in.ID)
}

// runScript appends the script's console output to its error, since it
// usually shows how far the script got.
func (h handlers) runScript(ctx context.Context, in RunScriptInput) (any, error) {
	res, err := script.Run(ctx, h.e, in.Code, h.scripts)
	if err == nil {
		return res, nil
	}
	if len(res.Logs) == 0 {
		return nil, err
	}
	return nil, fmt.Errorf("%w\n\nconsole output:\n%s", err, strings.Join(res.Logs, "\n"))
}

func (h handlers) insertRecord(ctx context.Context, in InsertRecordInput) (any, error) {
	return h.e.Insert(ctx, in.RecordType, rawData(in.Data), in.Labels...)
}

func (h handlers) updateRecord(ctx context.Context, in UpdateRecordInput) (any, error) {
	return h.e.Update(ctx, in.RecordType, in.ID, rawData(in.Data), in.Labels...)
}

func (h handlers) deleteRecord(ctx context.Context, in RecordRef) (any, error) {
	if err := h.e.Delete(ctx, in.RecordType, in.ID); err != nil {
		return nil, err
	}
	return Deleted{Deleted: in.ID}, nil
}
