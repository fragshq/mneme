package idgen

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"mneme/errs"
)

// SequenceProvider hands out progressive counter values. It is implemented by
// the application's storage. Next must return 1, 2, 3, ... for each distinct
// (record type, name, scope) triple and must be safe for concurrent use.
type SequenceProvider interface {
	Next(ctx context.Context, recordType string, name string, scope string) (int64, error)
}

// GenContext carries the inputs a segment may use while rendering.
type GenContext struct {
	RecordType string
	Data       any       // the record, decoded by encoding/json (map[string]any)
	Now        time.Time // creation time
	Sequences  SequenceProvider
	// Prefix is the text rendered by the preceding segments.
	Prefix string
}

// segment renders one part of an ID. deterministic segments (fixed, date,
// field) always render the same value for the same input; an ID made only of
// them is not retried on conflict. needsSequences segments require a
// SequenceProvider.
type segment struct {
	render         func(ctx context.Context, g *GenContext) (string, error)
	deterministic  bool
	needsSequences bool
}

// Built-in segment kinds.
const (
	KindFixed    = "fixed"
	KindSequence = "sequence"
	KindUUID     = "uuid"
	KindRandom   = "random"
	KindDate     = "date"
	KindField    = "field"
)

// factories builds each kind's segment from its JSON configuration,
// validating it.
var factories = map[string]func(config json.RawMessage) (segment, error){
	KindFixed:    newFixed,
	KindSequence: newSequence,
	KindUUID:     newUUID,
	KindRandom:   newRandom,
	KindDate:     newDate,
	KindField:    newField,
}

// Compile validates spec and builds a Generator. An empty spec compiles to
// DefaultSpec. Every problem matches errs.ErrInvalid.
func Compile(spec Spec) (*Generator, error) {
	if spec.IsZero() {
		spec = DefaultSpec()
	}
	g := &Generator{deterministic: true}
	for i, s := range spec.Segments {
		f, ok := factories[s.Kind]
		if !ok {
			return nil, errs.Invalidf("id segment %d: unknown kind %q", i, s.Kind)
		}
		seg, err := f(s.Config)
		if err != nil {
			return nil, errs.Invalidf("id segment %d (%s): %v", i, s.Kind, err)
		}
		g.deterministic = g.deterministic && seg.deterministic
		g.needsSequences = g.needsSequences || seg.needsSequences
		g.segments = append(g.segments, seg)
	}
	return g, nil
}

// Generator renders IDs for one compiled Spec. It is safe for concurrent use.
type Generator struct {
	segments       []segment
	deterministic  bool
	needsSequences bool
}

// Deterministic reports whether every segment is deterministic, in which case
// regenerating after a conflict cannot help.
func (g *Generator) Deterministic() bool { return g.deterministic }

// NeedsSequences reports whether a SequenceProvider is required.
func (g *Generator) NeedsSequences() bool { return g.needsSequences }

// Generate renders a new ID.
func (g *Generator) Generate(ctx context.Context, gc GenContext) (string, error) {
	if g.needsSequences && gc.Sequences == nil {
		return "", errs.Invalidf("id strategy uses a sequence but no SequenceProvider is configured")
	}
	b := strings.Builder{}
	for _, s := range g.segments {
		gc.Prefix = b.String()
		part, err := s.render(ctx, &gc)
		if err != nil {
			return "", err
		}
		b.WriteString(part)
	}
	if b.Len() == 0 {
		return "", errs.Invalidf("id strategy rendered an empty id")
	}
	return b.String(), nil
}
