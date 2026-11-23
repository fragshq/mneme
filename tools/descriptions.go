package tools

// Descriptions of input properties too long, or too shared, for a struct
// tag. newTool attaches them through prop{} overrides.
const (
	descSchema = `JSON Schema (draft 2020-12) for the records. The root must be an object schema: {"type":"object","properties":{...}}`

	descSort = `Sort order: a field name ("qty"), the name prefixed with "-" for descending ("-qty"), a {"field":"qty","desc":true} object, or an array of those for several keys (["status","-qty"]). Default: creation order.`

	descIDStrategy = `Leave this out. Records then get a UUIDv7 ID, which is unique, ordered by creation time, and the right choice for almost every record type.
Set it only when the user explicitly asks for a particular ID format (e.g. invoice numbers like INV-2026-0001) or an external system dictates the IDs; never on your own initiative to make IDs shorter, prettier or more readable.
A custom format is a commitment: sequences add a counter per insert, and a format based on record fields can collide.
When redefining a record type that already has a custom strategy, pass it again unchanged, or new records switch back to UUIDs.
Format, only when needed: {"segments":[...]} where each segment is one of
  {"fixed":"INV-"}
  {"date":"2006"} (Go layout)
  {"sequence":{"name":"inv","pad":4,"scope":"prefix"}} (scope prefix = one counter per distinct text before it)
  {"uuid":"v7"}
  {"random":{"length":8}}
  {"field":"/code"}`

	descFilter = `Optional filter, e.g. {"op":"and","filters":[{"op":"eq","field":"status","value":"open"},{"op":"gt","field":"qty","value":3}]}
Ops: eq ne gt gte lt lte in contains prefix exists and or not.
Fields: dotted paths (owner.id), JSON pointers (/owner/id), or $id $version $createdAt $updatedAt $labels.
Filter labels with contains, e.g. {"op":"contains","field":"$labels","value":"urgent"}.
A filter on a field a record lacks never matches, except under not / exists.`
)

// Tool descriptions.
const (
	descListRecordTypes = `List all record types with their latest version.`

	descDefineRecordType = `Define a new record type, or redefine an existing one.
Each call creates a new version: the latest version validates new records, while existing records keep the version they were written with.
Each call takes the complete definition, not a patch.
Records get UUIDv7 IDs by default: omit id_strategy unless the user explicitly asked for a specific ID format.`

	descGetRecordType = `Get a record type: the requested version (default: latest) with its JSON Schema and ID strategy, the list of versions, and the fields known across all versions (a field is required only if every version requires it).`

	descValidateRecordType = `Check a record type definition (name, schema, ID strategy) without storing it. Takes the same input as define_record_type.`

	descPreviewID = `Show what an ID will look like for a record type, without storing anything or consuming sequence numbers. Pass data when the ID strategy uses record fields.`

	descListRecords = `List records of a record type, optionally filtered and sorted, one page at a time.
Each page returns items plus limit (the page size applied), total (how many records match the filter across all pages) and hasMore; when hasMore is true, pass nextCursor back as cursor, with the same filter and sort, for the next page.
Records may follow any schema version (see schemaVersion).
To only count records, use count_records. To join, group or compute over many records, use run_script instead.`

	descCountRecords = `Count the records of a record type, optionally filtered, without reading them.
Use it to answer "how many" questions; to count per group or per value, use run_script.`

	descGetRecord = `Get one record by ID.`

	descListLabels = `List the distinct labels used by a record type's records, each with the number of records carrying it, sorted by label.
Pass a filter to count only matching records. Use it to discover which labels exist before filtering on $labels.`

	descInsertRecord = `Insert a record. It is validated against the latest schema of its record type and gets an ID from the record type's ID strategy. Optional labels tag the record outside its schema.`

	descUpdateRecord = `Replace a record entirely, data and labels.
The new data must conform to the latest schema, and the record moves to that version.
The ID and createdAt never change; updatedAt is set to now. Labels not passed are removed.`

	descDeleteRecord = `Permanently delete a record by ID.`
)
