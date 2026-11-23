package tools

import (
	"github.com/google/jsonschema-go/jsonschema"

	"mneme"
	"mneme/script"
)

// Design returns the tools to design record types: list_record_types,
// define_record_type, get_record_type, validate_record_type and preview_id.
func Design(e *mneme.Engine) []Tool {
	h := handlers{e: e}
	return []Tool{
		newTool(ListRecordTypes, descListRecordTypes, h.listRecordTypes),
		newTool(DefineRecordType, descDefineRecordType, h.defineRecordType),
		newTool(GetRecordType, descGetRecordType, h.getRecordType),
		newTool(ValidateRecordType, descValidateRecordType, h.validateRecordType),
		newTool(PreviewID, descPreviewID, h.previewID),
	}
}

// Query returns the read-only tools: list_records, count_records,
// get_record, list_labels and run_script, whose description is script.Guide
// and whose runs are bounded by opts.
func Query(e *mneme.Engine, opts script.Options) []Tool {
	h := handlers{e: e, scripts: opts}
	return []Tool{
		newTool(ListRecords, descListRecords, h.listRecords),
		newTool(CountRecords, descCountRecords, h.countRecords),
		newTool(GetRecord, descGetRecord, h.getRecord),
		newTool(ListLabels, descListLabels, h.listLabels),
		newTool(RunScript, script.Guide, h.runScript),
	}
}

// Write returns the tools that change records: insert_record,
// update_record and delete_record.
func Write(e *mneme.Engine) []Tool {
	h := handlers{e: e}
	return []Tool{
		newTool(InsertRecord, descInsertRecord, h.insertRecord),
		newTool(UpdateRecord, descUpdateRecord, h.updateRecord),
		newTool(DeleteRecord, descDeleteRecord, h.deleteRecord),
	}
}

// sortSchema describes query.Sorts' lenient JSON form: a field name
// ("-field" for descending), a {field, desc} object, or an array of those.
func sortSchema() *jsonschema.Schema {
	item := func() []*jsonschema.Schema {
		field := &jsonschema.Schema{
			Type:                 "object",
			Required:             []string{"field"},
			Properties:           map[string]*jsonschema.Schema{"field": {Type: "string"}, "desc": {Type: "boolean"}},
			AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
		}
		return []*jsonschema.Schema{{Type: "string"}, field}
	}
	list := &jsonschema.Schema{Type: "array", Items: &jsonschema.Schema{AnyOf: item()}}
	return &jsonschema.Schema{AnyOf: append(item(), list)}
}
