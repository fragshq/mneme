// Package jsonx holds small JSON helpers shared across Mneme: strict
// decoding (unknown fields are errors), null detection, re-decoding loosely
// typed values into typed ones, and semantic equality.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// IsNull reports whether raw is empty, only whitespace, or the literal null.
func IsNull(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// Kind returns the first significant byte of raw: '{', '[', '"', 't', 'f',
// 'n', a digit or '-', or 0 when raw is empty.
func Kind(raw []byte) byte {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return 0
	}
	return t[0]
}

// DecodeStrict decodes raw into out, rejecting unknown object fields and
// trailing data.
func DecodeStrict(raw []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// Remarshal converts in (typically a loosely typed value such as a
// map[string]any) into out by encoding it to JSON and decoding it strictly.
func Remarshal(in any, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return DecodeStrict(raw, out)
}

// StringOr decodes raw as either a bare JSON string, stored in *s, or an
// object decoded strictly into obj. A null or empty raw leaves both
// untouched. It suits configs with a shorthand form, e.g. "v7" or
// {"version":"v7"}.
func StringOr(raw []byte, s *string, obj any) error {
	if IsNull(raw) {
		return nil
	}
	if Kind(raw) == '"' {
		return json.Unmarshal(raw, s)
	}
	return DecodeStrict(raw, obj)
}

// Equal reports whether a and b hold the same JSON value: object key order
// and whitespace are ignored, and numbers compare by value. Invalid JSON is
// never equal to anything.
func Equal(a []byte, b []byte) bool {
	x, y := any(nil), any(nil)
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, errA := json.Marshal(x)
	jb, errB := json.Marshal(y)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}
