// Package tools describes Mneme operations as protocol-neutral tools: a
// name, a description written for an LLM, a JSON Schema for the input, and a
// handler that takes the input as JSON. An application exposes them over any
// protocol (an MCP server, an HTTP API, a CLI) without restating what each
// operation does or how to call it well.
//
// Nothing here knows about a protocol: transports, envelopes, error
// presentation, auth and tenancy (carried in the context) stay with the
// application. Tools are plain values, so an application may register them
// as they are, change a Description, or write its own around the same
// handlers.
//
// The tools are grouped by the server they suit:
//   - Design: list, define, inspect and validate record types, preview IDs;
//   - Query: read and count records, list their labels, and run read-only scripts
//     over them;
//   - Write: insert, update and delete records.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/jsonx"
	"github.com/fragshq/mneme/script"
)

// Tool is one operation.
type Tool struct {
	// Name is a stable snake_case identifier, e.g. "define_record_type".
	Name string
	// Description tells the caller (typically an LLM) what the tool does and
	// when to use it.
	Description string
	// InputSchema describes the JSON object Call accepts, with a description
	// for each property. It is always of type "object".
	InputSchema *jsonschema.Schema
	// Call validates input against InputSchema, runs the operation and
	// returns its result: a value that encodes to a JSON object. Input
	// problems match errs.ErrInvalid; see package mneme for the other
	// errors.
	Call func(ctx context.Context, input json.RawMessage) (any, error)
}

// Tool names.
const (
	ListRecordTypes    = "list_record_types"
	DefineRecordType   = "define_record_type"
	GetRecordType      = "get_record_type"
	ValidateRecordType = "validate_record_type"
	PreviewID          = "preview_id"
	ListRecords        = "list_records"
	CountRecords       = "count_records"
	GetRecord          = "get_record"
	ListLabels         = "list_labels"
	RunScript          = "run_script"
	InsertRecord       = "insert_record"
	UpdateRecord       = "update_record"
	DeleteRecord       = "delete_record"
)

// All returns every tool: Design, Query and Write.
func All(e *mneme.Engine, scripts script.Options) []Tool {
	out := Design(e)
	out = append(out, Query(e, scripts)...)
	return append(out, Write(e)...)
}

// Find returns the tool named name from ts.
func Find(ts []Tool, name string) (Tool, bool) {
	for _, t := range ts {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// shared describes input properties that several tools take, or whose
// description is too long for a struct tag. newTool applies an entry to any
// property of that name; schema, when set, replaces the inferred one, for
// types whose JSON form is more lenient than their Go type (query.Sorts).
var shared = map[string]struct {
	description string
	schema      *jsonschema.Schema
}{
	"filter":      {description: descFilter},
	"sort":        {description: descSort, schema: sortSchema()},
	"schema":      {description: descSchema},
	"id_strategy": {description: descIDStrategy},
}

// newTool builds a Tool whose input is decoded into In, after validation
// against the schema inferred from In: field descriptions come from the
// jsonschema struct tags or from shared, and fields without omitempty are
// required.
func newTool[In any](name string, description string, fn func(ctx context.Context, in In) (any, error)) Tool {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tools: schema for %s: %v", name, err))
	}
	for pname, ps := range s.Properties {
		if sh, ok := shared[pname]; ok {
			if sh.schema != nil {
				ps = sh.schema
				s.Properties[pname] = ps
			}
			ps.Description = sh.description
		}
	}
	resolved, err := s.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("tools: schema for %s: %v", name, err))
	}
	call := func(ctx context.Context, input json.RawMessage) (any, error) {
		if jsonx.IsNull(input) {
			input = json.RawMessage("{}")
		}
		generic := any(nil)
		if err := json.Unmarshal(input, &generic); err != nil {
			return nil, errs.Invalidf("%s: input is not valid JSON: %v", name, err)
		}
		if err := resolved.Validate(generic); err != nil {
			return nil, errs.Invalidf("%s: %v", name, err)
		}
		in := new(In)
		if err := json.Unmarshal(input, in); err != nil {
			return nil, errs.Invalidf("%s: %v", name, err)
		}
		return fn(ctx, *in)
	}
	return Tool{Name: name, Description: description, InputSchema: s, Call: call}
}

// convert re-decodes a loosely typed input value into out, strictly.
func convert(field string, in any, out any) error {
	if err := jsonx.Remarshal(in, out); err != nil {
		return errs.Invalidf("%s: %v", field, err)
	}
	return nil
}

// rawData encodes a record's data for the engine; nil becomes {}. data was
// decoded from JSON, so it always encodes.
func rawData(data map[string]any) json.RawMessage {
	if data == nil {
		return json.RawMessage("{}")
	}
	raw, _ := json.Marshal(data)
	return raw
}
