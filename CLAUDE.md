# CLAUDE.md

Guidance for working in this repository. The original product brief is in `mneme.md`, and the user-facing documentation is in `README.md`.

## What Mneme is

Mneme is a Go **library**, not a service. It is embedded by an application, called **the enclosure**. With it, the enclosure can:

1. **Define record types.** A record type has a mnemonic name, a JSON Schema, an ID strategy and metadata. Definitions are **generational**: every redefinition creates a new immutable version, all versions coexist, and **the latest wins** for new inserts.
2. **Consult definitions.** The enclosure can read any version, list versions, or get a description merged across versions.
3. **Insert records.** The caller names the record type, the data is validated against the **latest** schema, an ID is generated, and the record is stored **with the schema version it was validated against**.
4. **Read records.** Records come back by ID, or as a filtered, sorted, cursor-paginated list. Records may conform to **any** version, and filters work across all of them.

These boundaries are deliberate and must hold:

- **Mneme never owns storage.** It only declares interfaces in `store/`, and the enclosure implements them, dependency-injection style. Tenancy, table layout (shared table vs table-per-type) and column design must stay invisible to Mneme. The `context.Context` is passed through untouched to every store call, and that is how enclosures carry tenancy.
- **Mneme ships no MCP (or any protocol) code.** The enclosure exposes the MCP servers: one to design data structures, one to query data. Mneme's job is to make those servers thin wrappers.
  - Package `tools` provides protocol-neutral tools: names, LLM-facing descriptions, input JSON Schemas, and JSON handlers. An MCP server maps them in a loop.
  - Every input and output has a clean JSON form.
  - The helpers `DescribeRecordType`, `ValidateDefinition` and `PreviewID` exist for this purpose too.

## Commands

```sh
# core library (no external services needed)
go build ./...
go vet ./...
go test -race ./...
gofmt -l .                      # must print nothing
```

## Layout

```
mneme.go           Engine, Config, New, compile cache, re-exported errors (ErrNotFound/ErrConflict/ErrInvalid/ErrNoTenant/ErrQuota), Record alias
recordtypes.go     Define, ValidateDefinition, GetRecordType, ListRecordTypes, ListVersions, DescribeRecordType
records.go         Insert (validate → generate ID → store, retry on conflict), Update (full replace), Delete (hard),
                   PreviewID, Get, List, Count, ListLabels
labels.go          label normalization and limits (MaxLabels, MaxLabelLength)
errs/              sentinel errors shared by all packages (avoids import cycles)
jsonptr/           RFC 6901 JSON Pointer: Parse, String, Get, FromDotted (general-purpose)
jsonx/             JSON helpers: IsNull, Kind, DecodeStrict, Remarshal, StringOr, Equal (general-purpose)
idgen/             composable ID strategies: Spec/SegmentSpec (JSON form), Compile, Generator,
                   built-in segments (segments.go), Go constructors (build.go)
schema/            Definition (record type version), JSONSchema alias, Compile/Validate,
                   Field discovery + MergeFields, ValidationError
query/             Filter AST + Normalize (filter.go), Query/Page/Normalize (query.go),
                   reference semantics Match/Sort (eval.go), keyset Cursor (cursor.go), ParseFilter
script/            read-only JavaScript over records (goja): Run, Options/limits, Guide (LLM-facing docs);
                   bindings.go = list/count/get/types/describe/console. Core never imports it.
tools/             protocol-neutral tools (Name, Description, InputSchema, Call) in Design/Query/Write sets;
                   descriptions.go holds the LLM-facing text. Core never imports it.
store/             storage interfaces (store.Store = all three) + Record + Doc adapter (query.Doc over a Record) +
                   Paginate/Count/CountLabels over Docs (ListRecords/CountRecords/ListLabels in Go — for stores
                   without native JSON querying), NewDocs, error constructors (errors.go)
store/memstore/    in-memory implementation — THE reference behaviour
store/storetest/   conformance suite every store must pass (storetest.Run)
```

The module path is currently the placeholder `mneme`. The user has not chosen the final path yet, so don't change it unless asked.

## Core types and flow

- **`schema.Definition`** has the fields `Name`, `Version`, `Title`, `Description`, `IDStrategy *idgen.Spec`, `Indexes []string`, `Schema *schema.JSONSchema` and `CreatedAt`.
  - `schema.JSONSchema` is an alias for `github.com/google/jsonschema-go/jsonschema.Schema`, a typed, authorable struct. The official Go MCP SDK uses the same type, which is why it was chosen.
  - **Do not go back to `json.RawMessage` for schemas.** The user explicitly rejected that as "ugly and unpractical".
  - Mneme settings live as **fields on the Definition**, not inside the schema. The `x-mneme` extension keyword was removed.
- **Define:**
  1. Validate the name against `^[a-z][a-z0-9_-]{0,62}$`.
  2. Deep-copy the input with `Definition.Clone`, a JSON round trip.
  3. Resolve the version: `0` means latest+1, and an explicit value must be greater than the latest or the call returns `ErrConflict`. Creating a **new** type checks `Config.MaxRecordTypes` (`ErrQuota`); new versions never do.
  4. Set `CreatedAt`.
  5. Compile the schema and the ID strategy, and fail with `ErrInvalid` if either is invalid.
  6. Run the optional `Config.Compatibility` hook.
  7. Call `PutDefinition` and cache the compiled form.
- **Compilation** (`schema.Compile`) marshals the typed schema to JSON and compiles it with `github.com/santhosh-tekuri/jsonschema/v6`, imported under the alias `validator`. It uses draft 2020-12 and never resolves remote `$ref`s. Validation uses this library because it reports structured issues with instance paths. The root schema must be `Type: "object"`.
- **Engine cache:** a `sync.Map` keyed by a SHA-256 of the definition JSON, **not** `(name, version)`: stores may scope definitions by tenant through the context, so two tenants sharing one Engine can hold different definitions under the same name and version (`TestCompileCacheIsolatesTenants`). Entries never go stale. `Insert` still fetches the *latest* definition from the store each time, because another process may have defined a newer version.
- **Insert:**
  1. Validate the data against the latest version (so a bad record reports `ErrInvalid` before any quota), and compact the JSON.
  2. Check `Config.MaxRecordsPerType` with `CountRecords` (`ErrQuota`). Update never checks it.
  3. Generate the ID from `idgen.GenContext`: record type, decoded data, now and sequences.
  4. Call `InsertRecord`.
  5. On `ErrConflict`, regenerate and retry up to `MaxIDAttempts`. There is no retry if the generator is fully deterministic.
- **List and Count:** `List` returns a `query.Page` with `Items`, `Limit` (the page size applied after default/clamp), `Total` (records matching the filter, cursor ignored), `HasMore` (= `NextCursor != ""`) and `NextCursor`. Stores fill only `Items` and `NextCursor`; the engine adds the rest, getting `Total` from a separate `CountRecords` call (so it isn't atomic with the page). Pagination stays keyset-only: the user chose cursors over offsets, and every page always carries `Total`. `Engine.Count(ctx, type, filter)` is the standalone count (normalizes the filter and runs `checkFields`, like `ListLabels`, through the shared `checkFilter`), exposed as the `count_records` tool (Query set) and the script binding `count(type, filter)`.
- **Labels:** `Record.Labels` is technical metadata outside the schema, supplied by the caller.
  - `normalizeLabels` (`labels.go`) turns them into a **sorted, de-duplicated, never-nil** set and rejects empty strings, more than `MaxLabels` (64) labels, or labels longer than `MaxLabelLength` (128 characters).
  - The API is `Insert(ctx, type, data, labels...)` and `Update(ctx, type, id, data, labels...)`.
  - Filtering uses the meta field `$labels`, an array of strings. Use `contains` for one label, `eq` for an exact set, and `and`/`or`/`not` to combine.
  - `Engine.ListLabels(ctx, type, filter)` returns the distinct labels of the matching records (nil filter = all) as `[]LabelCount{Label, Count}`, sorted by label in **byte order**, never nil. It normalizes the filter and applies `StrictFilterFields` like `List` (shared `checkFields`). It is exposed as the `list_labels` tool (Query set).
  - Every store keeps them next to the timestamps: the `labels` key in Redis, a `labels jsonb` column in Postgres.
- **Update:**
  1. Validate the new data against the **latest** version.
  2. Load the current record (`ErrNotFound` if it's missing).
  3. Set `SchemaVersion` to the latest, replace `Data` **and `Labels`** (labels left out are removed), and set `UpdatedAt` to now.
  4. Call `UpdateRecord`.

  This is a **full replace**, not a patch. Updating an old record migrates it to the latest version. The ID and `CreatedAt` never change, and a `field` ID segment is not re-rendered. There is **no concurrency control**; the last writer wins, as the user decided.
- **Quotas** (`MaxRecordTypes`, `MaxRecordsPerType`; 0 = unlimited) count what the store sees through the context, so they're per tenant with no tenancy code in the core. They're checked before the write, not atomically: concurrent writers may overshoot a little (a deliberate trade-off for a safety cap: exact enforcement would need a store-level, per-backend mechanism). `ErrQuota` doesn't match `ErrInvalid` (the input is fine). Tested in `quota_test.go`.
- **Delete:** a **hard** delete by ID through `DeleteRecord`. A deleted ID can be inserted again, but sequences never hand out the same value twice.
- **DeleteRecordType** (`recordtypes.go`): removes a whole record type: `DeleteRecords`, then `DeleteSequences` (when a `SequenceProvider` is configured), then `DeleteDefinitions`, so a failure part-way leaves the type defined and the call can be repeated. `ErrNotFound` for an unknown type. The name is free afterwards: versions and sequences restart from 1 (the user decided counters go with the data). It is not isolated from concurrent writes. It's deliberately **not** a tool: LLMs can't wipe a type; only the workbench dashboard exposes it.
- **Time:** `Engine.now()` truncates to **microseconds** in UTC, the precision of typical SQL timestamp types, so the record returned by `Insert` equals what such stores keep. Keep this behaviour.

## ID strategies (`idgen`)

The ID strategy is a **per-record-type preference** that the user composes from segments. The user insisted on this. The rendered segments are concatenated.

| kind | JSON config | notes |
|---|---|---|
| `fixed` | `"RID-"` | deterministic |
| `sequence` | `"name"` or `{name,pad,start,step,scope}` | needs a `SequenceProvider`; value = `start + (n-1)*step`; `scope:"prefix"` = one counter per distinct text rendered before it (e.g. per year) |
| `uuid` | `"v7"` (default) / `"v4"` | |
| `random` | `{length, alphabet}` | `alphabet` supports ranges such as `A-Z0-9`; default is Crockford base32; uses `crypto/rand` |
| `date` | Go layout, default `"20060102"` | UTC creation time; deterministic |
| `field` | `"/ptr"` or `{pointer, default}` | a scalar taken from the record; deterministic |

- **JSON form:** a segment is a **single-key object** `{"<kind>": <config>}`, and `SegmentSpec` has custom (Un)MarshalJSON for it.
- **Go form:** the constructors in `build.go` (`NewSpec`, `Fixed`, `Sequence(SequenceOptions{...})`, `UUIDv7`, `UUIDv4`, `Random`, `Date`, `Field`, `FieldOr`).
- **Default:** a nil or empty spec means a single UUIDv7 segment (`DefaultSpec`).
- **Validating configs:** segment factories decode configs strictly and return plain errors; `idgen.Compile` wraps them as `ErrInvalid`. `Spec` itself decodes strictly too (an unknown key such as `"segmentz"` is an error).
- **Segments** are values of the unexported `segment` struct: a `render` func plus `deterministic` (disables conflict retries) and `needsSequences` (makes `Define` fail if `Config.Sequences` is nil).
- **The set of kinds is fixed.** The user dropped custom kinds (the old `Registry`/`Config.IDSegments`): nothing used them.
- **`PreviewID`** uses a fake provider that always returns 1, so it never consumes a counter value.

**Adding a built-in segment kind:**
1. Write the factory in `segments.go`, validating the config there. A config with an object form is a named type shared with the constructor (as `SequenceOptions`, `randomConfig`, `fieldConfig` are).
2. Add it to the `factories` table in `generator.go`.
3. Add a constructor in `build.go`.
4. Add tests in `idgen_test.go`, including invalid configs in `TestInvalidSpecs` and a case in `TestConstructorsMatchJSONForm`.
5. Document it in `README.md`.

## Querying (`query`) — the semantics are a contract

- **Field references:** a JSON pointer (`/owner/id`), dotted shorthand (`owner.id`), or a meta field (`$id`, `$version`, `$createdAt`, `$updatedAt`, `$labels`).
- **Normalization:** `Normalize` converts every field to canonical form, either a pointer string or a meta field. **Stores only ever receive normalized queries.**
- **Sort normalization:** `Query.Normalize` defaults the sort to `$createdAt` ascending and **always appends `$id`** as a unique tiebreaker. It also clamps `Limit`.
- **Missing fields never match**, except through `not` and `exists`. So `ne` skips records that lack the field, while `not(eq)` includes them. This is what makes filtering across schema versions coherent.
- **Equality:** `eq`, `ne` and `in` use JSON equality, so `1` equals `1.0`.
- **Ordered comparisons:** `gt`, `gte`, `lt` and `lte` compare number with number, or string with string in **byte order**. Mismatched types never match. `$createdAt` and `$updatedAt` compare as time, with values given as RFC 3339 strings (`query.IsTimeField`).
- **`contains`:** true for an array element equal to the value, or for a string substring.
- **`prefix`:** applies to strings only.
- **Sort JSON is lenient:** `query.Sorts` accepts `"field"`, `"-field"`, `{field, desc}`, or an array mixing those. Tools, scripts and protocols all use it, so don't write another sort parser.
- **Total sort order:** null < string < number < bool < array < object < **missing**. Missing sorts last when ascending and first when descending, which matches SQL's default NULL ordering.
- **Cursors:** keyset cursors (`query.Cursor`) are base64 JSON holding the last item's sort keys plus a signature of the sort. `DecodeCursor` rejects a cursor whose sort signature doesn't match the query. Cursors are **client-supplied and untrusted**.
- **Reference implementation:** `query.Match`, `query.Sort`, `CursorFor` and `Cursor.After`, used by memstore through `store.Doc`.
- **Filter JSON** is parsed by `query.ParseFilter` (null/blank = no filter, strict decode, `ErrInvalid`) in tools, scripts and protocols alike, so don't write another filter decoder.

**Adding or changing a filter op or semantic:** update every one of these places in the same change:
1. `filter.go`: the constant, the constructor and the validation in `Normalize`.
2. `eval.go`: the reference behaviour.
3. Every plugin that translates queries natively, such as the SQL translation in `../mneme-plugins/mneme-postgres-storage/sql.go`.
4. `store/storetest`: cases in `testFilters`.
5. The `README.md` query section.

If memstore and a plugin disagree, the conformance suite is how you find out.

## Scripting (`script`)

`script.Run(ctx, reader, code, opts)` runs read-only JS over records. `Reader` is the read part of `*mneme.Engine`.

- **The input must look like what an LLM writes unprompted.** The user asked for this, so keep it true:
  - The code is wrapped as an async function body, so top-level `return` and `await` work.
  - When the last statement is an expression, it is rewritten in the AST (not as text) into a `return`.
  - `list` returns **all** matches as a plain array (it pages internally), because LLMs forget to paginate.
  - `sort` accepts `"-field"`.
  - Records keep their JSON form (`data` holds the payload).
- **Only read bindings:** `list`, `count` (reads no records, one store call), `get` (`null` when missing), `types`, `describe`, `console.*`. Never bind writes.
- **Limits** (`Options`): timeout (5s), records read, store calls, output bytes, log lines.
  - A breach calls `vm.Interrupt`, which is uncatchable, and returns `ErrLimit`.
  - The timeout is `context.WithTimeoutCause` and also reaches store calls.
- **Errors:**
  - Caller mistakes (`ErrInvalid`/`ErrNotFound` from the engine, bad arguments) are thrown as plain JS `Error`s the script can catch. A run that fails because of them returns `ErrScript`.
  - Other store errors and caller cancellation abort the run and come back unchanged.
- **Guide** is the LLM-facing doc. `TestGuideExampleRuns` executes its example, so update both together.
- **Changes to the query JSON or record JSON** must be reflected in `Guide` and `listArgs`. Sorting uses `query.Sorts`, so it stays the same in scripts and tools.

## Tools (`tools`)

These are the operations exposed to LLMs (and to any protocol) as **protocol-neutral** tools. The user's rule: descriptions and schemas live in Mneme so every client reuses them, while **nothing protocol-specific** (MCP SDK, transports, envelopes, error rendering) ever enters Mneme.

- **Vocabulary is "record type" / "record"** in tool names (`define_record_type`, `insert_record`...), parameters (`record_type`) and text.
- **Schemas** are inferred from the exported input structs with `jsonschema.For`:
  - a field's `jsonschema` tag is its description, and a field without `omitempty` is required;
  - unknown properties are rejected.
  - Descriptions too long or too shared for a tag (`filter`, `sort`, `schema`, `id_strategy`) live in the `shared` table in `tools.go`, which `newTool` applies to any property of that name (`sort` also gets its lenient schema there). A test fails if any property lacks a description.
- **The sets take `*mneme.Engine`** (the user dropped the `tools.Engine` interface). `script.Reader` stays an interface: it is what keeps scripts read-only.
- **Every tool returns a JSON object** (tests enforce it): engine values, or the named result structs in `inputs.go` (`RecordTypeList`, `LabelList`, `Count`, `Validation`, `PreviewedID`, `Deleted`, `RecordTypeDetails`). MCP enclosures can then put any result in `structuredContent` as is.
- **`Call`** validates the raw JSON against the schema, decodes it and runs the handler. Every input problem matches `ErrInvalid`. For `run_script` errors, the console output is appended to the error message.
- **Guidance lives in the descriptions,** e.g. "omit `id_strategy` unless the user asked for an ID format". Keep it when editing them.
- **Cross-tool guidance lives in `Instructions(ts)`** (`instructions.go`): how record types, versions and labels work, and which tool to pick for a task (design order, count vs list vs script, full-replace updates). Each section appears only when its set's tools are in `ts`, and `TestInstructionsMentionOnlyPresentTools` fails if the text names a tool that isn't offered. MCP enclosures pass it as server instructions. When adding or renaming a tool, check whether it belongs there.
- **Adding an engine operation** that clients should expose: add an input type, a description and a `newTool` entry in the right set, and test it in `tools_test.go`.

## Storage contracts (`store`)

- **`SchemaStore`:** `PutDefinition` (returns `ErrConflict` on a duplicate name+version), `GetDefinition` (version `0` means latest, and it returns `ErrNotFound`), `ListDefinitions` (latest of each type, sorted by name), `ListVersions` (oldest first, `ErrNotFound` if the type is unknown), `DeleteDefinitions` (every version; `ErrNotFound` if unknown).
  - Every Definition field must round-trip, and the suite compares the JSON.
- **`RecordStore`:**
  - `InsertRecord`: returns `ErrConflict` on a duplicate record type + ID. The same ID in different types is allowed.
  - `GetRecord`: returns `ErrNotFound`.
  - `UpdateRecord`: replaces the record exactly as given, including `CreatedAt`. It returns `ErrNotFound` if the record is missing, and **must not create it**.
  - `DeleteRecord`: a hard delete. It returns `ErrNotFound` if the record is missing.
  - `DeleteRecords(ctx, recordType)`: removes every record of the type; no error when there are none.
  - `ListRecords`: fills only `Items` and `NextCursor`.
  - `CountRecords(ctx, recordType, filter)`: `filter` is normalized or nil. It returns the number of matching records, and 0 for an unknown type. Postgres runs one `count(*)` over the same `matching` conditions; Redis and memory use `store.Count`.
  - `ListLabels(ctx, recordType, filter)`: `filter` is normalized or nil. It returns distinct labels with counts in byte order, and an empty (non-nil) slice for an unknown type. Postgres runs one `jsonb_array_elements_text` + `GROUP BY` query ordered with `COLLATE "C"`; Redis and memory use `store.CountLabels`.
  - Records carry `CreatedAt`, `UpdatedAt` and `Labels`, and all must round-trip. Empty labels must read back as `[]`, never nil.
  - `ListRecords` must return `NextCursor` **only when more items exist**, so an exact multiple of the page size has no empty tail page.
  - Cursors must stay stable when records are inserted.
- **`SequenceProvider`** (`store.SequenceProvider` embeds `idgen.SequenceProvider`):
  - `Next(ctx, recordType, name, scope)` returns 1, 2, 3… per distinct triple, and must be safe for concurrent use with no gaps or duplicates.
  - `DeleteSequences(ctx, recordType)` removes every counter of the type (any name and scope); no error when there are none.
- **Errors:** stores return the `store.ErrNotFound`/`ErrConflict`/`ErrNoTenant` sentinels (`ErrQuota` is the engine's own, never a store's), possibly wrapped. These are the same values as `errs.*` and `mneme.*`. Build not-found and conflict errors with the constructors in `store/errors.go` (`RecordNotFound`, `RecordExists`, `TypeNotFound`, `VersionNotFound`, `VersionExists`), so every store words them alike.
- **Tenancy** (optional per store): take a `store.TenantFunc` option and resolve it with `store.Tenant(ctx, fn)`, which fails closed with `store.ErrNoTenant`. `ErrNoTenant` is a deployment problem and does **not** match `ErrInvalid`.
  - A tenant is `[]string`, one value per tenancy field (e.g. organization + user). The user decided the semantics are **strict**: each full combination is one isolated space (record types, records, sequences); there is no hierarchy or prefix read. A nil/empty result or any empty value is `ErrNoTenant`.
  - Stores keyed by one string encode it with `store.TenantKey` (escaped values joined by `/`, injective) and decode with `store.ParseTenantKey`. Postgres keeps one column per field instead.
- **Reads** return `Record.Normalized()`: UTC timestamps, non-nil labels, no shared memory. Use it instead of re-implementing those rules.
- **Conformance:**
  - Every store implementation must pass `storetest.Run`. The factory must return **empty** stores for each subtest. When you change behaviour, extend the suite first.
  - Stores with tenancy also pass `storetest.RunTenancy`. Its factory receives the `TenantFunc` to plug in and the field names (`fields`), and it runs everything with one field and with two, including tenants that differ in one field only and pairs a naive encoding would confuse.
  - `storetest.Engine` and `storetest.IDs` are shared helpers for store tests.

### memstore
This is the reference implementation, protected by an RWMutex. It deep-copies definitions on put **and** on every read, so callers can't mutate stored versions. Records are stored as `*store.Doc`, decoded **once** on write and **never modified**: a write replaces the Doc. `ListRecords` snapshots the Docs under the lock (`snapshot`), runs `store.Paginate` outside it, and copies (`Normalized`) only the returned page. `ListLabels` and `CountRecords` take the same snapshot and use `store.CountLabels` / `store.Count`. Keep Docs immutable, because concurrent lists share them. `BenchmarkListPage` measures the list path.
- The plugin `mneme-memory-storage` wraps memstore, so improvements belong here and reach both.

### Plugins (`../mneme-plugins/`)
Storage backends that live outside this repo, each in its own Go module with `replace mneme => ../../mneme`.

- **`mneme-memory-storage`** (package `memorystore`):
  - Nothing is persisted. Each tenant gets its own core `memstore.Store`, so its semantics are the reference by construction. Don't re-implement query logic there.
  - `Options.Tenant` (a `store.TenantFunc`) turns on tenancy, which fails closed like the others. Tenants are created on their first write (`PutDefinition`, `InsertRecord`, `Next`), never by reads; updates and deletes count as reads. Every method is one line through the generic `on`/`do` helpers, whose `read`/`write` flag says whether the call may create the tenant. Tenants are keyed by `store.TenantKey`. `Tenants(ctx)` (`[][]string`, sorted, like the other plugins') and `DropTenant(values...)` manage memory.
  - **Tests:** `storetest.Run` (with tenancy off and on) and `storetest.RunTenancy`. No external services.
  - The workbench exposes it as `MNEME_STORAGE=memory`.

- **`mneme-redis-storage`** (package `redisstore`):
  - **Key space:** every key sits under a hash tag `{prefix[:namespace]}`, the namespace being `store.TenantKey(tenant)`, which escapes braces so the tag always ends where the namespace does. `Options.Tenant` (a `store.TenantFunc`, named like the other plugins' option) isolates tenants; `nsFor` builds the namespace. When it is set, an empty tenant fails with `store.ErrNoTenant` (fail closed, as the user decided). `TestTenancy` runs `storetest.RunTenancy`, and `TestRejectedCallsTouchNoKeys` replays `storetest.CallsWithoutTenant` and checks that they create or change no key.
  - **Atomic writes:** definitions go through a Lua script, records through `HSETNX`, sequences through `HINCRBY`.
  - **`Tenants(ctx)`** (`tenants.go`) lists the tenants (`[][]string`, decoded with `store.ParseTenantKey`; an empty tenant = bare `{prefix}`) holding record types by `SCAN`ning for `{prefix:*}:latest`, on every master of a cluster. It's plugin API for admin tools, not part of the core contracts: tenancy stays invisible to Mneme.
  - **Queries:** `ListRecords` and `ListLabels` read the type's hash with `HSCAN` (`scanRecords`) and call `store.Paginate` / `store.CountLabels`, so each call costs O(n).
  - **Tests:** the conformance suite runs against miniredis always, and also against real Redis when `MNEME_REDIS_ADDR` is set.

- **`mneme-postgres-storage`** (package `pgstore`):
  - **Storage:** Postgres via pgx v5. `New(ctx, db, opts)` takes a `pgstore.DB` (`*pgxpool.Pool`, or a `pgx.Tx`). Technical fields are columns (the tenant columns, `id`, `schema_version`, `created_at`, `updated_at`, `labels jsonb`), and the payload is `data jsonb`.
  - **Layouts:** set with `Options.Layout`.
    - `TablePerType`: a table per record type, created in the same transaction as the type's definition.
    - `SharedTable`: one table with a `record_type` discriminator.
  - **Tenancy:** decided at bootstrap. `Options.TenantColumns` (default `["tenant"]`) names one `text COLLATE "C"` column per tenancy field; together they lead every primary key and index, in every table. `New` rejects non-identifiers, duplicates and technical column names. Without `Options.Tenant`, every tenant column is `''`. With it, `Tenant(ctx)` fills them, scoping definitions, records and sequences; a tenant with the wrong number of values is `ErrNoTenant`. Every query builds its tenant conditions with `tenantWhere`, and inserts with `tenantArgs`. An empty result fails with `store.ErrNoTenant` before any SQL runs. The user decided this fails closed: never fall back to `''` when tenancy is on. The tenancy test variants use a fixed tenant (`fixedTenant`), since their tests run shared code with a bare context (conformance, plan checks). `TestTenancy` runs `storetest.RunTenancy` in both layouts.
  - **`Tenants(ctx)`** (`tenants.go`) lists tenants holding record types from the definitions table, as `[][]string` (all-`''` rows = an empty tenant). Plugin API for admin tools, like Redis'.
  - **Indexes:** with `TablePerType`, a definition's `Indexes` become jsonb and `COLLATE "C"` text expression indexes. With `SharedTable` they are validated but create **nothing**: the user ruled out per-field indexes on the shared table, because they would pile up with every type and tenant. `Options.IndexAllFields` adds a GIN index on `data` in either layout.
  - **Native queries:** `sql.go` translates filters, sorting and keyset pagination to SQL. Leaves are COALESCEd **only under NOT**, so elsewhere they stay index-matchable. Index DDL and queries build field expressions with the same `fieldExprs`, so the planner matches them. Keysets are row comparisons when all directions agree. `ListRecords` and `ListLabels` build their WHERE with the same `matching` (tenant, record type, filter). `ListLabels` is one `jsonb_array_elements_text` + `GROUP BY` query ordered with `COLLATE "C"`.
  - **SQL safety:** every value, cursor values included, is a numbered parameter (`sqlBuilder.arg` returns `$n`, so build order never matters). Only pointer tokens are inlined as escaped literals, so queries match the index DDL.
  - **Tests:**
    - The conformance suite runs in four variants.
    - `TestFiltersMatchReference` is a differential test against memstore, covering every op under NOT, for both `ListRecords` and `ListLabels`.
    - `TestIndexesServeQueries` asserts the indexes are used, via EXPLAIN.
    - The tests need `MNEME_PG_DSN` and **skip** without it. Use `-v` to confirm they actually ran.

Changes to `store`, `query` or `storetest` can break plugins. Re-run their tests too.

## Conventions

The user's Go style rules also apply globally; follow them in addition to these:

- Use `any`, never `interface{}`.
- **Type every parameter individually**: `func f(a string, b string)`, never `func f(a, b string)`. This also applies to interface methods and closures.
- Prefer `x := T{}` and `x := any(nil)` over `var x T`. `var` is acceptable only where it's unavoidable, such as recursive closures (`var walk func(...)`) and package-level vars.
- **Package placement:** well-defined, general-purpose functionality goes in its own sub-package (as `jsonptr` does). Ask if unsure or if it would cause an import cycle.
- **Storage agnostic.** The core must never depend on, mention or assume a specific database. Keep backends in `../mneme-plugins/<name>` as separate modules. If a plugin uses Postgres, it uses pgx v5 directly (`pgxpool`, `$n` parameters), **not** Gorm: storage plugins run only hand-written SQL, and Gorm's `?` placeholders forced workarounds (the user approved this exception to their Gorm rule).
- **JSON helpers:** for strict decoding, null checks, "string or object" configs and JSON equality, use `jsonx`. Don't hand-roll a `json.Decoder` with `DisallowUnknownFields`.
- **Errors:** every caller-input problem must satisfy `errors.Is(err, errs.ErrInvalid)`: build it with `errs.Invalidf(format, args...)`, never `fmt.Errorf("%w: ...", errs.ErrInvalid, ...)`. Other sentinels are wrapped with `%w`. `schema.ValidationError` implements `Is` for this.
- **Imports:** standard library, then third-party, then `mneme/...`, as separate groups.
- **Docs:** keep godoc comments on all exported identifiers. Package-level comments document semantics (see `query` and `idgen`).
- **Formatting:** run `gofmt -w .` before finishing. Formatting is not enforced by a hook.
- **Tests:** a black-box `_test` package per package. Table-driven where it fits.

## Decisions already made (don't relitigate)

- **Evolution is free:** any change is allowed between versions, there are no migrations, and old records keep their version. `Config.Compatibility` is the hook for future rules.
- **Required fields across versions:** in `MergeFields`/`DescribeRecordType`, a field is `Required` only if **every** version requires it, because older records may lack it.
- **`StrictFilterFields`** is opt-in. When it's on, `List` rejects filter and sort fields that no version declares.
- **Record operations:** insert, get, list, update (a full replace against the latest schema) and delete (hard). There is no patch and no optimistic locking. Whole record types can be deleted (`DeleteRecordType`, with their data and counters). Type deprecation is planned but not built.
- **No backward compatibility.** The project is pre-release. Change storage formats, schemas and APIs freely. Don't write migrations, legacy-data fallbacks or compatibility shims, either in stores or in the core.
- **Naming:** it's "record type" everywhere, as in the brief. That covers identifiers (`recordType`, `Record.RecordType`, `GetRecordType`, `DescribeRecordType`, `RecordTypeDescription`), JSON (`"recordType"`), tools (`record_type`) and prose. The user chose this, so don't reintroduce "entity".
- **Record data** on `Insert` is still `json.RawMessage`. An `InsertValue(ctx, name, v any)` convenience has been offered to the user but not requested.

## Not set up yet

- No git repository, lint config (`golangci-lint`) or CI. Ask where it will be hosted before adding them.
- The final module path.
