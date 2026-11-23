package jsonptr_test

import (
	"encoding/json"
	"testing"

	"mneme/jsonptr"
)

func TestGetAndEscapes(t *testing.T) {
	doc := any(nil)
	_ = json.Unmarshal([]byte(`{"a":{"b/c":[10,{"d~e":null}]}}`), &doc)
	cases := []struct {
		ptr   string
		want  any
		found bool
	}{
		{"/a/b~1c/0", 10.0, true},
		{"/a/b~1c/1/d~0e", nil, true},
		{"/a/b~1c/2", nil, false},
		{"/a/b~1c/01", nil, false},
		{"/a/x", nil, false},
	}
	for _, c := range cases {
		p, err := jsonptr.Parse(c.ptr)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := p.Get(doc)
		if ok != c.found || got != c.want {
			t.Errorf("%s: got %v,%v", c.ptr, got, ok)
		}
		if p.String() != c.ptr {
			t.Errorf("String round trip: %s -> %s", c.ptr, p.String())
		}
	}
	for _, bad := range []string{"a", "/x~2"} {
		if _, err := jsonptr.Parse(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if jsonptr.FromDotted("a.b").String() != "/a/b" {
		t.Error("FromDotted")
	}
}
