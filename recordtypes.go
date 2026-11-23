package mneme

import (
	"context"
	"errors"
	"fmt"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/schema"
)

// Define stores a new version of a record type, creating the type on first
// use. The latest version is the one new records are validated against.
//
// d.Version is optional: 0 assigns latest+1, and an explicit value must be
// greater than the latest stored version. d.CreatedAt is set by the Engine.
// The definition is copied, so later changes to d do not affect it.
func (e *Engine) Define(ctx context.Context, d schema.Definition) (schema.Definition, error) {
	if err := schema.ValidateName(d.Name); err != nil {
		return schema.Definition{}, err
	}
	if d.Version < 0 {
		return schema.Definition{}, errs.Invalidf("version must not be negative")
	}
	d, err := d.Clone()
	if err != nil {
		return schema.Definition{}, errs.Invalidf("%v", err)
	}
	prev := (*schema.Definition)(nil)
	latest, err := e.cfg.Schemas.GetDefinition(ctx, d.Name, 0)
	switch {
	case err == nil:
		prev = &latest
	case !errors.Is(err, errs.ErrNotFound):
		return schema.Definition{}, err
	}
	prevVersion := 0
	if prev != nil {
		prevVersion = prev.Version
	} else if err := e.checkRecordTypeQuota(ctx); err != nil {
		return schema.Definition{}, err
	}
	switch {
	case d.Version == 0:
		d.Version = prevVersion + 1
	case d.Version <= prevVersion:
		return schema.Definition{}, fmt.Errorf("%w: version %d must be greater than the latest (%d)", errs.ErrConflict, d.Version, prevVersion)
	}
	d.CreatedAt = e.now()
	// compile caches by content, so caching before the store accepts d is
	// harmless.
	if _, err := e.compile(d); err != nil {
		return schema.Definition{}, err
	}
	if e.cfg.Compatibility != nil {
		if err := e.cfg.Compatibility(ctx, prev, d); err != nil {
			return schema.Definition{}, err
		}
	}
	if err := e.cfg.Schemas.PutDefinition(ctx, d); err != nil {
		return schema.Definition{}, err
	}
	return d, nil
}

// ValidateDefinition checks a record type (name, schema and ID strategy)
// without storing anything.
func (e *Engine) ValidateDefinition(d schema.Definition) error {
	if err := schema.ValidateName(d.Name); err != nil {
		return err
	}
	d.Version = 1
	_, err := e.build(d)
	return err
}

// DeleteRecordType permanently removes a record type: every record, every
// sequence counter and every version of its definition, in that order, so a
// failure part-way leaves the type defined and the call can be repeated. It
// returns ErrNotFound when the record type does not exist. Afterwards the
// name is free again: a new definition starts from version 1 and sequences
// from 1. Like the other operations, it is not isolated from concurrent
// writes to the same type.
func (e *Engine) DeleteRecordType(ctx context.Context, name string) error {
	if _, err := e.GetRecordType(ctx, name, 0); err != nil {
		return err
	}
	if err := e.cfg.Records.DeleteRecords(ctx, name); err != nil {
		return err
	}
	if e.cfg.Sequences != nil {
		if err := e.cfg.Sequences.DeleteSequences(ctx, name); err != nil {
			return err
		}
	}
	return e.cfg.Schemas.DeleteDefinitions(ctx, name)
}

// checkRecordTypeQuota fails with ErrQuota when the store already holds
// Config.MaxRecordTypes record types.
func (e *Engine) checkRecordTypeQuota(ctx context.Context) error {
	if e.cfg.MaxRecordTypes <= 0 {
		return nil
	}
	defs, err := e.cfg.Schemas.ListDefinitions(ctx)
	if err != nil {
		return err
	}
	if len(defs) >= e.cfg.MaxRecordTypes {
		return fmt.Errorf("%w: at most %d record types are allowed; delete one to define another", errs.ErrQuota, e.cfg.MaxRecordTypes)
	}
	return nil
}

// GetRecordType returns one version of a record type; version 0 means the latest.
func (e *Engine) GetRecordType(ctx context.Context, name string, version int) (schema.Definition, error) {
	if err := schema.ValidateName(name); err != nil {
		return schema.Definition{}, err
	}
	return e.cfg.Schemas.GetDefinition(ctx, name, version)
}

// ListRecordTypes returns the latest version of every record type.
func (e *Engine) ListRecordTypes(ctx context.Context) ([]schema.Definition, error) {
	return e.cfg.Schemas.ListDefinitions(ctx)
}

// RecordTypeSummary is a short view of a record type, for listings.
type RecordTypeSummary struct {
	Name          string `json:"name"`
	LatestVersion int    `json:"latestVersion"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
}

// Summaries summarizes definitions, typically the latest versions returned
// by ListRecordTypes. It never returns nil.
func Summaries(defs []schema.Definition) []RecordTypeSummary {
	out := make([]RecordTypeSummary, 0, len(defs))
	for _, d := range defs {
		out = append(out, RecordTypeSummary{Name: d.Name, LatestVersion: d.Version, Title: d.Title, Description: d.Description})
	}
	return out
}

// ListVersions returns every version of a record type, oldest first.
func (e *Engine) ListVersions(ctx context.Context, name string) ([]schema.Definition, error) {
	if err := schema.ValidateName(name); err != nil {
		return nil, err
	}
	return e.cfg.Schemas.ListVersions(ctx, name)
}

// RecordTypeDescription summarises a record type across all of its versions; it is
// designed to be handed to humans or LLM tooling as-is.
type RecordTypeDescription struct {
	Name          string             `json:"name"`
	LatestVersion int                `json:"latestVersion"`
	Versions      []int              `json:"versions"`
	Title         string             `json:"title,omitempty"`
	Description   string             `json:"description,omitempty"`
	IDStrategy    idgen.Spec         `json:"idStrategy"`
	Indexes       []string           `json:"indexes,omitempty"`
	Fields        []schema.Field     `json:"fields"`
	LatestSchema  *schema.JSONSchema `json:"latestSchema"`
}

// DescribeRecordType merges the fields of every version, so callers know what
// they may filter on, plus the latest version's settings.
func (e *Engine) DescribeRecordType(ctx context.Context, name string) (RecordTypeDescription, error) {
	cs, err := e.allVersions(ctx, name)
	if err != nil {
		return RecordTypeDescription{}, err
	}
	last := cs[len(cs)-1]
	out := RecordTypeDescription{
		Name:          name,
		LatestVersion: last.Definition.Version,
		Title:         last.Definition.Title,
		Description:   last.Definition.Description,
		IDStrategy:    last.Definition.IDSpec(),
		Indexes:       last.Definition.Indexes,
		Fields:        schema.MergeFields(cs),
		LatestSchema:  last.Definition.Schema,
	}
	for _, c := range cs {
		out.Versions = append(out.Versions, c.Definition.Version)
	}
	return out, nil
}

func (e *Engine) allVersions(ctx context.Context, name string) ([]*schema.Compiled, error) {
	defs, err := e.ListVersions(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]*schema.Compiled, 0, len(defs))
	for _, d := range defs {
		c, err := e.compile(d)
		if err != nil {
			return nil, fmt.Errorf("record type %s v%d: %w", name, d.Version, err)
		}
		out = append(out, c.schema)
	}
	return out, nil
}
