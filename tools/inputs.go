package tools

import (
	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/schema"
)

// ListRecordTypesInput is the input of list_record_types.
type ListRecordTypesInput struct{}

// DefineRecordTypeInput is the input of define_record_type and
// validate_record_type.
type DefineRecordTypeInput struct {
	Name        string         `json:"name" jsonschema:"record type name: lowercase letters, digits, '_' or '-', starting with a letter"`
	Title       string         `json:"title,omitempty" jsonschema:"human readable title"`
	Description string         `json:"description,omitempty" jsonschema:"what the record type represents"`
	Schema      map[string]any `json:"schema"`
	IDStrategy  map[string]any `json:"id_strategy,omitempty"`
	Indexes     []string       `json:"indexes,omitempty" jsonschema:"fields worth indexing (dotted paths or JSON pointers); whether indexes are created depends on the storage"`
}

// definition converts the input to a schema.Definition.
func (in DefineRecordTypeInput) definition() (schema.Definition, error) {
	d := schema.Definition{Name: in.Name, Title: in.Title, Description: in.Description, Indexes: in.Indexes, Schema: &schema.JSONSchema{}}
	if err := convert("schema", in.Schema, d.Schema); err != nil {
		return d, err
	}
	if in.IDStrategy != nil {
		d.IDStrategy = &idgen.Spec{}
		if err := convert("id_strategy", in.IDStrategy, d.IDStrategy); err != nil {
			return d, err
		}
	}
	return d, nil
}

// GetRecordTypeInput is the input of get_record_type.
type GetRecordTypeInput struct {
	Name    string `json:"name" jsonschema:"record type name"`
	Version int    `json:"version,omitempty" jsonschema:"a specific version; omit for the latest"`
}

// PreviewIDInput is the input of preview_id.
type PreviewIDInput struct {
	RecordType string         `json:"record_type" jsonschema:"record type name"`
	Data       map[string]any `json:"data,omitempty" jsonschema:"sample record data, needed only when the ID strategy uses record fields"`
}

// ListRecordsInput is the input of list_records.
type ListRecordsInput struct {
	RecordType string         `json:"record_type" jsonschema:"record type name"`
	Filter     map[string]any `json:"filter,omitempty"`
	Sort       query.Sorts    `json:"sort,omitempty"`
	Limit      int            `json:"limit,omitempty" jsonschema:"page size (default 50)"`
	Cursor     string         `json:"cursor,omitempty" jsonschema:"nextCursor from the previous page"`
}

// FilterInput is the input of count_records and list_labels.
type FilterInput struct {
	RecordType string         `json:"record_type" jsonschema:"record type name"`
	Filter     map[string]any `json:"filter,omitempty"`
}

// RecordRef is the input of get_record and delete_record.
type RecordRef struct {
	RecordType string `json:"record_type" jsonschema:"record type name"`
	ID         string `json:"id" jsonschema:"record ID"`
}

// RunScriptInput is the input of run_script.
type RunScriptInput struct {
	Code string `json:"code" jsonschema:"the JavaScript to run, as described in the tool description"`
}

// InsertRecordInput is the input of insert_record.
type InsertRecordInput struct {
	RecordType string         `json:"record_type" jsonschema:"record type name"`
	Data       map[string]any `json:"data" jsonschema:"the record; must conform to the latest schema of the record type"`
	Labels     []string       `json:"labels,omitempty" jsonschema:"optional labels: free-form tags stored outside the schema, usable in filters as $labels"`
}

// UpdateRecordInput is the input of update_record.
type UpdateRecordInput struct {
	RecordType string         `json:"record_type" jsonschema:"record type name"`
	ID         string         `json:"id" jsonschema:"record ID"`
	Data       map[string]any `json:"data" jsonschema:"the complete new record (full replace); must conform to the latest schema of the record type"`
	Labels     []string       `json:"labels,omitempty" jsonschema:"the complete new set of labels (full replace: labels left out are removed)"`
}

// Results that are not engine types. Every tool returns a JSON object.

// RecordTypeList is the result of list_record_types.
type RecordTypeList struct {
	RecordTypes []mneme.RecordTypeSummary `json:"recordTypes"`
}

// RecordTypeDetails is the result of get_record_type.
type RecordTypeDetails struct {
	Definition schema.Definition `json:"definition"`
	Versions   []int             `json:"versions"`
	// Fields are the fields declared across all versions.
	Fields []schema.Field `json:"fields"`
}

// Validation is the result of validate_record_type.
type Validation struct {
	Valid bool `json:"valid"`
}

// PreviewedID is the result of preview_id.
type PreviewedID struct {
	ID string `json:"id"`
}

// Count is the result of count_records.
type Count struct {
	Count int `json:"count"`
}

// LabelList is the result of list_labels.
type LabelList struct {
	Labels []mneme.LabelCount `json:"labels"`
}

// Deleted is the result of delete_record.
type Deleted struct {
	Deleted string `json:"deleted"`
}
