// Package jsonptr implements RFC 6901 JSON Pointers over values decoded by
// encoding/json (map[string]any, []any, and scalars).
package jsonptr

import (
	"fmt"
	"strconv"
	"strings"
)

var (
	// escaper and unescaper translate a token to and from its RFC 6901
	// form; each runs in a single pass, so "~01" decodes to "~1".
	escaper   = strings.NewReplacer("~", "~0", "/", "~1")
	unescaper = strings.NewReplacer("~1", "/", "~0", "~")
	// validEscapes removes the two legal escapes, so any "~" left is invalid.
	validEscapes = strings.NewReplacer("~0", "", "~1", "")
)

// Pointer is a parsed JSON Pointer. The empty pointer addresses the whole document.
type Pointer []string

// Parse parses s as a JSON Pointer ("" or "/a/b~1c").
func Parse(s string) (Pointer, error) {
	if s == "" {
		return Pointer{}, nil
	}
	if s[0] != '/' {
		return nil, fmt.Errorf("json pointer %q must start with '/'", s)
	}
	parts := strings.Split(s[1:], "/")
	for i, p := range parts {
		if strings.Contains(validEscapes.Replace(p), "~") {
			return nil, fmt.Errorf("json pointer %q has an invalid escape", s)
		}
		parts[i] = unescaper.Replace(p)
	}
	return Pointer(parts), nil
}

// FromDotted converts a dotted path ("owner.id") into a pointer.
func FromDotted(s string) Pointer {
	if s == "" {
		return Pointer{}
	}
	return Pointer(strings.Split(s, "."))
}

// String renders the pointer in RFC 6901 form.
func (p Pointer) String() string {
	b := strings.Builder{}
	for _, t := range p {
		b.WriteByte('/')
		b.WriteString(escaper.Replace(t))
	}
	return b.String()
}

// Get resolves the pointer against doc. The boolean is false when any
// segment is missing; an explicit JSON null is reported as (nil, true).
func (p Pointer) Get(doc any) (any, bool) {
	cur := doc
	for _, tok := range p {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[tok]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) || (len(tok) > 1 && tok[0] == '0') {
				return nil, false
			}
			cur = v[i]
		default:
			return nil, false
		}
	}
	return cur, true
}
