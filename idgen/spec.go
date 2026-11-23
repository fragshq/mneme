// Package idgen implements Mneme's composable, per-record-type ID strategies.
//
// An ID strategy (Spec) is an ordered list of segments whose rendered values
// are concatenated. Built-in segment kinds are:
//
//	{"fixed": "RID-"}                                          literal text
//	{"sequence": {"name":"rid","pad":4,"start":1,"step":1,
//	              "scope":"prefix"}}                           progressive counter
//	{"uuid": "v7"}                                             UUID (v4 or v7, default v7)
//	{"random": {"length":6,"alphabet":"A-Z0-9"}}               random characters
//	{"date": "2006"}                                           creation time, Go layout, UTC
//	{"field": "/region"}                                       value taken from the record
//
// The set of kinds is fixed. Compile turns a Spec into a Generator.
package idgen

import (
	"encoding/json"

	"mneme/errs"
	"mneme/jsonx"
)

// Spec is the JSON-serialisable description of an ID strategy.
type Spec struct {
	Segments []SegmentSpec `json:"segments"`
}

// DefaultSpec is used when a record type declares no ID strategy: a single UUIDv7.
func DefaultSpec() Spec {
	return Spec{Segments: []SegmentSpec{{Kind: KindUUID, Config: json.RawMessage(`"v7"`)}}}
}

// UnmarshalJSON decodes a spec strictly: an unknown key, such as the typo
// "segmentz", is an error rather than silently giving the default strategy.
func (s *Spec) UnmarshalJSON(b []byte) error {
	type plain Spec // without this method
	p := plain{}
	if err := jsonx.DecodeStrict(b, &p); err != nil {
		return errs.Invalidf("id strategy: %v", err)
	}
	*s = Spec(p)
	return nil
}

// IsZero reports whether the spec declares no segments.
func (s Spec) IsZero() bool { return len(s.Segments) == 0 }

// SegmentSpec is one segment, encoded in JSON as a single-key object
// {"<kind>": <config>}.
type SegmentSpec struct {
	Kind   string
	Config json.RawMessage
}

// MarshalJSON encodes the segment as {"<kind>": <config>}.
func (s SegmentSpec) MarshalJSON() ([]byte, error) {
	cfg := s.Config
	if jsonx.Kind(cfg) == 0 {
		cfg = json.RawMessage("null")
	}
	return json.Marshal(map[string]json.RawMessage{s.Kind: cfg})
}

// UnmarshalJSON decodes {"<kind>": <config>}; exactly one key is allowed.
func (s *SegmentSpec) UnmarshalJSON(b []byte) error {
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &m); err != nil {
		return errs.Invalidf(`id segment must be an object like {"fixed":"X-"}: %v`, err)
	}
	if len(m) != 1 {
		return errs.Invalidf("id segment must have exactly one key, got %d", len(m))
	}
	for k, v := range m {
		s.Kind, s.Config = k, v
	}
	return nil
}
