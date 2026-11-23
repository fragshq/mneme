package tools

import "strings"

// Instructions returns guidance that spans several tools, for the model
// using ts: how record types and records work, and which tool to pick for
// common tasks. Per-tool guidance stays in each tool's Description; this
// covers what no single description can, such as the order to call tools
// in. Sections about a tool set (Design, Query, Write) are included only
// when ts contains that set's tools, so a server exposing a subset never
// mentions tools it lacks.
//
// The text is protocol-neutral. An MCP server passes it as its server
// instructions; other clients may put it in a system prompt.
func Instructions(ts []Tool) string {
	parts := []string{instrIntro}
	for _, s := range []struct {
		needs string
		text  string
	}{
		{DefineRecordType, instrDesign},
		{ListRecords, instrQuery},
		{InsertRecord, instrWrite},
	} {
		if _, ok := Find(ts, s.needs); ok {
			parts = append(parts, s.text)
		}
	}
	return strings.Join(parts, "\n\n")
}

const (
	instrIntro = `These tools store and query structured records.
- A record type has a name, a JSON Schema and an ID strategy. Redefining it creates a new version; all versions are kept.
- Records are validated against the latest version when written, and keep the version they were written with. Records of one type may therefore follow different schema versions: a field missing on older records is expected, not an error.
- Labels are optional tags on a record, outside its schema, for grouping and filtering (field $labels).`

	instrDesign = `Designing record types:
- Call list_record_types first, and get_record_type for a type that already exists, so you extend it instead of creating a near-duplicate.
- Check a new or changed definition with validate_record_type, then store it with define_record_type.
- define_record_type takes the complete definition, and every call creates a new version that stays forever: get the schema right before defining it, and don't redefine a type for cosmetic changes.
- Leave id_strategy out unless the user asked for an ID format. When they did, show them a sample with preview_id.`

	instrQuery = `Reading records:
- "How many?": count_records. Don't page through list_records to count.
- Browsing or fetching records: list_records with a filter. Its total says how many match; when hasMore is true, pass nextCursor back as cursor for the next page, with the same filter and sort.
- One record by ID: get_record.
- Which labels exist: list_labels, before filtering on $labels.
- Joins across record types, grouping, sums, or computed answers: run_script, which reads all matching records for you.
- Unsure which fields a type has? In run_script, describe(type) lists them across all versions.`

	instrWrite = `Changing records:
- update_record replaces the whole record, data and labels: start from the current record, change what you need, and send everything back. Labels you leave out are removed.
- An update validates against the latest schema and moves the record to that version, so an old record may need fields its version didn't have.
- delete_record is permanent. Confirm with the user before deleting records they didn't explicitly name.`
)
