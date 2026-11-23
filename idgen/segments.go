package idgen

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/jsonptr"
	"github.com/fragshq/mneme/jsonx"
)

// stringConfig decodes a config that is a JSON string; null or empty leaves
// *s (its default) untouched.
func stringConfig(cfg json.RawMessage, s *string) error {
	if jsonx.IsNull(cfg) {
		return nil
	}
	if err := json.Unmarshal(cfg, s); err != nil {
		return errors.New("config must be a string")
	}
	return nil
}

// ---- fixed -----------------------------------------------------------------

func newFixed(cfg json.RawMessage) (segment, error) {
	text := ""
	if err := stringConfig(cfg, &text); err != nil {
		return segment{}, err
	}
	if text == "" {
		return segment{}, errors.New("text must not be empty")
	}
	return segment{
		render:        func(context.Context, *GenContext) (string, error) { return text, nil },
		deterministic: true,
	}, nil
}

// ---- sequence --------------------------------------------------------------

func newSequence(cfg json.RawMessage) (segment, error) {
	o := SequenceOptions{Name: "default", Start: 1, Step: 1}
	if err := jsonx.StringOr(cfg, &o.Name, &o); err != nil {
		return segment{}, err
	}
	switch {
	case o.Name == "":
		return segment{}, errors.New("name must not be empty")
	case o.Pad < 0 || o.Pad > 32:
		return segment{}, errors.New("pad must be between 0 and 32")
	case o.Step < 1:
		return segment{}, errors.New("step must be >= 1")
	case o.Start < 0:
		return segment{}, errors.New("start must be >= 0")
	case o.Scope != "" && o.Scope != ScopePrefix:
		return segment{}, fmt.Errorf("scope must be \"\" or %q, got %q", ScopePrefix, o.Scope)
	}
	return segment{render: o.render, needsSequences: true}, nil
}

func (o SequenceOptions) render(ctx context.Context, g *GenContext) (string, error) {
	scope := ""
	if o.Scope == ScopePrefix {
		scope = g.Prefix
	}
	n, err := g.Sequences.Next(ctx, g.RecordType, o.Name, scope)
	if err != nil {
		return "", fmt.Errorf("sequence %q: %w", o.Name, err)
	}
	if n < 1 {
		return "", fmt.Errorf("sequence %q: provider returned %d, want >= 1", o.Name, n)
	}
	v := strconv.FormatInt(o.Start+(n-1)*o.Step, 10)
	if len(v) < o.Pad {
		v = strings.Repeat("0", o.Pad-len(v)) + v
	}
	return v, nil
}

// ---- uuid ------------------------------------------------------------------

func newUUID(cfg json.RawMessage) (segment, error) {
	ver := "v7"
	if err := stringConfig(cfg, &ver); err != nil {
		return segment{}, err
	}
	gen := uuid.NewV7
	switch ver {
	case "v7":
	case "v4":
		gen = uuid.NewRandom
	default:
		return segment{}, fmt.Errorf(`version must be "v4" or "v7", got %q`, ver)
	}
	return segment{render: func(context.Context, *GenContext) (string, error) {
		id, err := gen()
		return id.String(), err
	}}, nil
}

// ---- random ----------------------------------------------------------------

// defaultAlphabet is Crockford base32 (no I, L, O, U), so IDs read aloud well.
const defaultAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// randomConfig is the JSON form of a random segment.
type randomConfig struct {
	Length   int    `json:"length"`
	Alphabet string `json:"alphabet,omitempty"`
}

func newRandom(cfg json.RawMessage) (segment, error) {
	c := randomConfig{Length: 8}
	if !jsonx.IsNull(cfg) {
		if err := jsonx.DecodeStrict(cfg, &c); err != nil {
			return segment{}, fmt.Errorf(`config must be an object like {"length":6}: %v`, err)
		}
	}
	if c.Length < 1 || c.Length > 128 {
		return segment{}, errors.New("length must be between 1 and 128")
	}
	alpha := []rune(defaultAlphabet)
	if c.Alphabet != "" {
		expanded, err := expandAlphabet(c.Alphabet)
		if err != nil {
			return segment{}, err
		}
		alpha = expanded
	}
	max := big.NewInt(int64(len(alpha)))
	return segment{render: func(context.Context, *GenContext) (string, error) {
		out := make([]rune, c.Length)
		for i := range out {
			n, err := rand.Int(rand.Reader, max)
			if err != nil {
				return "", err
			}
			out[i] = alpha[n.Int64()]
		}
		return string(out), nil
	}}, nil
}

// expandAlphabet expands ranges such as "A-Z0-9" and removes duplicates.
func expandAlphabet(s string) ([]rune, error) {
	in := []rune(s)
	seen := map[rune]bool{}
	out := []rune{}
	add := func(r rune) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for i := 0; i < len(in); i++ {
		if i+2 < len(in) && in[i+1] == '-' {
			lo, hi := in[i], in[i+2]
			if lo > hi {
				return nil, fmt.Errorf("alphabet range %c-%c is reversed", lo, hi)
			}
			for r := lo; r <= hi; r++ {
				add(r)
			}
			i += 2
			continue
		}
		add(in[i])
	}
	if len(out) < 2 {
		return nil, errors.New("alphabet must contain at least 2 characters")
	}
	return out, nil
}

// ---- date ------------------------------------------------------------------

func newDate(cfg json.RawMessage) (segment, error) {
	layout := "20060102"
	if err := stringConfig(cfg, &layout); err != nil {
		return segment{}, err
	}
	if layout == "" {
		return segment{}, errors.New("layout must not be empty")
	}
	return segment{
		render:        func(_ context.Context, g *GenContext) (string, error) { return g.Now.UTC().Format(layout), nil },
		deterministic: true,
	}, nil
}

// ---- field -----------------------------------------------------------------

// fieldConfig is the object form of a field segment; the short form is the
// pointer alone.
type fieldConfig struct {
	Pointer string  `json:"pointer"`
	Default *string `json:"default,omitempty"`
}

func newField(cfg json.RawMessage) (segment, error) {
	c := fieldConfig{}
	if err := jsonx.StringOr(cfg, &c.Pointer, &c); err != nil {
		return segment{}, err
	}
	if c.Pointer == "" {
		return segment{}, errors.New(`a JSON pointer such as "/region" is required`)
	}
	ptr, err := jsonptr.Parse(c.Pointer)
	if err != nil {
		return segment{}, err
	}
	if len(ptr) == 0 {
		return segment{}, errors.New("pointer must address a field, not the whole record")
	}
	return segment{
		render: func(_ context.Context, g *GenContext) (string, error) {
			if v, ok := ptr.Get(g.Data); ok {
				switch x := v.(type) {
				case string:
					if x != "" {
						return x, nil
					}
				case float64:
					return strconv.FormatFloat(x, 'f', -1, 64), nil
				case bool:
					return strconv.FormatBool(x), nil
				}
			}
			if c.Default != nil {
				return *c.Default, nil
			}
			return "", errs.Invalidf("id field %s is missing, empty or not a scalar", c.Pointer)
		},
		deterministic: true,
	}, nil
}
