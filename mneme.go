// Package mneme lets an application define versioned data structures with
// JSON Schema, store records that conform to them, and query those records,
// while the application keeps full control over storage.
//
// The application supplies storage via the interfaces in package store and
// builds an Engine:
//
//	mem := memstore.New()
//	eng, err := mneme.New(mneme.Config{Schemas: mem, Records: mem, Sequences: mem})
//
// Every Engine method takes a context that is passed untouched to the stores,
// so applications can carry tenancy or other scoping information in it.
package mneme

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/idgen"
	"github.com/fragshq/mneme/schema"
	"github.com/fragshq/mneme/store"
)

// Re-exported sentinel errors; test with errors.Is.
var (
	ErrNotFound = errs.ErrNotFound
	ErrConflict = errs.ErrConflict
	ErrInvalid  = errs.ErrInvalid
	ErrNoTenant = errs.ErrNoTenant
	ErrQuota    = errs.ErrQuota
)

// Record is a stored record.
type Record = store.Record

// Config configures an Engine. Schemas and Records are required.
type Config struct {
	Schemas store.SchemaStore
	Records store.RecordStore
	// Sequences is required only by record types whose ID strategy has a
	// sequence segment.
	Sequences store.SequenceProvider
	// Now returns the current time. Defaults to time.Now.
	Now func() time.Time
	// Compatibility, when set, is consulted before storing a new version and
	// may reject it. prev is nil for the first version of a record type.
	Compatibility func(ctx context.Context, prev *schema.Definition, next schema.Definition) error
	// StrictFilterFields makes List reject filters and sorts on fields that no
	// version of the record type declares.
	StrictFilterFields bool
	// DefaultPageSize (default 50) and MaxPageSize (default 1000) bound List.
	DefaultPageSize int
	MaxPageSize     int
	// MaxIDAttempts bounds ID regeneration after a conflict (default 5).
	MaxIDAttempts int
	// MaxRecordTypes, when positive, caps the record types a store holds:
	// Define fails with ErrQuota when it would create one more. New versions
	// of an existing type are always allowed. 0 means unlimited.
	//
	// The quotas count what the store sees through the context, so a store
	// with tenancy enforces them per tenant. They are checked before the
	// write, not atomically with it, so concurrent writers may overshoot by
	// a few.
	MaxRecordTypes int
	// MaxRecordsPerType, when positive, caps the records of each record type:
	// Insert fails with ErrQuota when the type already holds that many.
	// Updates are always allowed. 0 means unlimited.
	MaxRecordsPerType int
}

// Engine is the Mneme entry point. It is safe for concurrent use.
type Engine struct {
	cfg   Config
	cache sync.Map // cacheKey -> *compiled
}

// cacheKey identifies a definition by content, not just by (name, version):
// stores may scope definitions by tenant (through the context), so two
// tenants can hold different definitions under the same name and version
// while sharing one Engine.
type cacheKey [sha256.Size]byte

func keyOf(d schema.Definition) (cacheKey, error) {
	raw, err := json.Marshal(d)
	return cacheKey(sha256.Sum256(raw)), err
}

type compiled struct {
	schema *schema.Compiled
	ids    *idgen.Generator
}

// New validates cfg and returns an Engine.
func New(cfg Config) (*Engine, error) {
	if cfg.Schemas == nil || cfg.Records == nil {
		return nil, errors.New("mneme: Config.Schemas and Config.Records are required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.DefaultPageSize <= 0 {
		cfg.DefaultPageSize = 50
	}
	if cfg.MaxPageSize <= 0 {
		cfg.MaxPageSize = 1000
	}
	if cfg.MaxIDAttempts <= 0 {
		cfg.MaxIDAttempts = 5
	}
	return &Engine{cfg: cfg}, nil
}

// compile builds (or fetches from cache) the compiled form of a definition.
// Entries are keyed by content, so they never go stale.
func (e *Engine) compile(d schema.Definition) (*compiled, error) {
	k, err := keyOf(d)
	if err != nil {
		return nil, err
	}
	if c, ok := e.cache.Load(k); ok {
		return c.(*compiled), nil
	}
	c, err := e.build(d)
	if err != nil {
		return nil, err
	}
	actual, _ := e.cache.LoadOrStore(k, c)
	return actual.(*compiled), nil
}

func (e *Engine) build(d schema.Definition) (*compiled, error) {
	sc, err := schema.Compile(d)
	if err != nil {
		return nil, err
	}
	gen, err := idgen.Compile(sc.Definition.IDSpec())
	if err != nil {
		return nil, err
	}
	if gen.NeedsSequences() && e.cfg.Sequences == nil {
		return nil, errs.Invalidf("record type %s uses a sequence ID segment but no SequenceProvider is configured", d.Name)
	}
	return &compiled{schema: sc, ids: gen}, nil
}

func (e *Engine) latest(ctx context.Context, name string) (*compiled, error) {
	if err := schema.ValidateName(name); err != nil {
		return nil, err
	}
	d, err := e.cfg.Schemas.GetDefinition(ctx, name, 0)
	if err != nil {
		return nil, err
	}
	return e.compile(d)
}

// now is truncated to microseconds, the precision of most databases (e.g.
// SQL timestamp types), so a record returned by Insert equals the stored one.
func (e *Engine) now() time.Time { return e.cfg.Now().UTC().Truncate(time.Microsecond) }
