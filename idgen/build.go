package idgen

import "encoding/json"

// Constructors for building ID strategies in Go:
//
//	idgen.NewSpec(
//		idgen.Fixed("RID-"),
//		idgen.Date("2006"),
//		idgen.Fixed("-"),
//		idgen.Sequence(idgen.SequenceOptions{Name: "rid", Pad: 4, Scope: idgen.ScopePrefix}),
//	)

// NewSpec returns a spec made of the given segments.
func NewSpec(segments ...SegmentSpec) *Spec {
	return &Spec{Segments: segments}
}

func segmentSpec(kind string, config any) SegmentSpec {
	raw, err := json.Marshal(config)
	if err != nil {
		panic("idgen: " + err.Error()) // configs below are always encodable
	}
	return SegmentSpec{Kind: kind, Config: raw}
}

// Fixed renders literal text.
func Fixed(text string) SegmentSpec { return segmentSpec(KindFixed, text) }

// ScopePrefix keeps one counter per distinct text rendered before the
// sequence segment (e.g. restart numbering every year after a Date segment).
const ScopePrefix = "prefix"

// SequenceOptions configures a Sequence segment; it is also the JSON form of
// a sequence segment's object config. Zero values take defaults.
type SequenceOptions struct {
	Name  string `json:"name,omitempty"`  // counter name, default "default"
	Pad   int    `json:"pad,omitempty"`   // left-pad with zeros to this width
	Start int64  `json:"start,omitempty"` // first value, default 1
	Step  int64  `json:"step,omitempty"`  // increment, default 1
	Scope string `json:"scope,omitempty"` // "" or ScopePrefix
}

// Sequence renders a progressive counter.
func Sequence(o SequenceOptions) SegmentSpec { return segmentSpec(KindSequence, o) }

// UUIDv7 renders a time-ordered UUID (the default strategy).
func UUIDv7() SegmentSpec { return segmentSpec(KindUUID, "v7") }

// UUIDv4 renders a random UUID.
func UUIDv4() SegmentSpec { return segmentSpec(KindUUID, "v4") }

// Random renders length random characters. alphabet may use ranges such as
// "A-Z0-9"; empty means Crockford base32.
func Random(length int, alphabet string) SegmentSpec {
	return segmentSpec(KindRandom, randomConfig{Length: length, Alphabet: alphabet})
}

// Date renders the creation time (UTC) with a Go time layout, e.g. "2006".
func Date(layout string) SegmentSpec { return segmentSpec(KindDate, layout) }

// Field renders the scalar at pointer (e.g. "/region") in the record; the
// insert fails if it is missing.
func Field(pointer string) SegmentSpec { return segmentSpec(KindField, pointer) }

// FieldOr is Field with a fallback used when the value is missing or empty.
func FieldOr(pointer string, fallback string) SegmentSpec {
	return segmentSpec(KindField, fieldConfig{Pointer: pointer, Default: &fallback})
}
