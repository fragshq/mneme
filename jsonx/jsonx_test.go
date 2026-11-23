package jsonx_test

import (
	"testing"

	"github.com/fragshq/mneme/jsonx"
)

func TestIsNullAndKind(t *testing.T) {
	cases := []struct {
		raw  string
		null bool
		kind byte
	}{
		{"", true, 0},
		{"  \n", true, 0},
		{"null", true, 'n'},
		{" null ", true, 'n'},
		{`"x"`, false, '"'},
		{` {"a":1}`, false, '{'},
		{"[1]", false, '['},
		{"-1", false, '-'},
	}
	for _, tc := range cases {
		if got := jsonx.IsNull([]byte(tc.raw)); got != tc.null {
			t.Errorf("IsNull(%q) = %v", tc.raw, got)
		}
		if got := jsonx.Kind([]byte(tc.raw)); got != tc.kind {
			t.Errorf("Kind(%q) = %q", tc.raw, got)
		}
	}
}

type pair struct {
	A int    `json:"a"`
	B string `json:"b,omitempty"`
}

func TestDecodeStrict(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{`{"a":1}`, true},
		{`{"a":1,"b":"x"}`, true},
		{`{"a":1,"c":2}`, false},
		{`{"a":"one"}`, false},
		{`{"a":1} {"a":2}`, false},
		{`{"a":1} x`, false},
		{`{`, false},
	}
	for _, tc := range cases {
		p := pair{}
		if err := jsonx.DecodeStrict([]byte(tc.raw), &p); (err == nil) != tc.ok {
			t.Errorf("DecodeStrict(%s): %v", tc.raw, err)
		}
	}
}

func TestRemarshal(t *testing.T) {
	p := pair{}
	if err := jsonx.Remarshal(map[string]any{"a": 3, "b": "x"}, &p); err != nil || p != (pair{A: 3, B: "x"}) {
		t.Fatalf("%+v %v", p, err)
	}
	if err := jsonx.Remarshal(map[string]any{"z": 1}, &p); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := jsonx.Remarshal(func() {}, &p); err == nil {
		t.Fatal("unencodable value accepted")
	}
}

func TestStringOr(t *testing.T) {
	cases := []struct {
		raw     string
		s       string
		obj     pair
		wantErr bool
	}{
		{``, "", pair{}, false},
		{`null`, "", pair{}, false},
		{`"v7"`, "v7", pair{}, false},
		{`{"a":2}`, "", pair{A: 2}, false},
		{`{"x":2}`, "", pair{}, true},
		{`3`, "", pair{}, true},
	}
	for _, tc := range cases {
		s, obj := "", pair{}
		err := jsonx.StringOr([]byte(tc.raw), &s, &obj)
		if (err != nil) != tc.wantErr || s != tc.s || obj != tc.obj {
			t.Errorf("StringOr(%s) = %q %+v %v", tc.raw, s, obj, err)
		}
	}
}

func TestEqual(t *testing.T) {
	cases := []struct {
		a     string
		b     string
		equal bool
	}{
		{`{"a":1,"b":[1,2]}`, `{ "b": [1, 2], "a": 1.0 }`, true},
		{`{"a":1}`, `{"a":2}`, false},
		{`[1,2]`, `[2,1]`, false},
		{`null`, `null`, true},
		{`{`, `{`, false},
	}
	for _, tc := range cases {
		if got := jsonx.Equal([]byte(tc.a), []byte(tc.b)); got != tc.equal {
			t.Errorf("Equal(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}
