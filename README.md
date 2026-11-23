# Mneme

A Go library for defining **versioned data structures** with JSON Schema, storing records that conform to them, and querying those records. **The application owns storage.**

```go
mem := memstore.New() // or your own implementation of the store interfaces
eng, _ := mneme.New(mneme.Config{Schemas: mem, Records: mem, Sequences: mem})

eng.Define(ctx, schema.Definition{
	Name:       "risk",
	Title:      "Risk",
	IDStrategy: idgen.NewSpec(idgen.Fixed("RID-"), idgen.Sequence(idgen.SequenceOptions{Name: "rid", Pad: 4})),
	Schema: &schema.JSONSchema{
		Type:       "object",
		Required:   []string{"title"},
		Properties: map[string]*schema.JSONSchema{"title": {Type: "string"}},
	},
})

rec, _ := eng.Insert(ctx, "risk", json.RawMessage(`{"title":"Flooding"}`), "urgent") // rec.ID == "RID-0001"
f := query.Contains("/title", "Flood")
page, _ := eng.List(ctx, "risk", query.Query{Filter: &f, Limit: 20}) // page.Items, Limit, Total, HasMore, NextCursor
n, _ := eng.Count(ctx, "risk", &f)                                    // matching records, without reading them
labels, _ := eng.ListLabels(ctx, "risk", nil) // [{Label: "urgent", Count: 1}]
rec, _ = eng.Update(ctx, "risk", "RID-0001", json.RawMessage(`{"title":"Flooding in DC-1"}`)) // full replace
_ = eng.Delete(ctx, "risk", "RID-0001")                                                       // hard delete
_ = eng.DeleteRecordType(ctx, "risk")                                                         // the type, its records and counters
```

## Concepts

| | |
|---|---|
| **Record type** | A mnemonic name with one or more versions, each holding a JSON Schema and settings. |
| **Version** | Immutable. `Define` creates `latest+1`, or an explicit version greater than the latest. All versions are kept, and the latest is used for new inserts. |
| **Record** | Validated against the latest version on insert and on update. It stores `SchemaVersion`, `CreatedAt`, `UpdatedAt` and `Labels`, and reads return records of any version. |
| **Labels** | Optional technical tags supplied by the caller, outside the schema. They are stored as a sorted, de-duplicated set (always present, `[]` when empty), limited to 64 labels of up to 128 characters, filterable as `$labels`, and listed with their counts by `ListLabels`. |
| **Update** | A full replace of data **and labels**. The new data must conform to the **latest** schema, and the record moves to that version. The ID and `CreatedAt` never change. There is no concurrency control: the last writer wins. |
| **Delete** | A hard delete by ID. |
| **Delete a record type** | `DeleteRecordType` permanently removes a record type: every record, its ID sequence counters and every version of its definition. The name is then free: a new definition starts again from v1, and sequences from 1. No tool exposes it to LLMs. |
| **Quotas** | Optional caps in `Config`: `MaxRecordTypes` (a new record type fails with `ErrQuota` once the store holds that many; new versions are always allowed) and `MaxRecordsPerType` (an insert fails with `ErrQuota` once the type holds that many; updates are always allowed). 0, the default, means unlimited. They count what the store sees through the context, so a store with tenancy enforces them per tenant. They're checked before the write, not atomically with it, so concurrent writers can overshoot by a few. |
| **ID strategy** | Per record type, composed of segments. Defaults to UUIDv7. |
| **Storage** | Interfaces in `store/`. Tenancy and table layout are the application's business. Mneme passes `context.Context` through untouched. |

## Record type definitions

A `schema.Definition` is one version of a record type:

| Field | |
|---|---|
| `Name` | mnemonic name, `^[a-z][a-z0-9_-]{0,62}$` |
| `Version` | `0` assigns latest+1; an explicit value must be greater than the latest |
| `Title`, `Description` | documentation for humans, UIs and MCP tools |
| `IDStrategy` | `*idgen.Spec`; nil means UUIDv7 |
| `Indexes` | fields the application may index (informational) |
| `Schema` | `*schema.JSONSchema` (an alias of [`github.com/google/jsonschema-go`](https://pkg.go.dev/github.com/google/jsonschema-go/jsonschema)'s `Schema`); the root must be `"type": "object"` |

There are three ways to build the schema:

- **As a Go literal,** as in the example above.
- **Inferred from a Go type:** `jsonschema.For[MyStruct](nil)`. Fields without `omitempty` become required.
- **Parsed from JSON:** with `json.Unmarshal`, or `schema.MustParse` for constants.

The official Go MCP SDK uses the same schema type, so MCP tools can pass schemas through unchanged. Records are validated with `santhosh-tekuri/jsonschema` (draft 2020-12), which reports each violation with its JSON-pointer path. `Define` copies the definition, so changing your value afterwards has no effect.

### ID segments

An ID is the concatenation of its segments:

| Segment | Config | Renders |
|---|---|---|
| `fixed` | `"RID-"` | literal text |
| `sequence` | `"name"` or `{"name","pad","start","step","scope"}` | progressive counter from `SequenceProvider`; `"scope":"prefix"` keeps one counter per distinct preceding text (e.g. restart each year) |
| `uuid` | `"v7"` (default) or `"v4"` | UUID |
| `random` | `{"length":8,"alphabet":"A-Z0-9"}` | random chars; default alphabet is Crockford base32 |
| `date` | Go layout, e.g. `"2006"`, default `"20060102"` | creation time (UTC) |
| `field` | `"/region"` or `{"pointer","default"}` | a scalar from the record |

Build specs in Go with the constructors (`idgen.Fixed`, `Sequence`, `UUIDv7`, `UUIDv4`, `Random`, `Date`, `Field`, `FieldOr`). For example, the following renders `RID-2026-0001`:

```go
idgen.NewSpec(idgen.Fixed("RID-"), idgen.Date("2006"), idgen.Fixed("-"),
	idgen.Sequence(idgen.SequenceOptions{Name: "rid", Pad: 4, Scope: idgen.ScopePrefix}))
```

Its JSON form, which is how it is stored and how MCP tools exchange it, is:

```json
{"segments":[{"fixed":"RID-"},{"date":"2006"},{"fixed":"-"},{"sequence":{"name":"rid","pad":4,"scope":"prefix"}}]}
```

The set of kinds is fixed. An ID strategy is decoded strictly: an unknown key (say, `"segmentz"`) is an error, not the default strategy. On an ID conflict, Insert regenerates up to `MaxIDAttempts` times, unless every segment is deterministic. `PreviewID` renders a sample ID without consuming a sequence value.

## Querying

Filters are a JSON-serialisable AST:

```json
{"op":"and","filters":[
  {"op":"eq","field":"/status","value":"open"},
  {"op":"not","filters":[{"op":"exists","field":"owner.id"}]}]}
```

- **Ops:** `eq ne gt gte lt lte in contains prefix exists and or not`.
- **Fields:** JSON pointers (`/owner/id`), dotted shorthand (`owner.id`), or the meta fields `$id`, `$version`, `$createdAt`, `$updatedAt` (the two timestamps compare as time, with values given as RFC 3339 strings) and `$labels`, an array of strings. Filter labels with `contains`, for example `{"op":"contains","field":"$labels","value":"urgent"}`; combine with `and`/`or`/`not` for all-of, any-of and none-of.
- **Across versions:** a filter on a field that a record lacks never matches, except through `not` and `exists`. `ne` therefore skips records without the field, while `not eq` includes them.
- **Sorting:** `Query.Sort` (a `query.Sorts`) accepts in JSON a field name (`"total"`), a field name prefixed with `-` for descending (`"-total"`), a `{"field":"total","desc":true}` object, or an array mixing those. The same form works everywhere: in `list_records`, in scripts and in your own protocols. The default is `$createdAt` ascending, with `$id` always added as a tiebreaker. Values order by type (null < string < number < bool < array < object), and missing values sort last when ascending.
- **Pagination:** keyset cursors (`Page.NextCursor`). Each cursor is bound to its sort and stays stable when records are inserted. Every page also says where it stands: `{"items": [...], "limit": 50, "total": 123, "hasMore": true, "nextCursor": "..."}`. `limit` is the page size actually applied (the default when none was asked, capped at the maximum), `total` counts every record matching the filter across all pages (ignoring the cursor), and `hasMore` is true exactly when `nextCursor` is set. `total` is computed with a separate store call, so concurrent writes can make it differ slightly from what paging returns.
- **Counting:** `eng.Count(ctx, "risk", filter)` returns how many records match `filter` (`nil` for all), without reading them.
- **Distinct labels:** `eng.ListLabels(ctx, "risk", filter)` returns the labels in use among the records matching `filter` (`nil` for all), each with how many of those records carry it, sorted by label in byte order: `[{"label":"urgent","count":2}]`.
- **Strict field checks:** `Config.StrictFilterFields` rejects fields that no schema version declares, in `List`, `Count` and `ListLabels`.

The full reference semantics are documented in package `query` and implemented by `query.Match` and `query.Sort`.

## Scripting

Package `script` runs read-only JavaScript (via [goja](https://github.com/dop251/goja)) over the records, for joins, aggregates and reshaping that filters can't express. It is a separate package, so applications that don't use it don't pull in a JS engine.

```go
res, err := script.Run(ctx, engine, code, script.Options{}) // res.Value is JSON, res.Logs the console output
```

Scripts are written the way an LLM writes them unprompted. The code is the body of an async function: top-level `return` and `await` work, and without a `return` the last expression is the result. The globals are:

- `list(type, {filter, sort, limit})`: every matching record as an array; paging happens internally. `sort` also accepts `"field"`/`"-field"`.
- `get(type, id)`: one record, or `null`.
- `types()` and `describe(type)`.
- `console.log`.

Records have their JSON form, with the payload under `data`. There are no writes, I/O, timers or modules, and every run gets a fresh runtime.

`script.Guide` documents all of this for the script's author. Use it as the description of the MCP tool that wraps `Run`.

**Limits** are set in `script.Options`. The defaults are 5s of wall-clock time (store calls included), 10,000 records read, 1,000 store calls, 1 MiB of output and 100 log lines. Exceeding one stops the script; the script cannot catch it. Errors:

| error | meaning | catchable in JS |
|---|---|---|
| `script.ErrScript` | syntax error, uncaught exception, bad arguments, unserializable result | bad arguments and unknown types are thrown as `Error`s |
| `script.ErrLimit` | a limit was exceeded | no |
| store errors, caller cancellation | returned unchanged | no |

`ErrScript` and `ErrLimit` both match `ErrInvalid`. `Result.Logs` is filled even when `Run` fails.

## Implementing storage

Implement `store.Store`, which combines `store.SchemaStore`, `store.RecordStore` and `store.SequenceProvider`, then run the conformance suite:

```go
func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		return newEmptyStore(t)
	})
}
```

A store that supports tenancy takes a `store.TenantFunc` option. A tenant is a list of values, one per tenancy field: `["acme"]`, or `["acme", "alice"]` for an organization plus a user. Each full combination is one isolated space, with its own record types, records and sequences. A missing tenant, or one with an empty value, fails closed with `ErrNoTenant`.

```go
tenant := func(ctx context.Context) []string {
	c := claimsFrom(ctx) // e.g. a verified JWT
	return []string{c.OrganizationID, c.UserID}
}
```

How a store keeps the fields is its own business. Postgres gives each one a column (`TenantColumns`); Redis and memory encode the whole tenant as one key (`store.TenantKey`).

Such a store should also pass the tenancy suite. The suite supplies the tenant function itself, with one tenancy field and then with two, and names the fields so a store can map them. It also runs the whole conformance suite with every call scoped to one tenant:

```go
func TestTenancy(t *testing.T) {
	storetest.RunTenancy(t, func(t *testing.T, tenant store.TenantFunc, fields []string) store.Store {
		return newEmptyStore(t, Options{Tenant: tenant, TenantFields: fields})
	})
}
```

Helpers for implementers:

- `store.Tenant(ctx, fn)` resolves the tenant and fails closed with `store.ErrNoTenant` when tenancy is on but the context names no tenant, or a partial one.
- `store.TenantKey` encodes a tenant as one string, for stores keyed by strings; distinct tenants always get distinct keys (`["a/b","c"]` and `["a","b/c"]` differ). `store.ParseTenantKey` reverses it.
- `Record.Normalized()` returns a record in the form reads must return: UTC timestamps, non-nil labels, no shared memory.
- For stores without native JSON querying, `store.Paginate`, `store.Count` and `store.CountLabels` compute `ListRecords`, `CountRecords` and `ListLabels` in Go with the reference semantics. They work on `store.Doc`s (records decoded once, see `store.NewDocs`), which a store may keep between calls.
- `store.RecordNotFound`, `RecordExists`, `TypeNotFound`, `VersionNotFound` and `VersionExists` build the errors stores return, with the same wording everywhere.
- `storetest.Engine` and `storetest.IDs` help in your own tests; `storetest.WithTenant` and `storetest.TenantOf` carry a tenant in a context; `storetest.CallsWithoutTenant` lets a store check its backend directly after the rejected calls.

Mneme is storage agnostic. It ships only `store/memstore`, an in-memory store that defines the reference behaviour, and has no database dependencies. Real backends are separate plugin modules that implement the interfaces above (see `../mneme-plugins`): Postgres, Redis, and an in-memory backend with tenancy for deployments that need no persistence.

## Errors

Test errors with `errors.Is` against `mneme.ErrNotFound`, `ErrConflict`, `ErrInvalid`, `ErrNoTenant` and `ErrQuota`. `ErrQuota` means a write would exceed a configured quota; the input is fine, so it doesn't match `ErrInvalid`, and deleting data makes room again. `ErrNoTenant` means a store has tenancy on but the context names no tenant. It's a deployment problem, so it doesn't match `ErrInvalid`. Schema violations are `*schema.ValidationError`, which lists each issue with its JSON-pointer path and also matches `ErrInvalid`.

## Tools (MCP and other protocols)

Mneme ships no protocol code. Package `tools` describes its operations as protocol-neutral tools, so an application can expose them over MCP, HTTP, a CLI or anything else without rewriting what each one does or how a model should use it. Each `tools.Tool` has:

- a `Name`, such as `define_record_type`;
- a `Description` written for an LLM;
- an `InputSchema` (JSON Schema, always `type: object`) with a description for each property;
- `Call(ctx, json.RawMessage) (any, error)`, which validates the input against the schema, runs the operation and returns a JSON-serializable result.

| set | tools |
|---|---|
| `tools.Design(engine)` | `list_record_types`, `define_record_type`, `get_record_type`, `validate_record_type`, `preview_id` |
| `tools.Query(engine, scriptOptions)` | `list_records`, `count_records`, `get_record`, `list_labels`, `run_script` (its description is `script.Guide`) |
| `tools.Write(engine)` | `insert_record`, `update_record`, `delete_record` |

`tools.All` returns all three sets. Tools are plain values, so you can register a set as it is, pick tools with `tools.Find`, or change a `Description` or property description before registering. The input types (`DefineRecordTypeInput` and so on) are exported for applications that want to write their own handlers. The sets take a `*mneme.Engine`. Every tool returns a JSON object: engine values such as records and pages, or small named results such as `{"recordTypes": [...]}`, `{"labels": [...]}` and `{"count": 3}`.

The protocol side stays with the application: transport, envelopes, how errors are presented, auth, and tenancy (carried in the `context.Context`). Input problems match `ErrInvalid`. Other errors are the engine's own, and a store failure comes back unchanged. With the official Go MCP SDK, the whole mapping is a loop:

```go
ts := tools.All(engine, script.Options{})
server := mcp.NewServer(impl, &mcp.ServerOptions{Instructions: tools.Instructions(ts)})
for _, t := range ts {
	server.AddTool(&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			out, err := t.Call(ctx, req.Params.Arguments)
			// render out as JSON text, or err as an isError result
		})
}
```

The descriptions encode the guidance a model needs. For example, `define_record_type` steers the model toward the default UUIDv7 ID strategy and warns that a redefinition must repeat a custom strategy. If you write your own descriptions, keep that guidance.

Guidance that spans several tools is in `tools.Instructions(ts)`: how record types, versions and labels work, and which tool fits a task (check existing types before defining one, `count_records` for "how many", `run_script` for joins and aggregates, updates replace the whole record). Pass it as the MCP server instructions, or put it in a system prompt. It describes only the sets present in `ts`, so a read-only server built from `tools.Query` never mentions writes.

## Not yet in scope

These are planned but not built:

- schema deprecation
- compatibility enforcement (the `Config.Compatibility` hook exists)
- migrations
