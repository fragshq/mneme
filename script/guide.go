package script

// Guide documents the script environment for the script's author. It is
// written for an LLM: an enclosure can use it as-is as the description of an
// MCP tool that calls Run.
const Guide = `Run read-only JavaScript over the stored records and get back its result as JSON.

Write the script like the body of an async function: use return (or leave the value as the last expression), and await if you like. Plain modern JavaScript; no modules, network, timers or writes.

Functions (all synchronous; await is allowed but not needed):
- list(type, {filter, sort, limit}) -> array of every matching record (paging is done for you). All options are optional.
- count(type, filter) -> the number of matching records, without reading them. filter is optional.
- get(type, id) -> one record, or null if there is none.
- types() -> [{name, title, description, latestVersion}] for every record type.
- describe(type) -> {versions, fields, latestSchema, …}: every field of any schema version, each as {path, types, required, versions}, where versions lists the schema versions that declare it, plus the latest JSON Schema.
- console.log(...) -> lines returned alongside the result, for debugging.

A record looks like:
  {"recordType": "order", "id": "…", "schemaVersion": 2, "data": {…the record's fields…},
   "createdAt": "2026-01-02T03:04:05Z", "updatedAt": "…", "labels": ["…"]}
The record's own fields are under data.

Records of one type may follow different schema versions (see schemaVersion), and list returns them all. A field added in a later version is missing on older records, where reading it gives undefined: arithmetic then yields NaN and method calls throw. Before relying on a field, check describe(type): a field whose versions don't include every version, or that isn't required, may be missing. Then either read it with a default (o.data.priority ?? "none"), or keep only the records that have it with an exists filter.

filter is a tree of {op, field, value} objects:
- ops: eq, ne, gt, gte, lt, lte, in (value is an array), contains (array element or substring), prefix, exists (no value)
- combine with {op: "and" | "or", filters: [...]} and {op: "not", filters: [one]}
- field: a data field ("status", "owner.id" or "/owner/id") or $id, $version, $createdAt, $updatedAt (compare with ISO 8601 strings), $labels
- a filter on a field a record lacks never matches (except under not / exists)
Example: {op: "and", filters: [{op: "eq", field: "status", value: "open"}, {op: "gte", field: "total", value: 100}]}

sort is "field" or "-field" (descending), {field, desc}, or an array of those. The default is creation order.
limit caps how many records list returns.

Prefer filtering in list over filtering in JavaScript, and count over list(...).length: the script may read a limited number of records in total and must finish within a few seconds. Exceeding a limit stops the script with an error saying which.

Example:
  const orders = list("order", {filter: {op: "eq", field: "status", value: "paid"}});
  const byCustomer = {};
  for (const o of orders) {
    byCustomer[o.data.customerId] = (byCustomer[o.data.customerId] ?? 0) + (o.data.total ?? 0);
  }
  const top = Object.entries(byCustomer).sort((a, b) => b[1] - a[1]).slice(0, 5);
  return top.map(([id, total]) => ({customer: get("customer", id)?.data.name, total}));
`
