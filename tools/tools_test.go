package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"mneme"
	"mneme/errs"
	"mneme/script"
	"mneme/store"
	"mneme/store/memstore"
	"mneme/tools"
)

func newTools(t *testing.T) []tools.Tool {
	t.Helper()
	m := memstore.New()
	e, err := mneme.New(mneme.Config{Schemas: m, Records: m, Sequences: m})
	if err != nil {
		t.Fatal(err)
	}
	return tools.All(e, script.Options{})
}

// call runs a tool and returns its result as JSON.
func call(t *testing.T, ts []tools.Tool, name string, input string) (string, error) {
	t.Helper()
	tool, ok := tools.Find(ts, name)
	if !ok {
		t.Fatalf("no tool %s", name)
	}
	out, err := tool.Call(context.Background(), json.RawMessage(input))
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if raw[0] != '{' {
		t.Fatalf("%s must return a JSON object, got %s", name, raw)
	}
	return string(raw), nil
}

func mustCall(t *testing.T, ts []tools.Tool, name string, input string) string {
	t.Helper()
	out, err := call(t, ts, name, input)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return out
}

func TestToolSet(t *testing.T) {
	ts := newTools(t)
	names := []string{}
	for _, tool := range ts {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.InputSchema == nil || tool.InputSchema.Type != "object" || tool.Call == nil {
			t.Fatalf("%s is incomplete", tool.Name)
		}
		if _, err := json.Marshal(tool.InputSchema); err != nil {
			t.Fatalf("%s: schema does not marshal: %v", tool.Name, err)
		}
		for name, p := range tool.InputSchema.Properties {
			if p.Description == "" {
				t.Errorf("%s: property %s has no description", tool.Name, name)
			}
		}
	}
	want := "list_record_types define_record_type get_record_type validate_record_type preview_id list_records count_records get_record list_labels run_script insert_record update_record delete_record"
	if strings.Join(names, " ") != want {
		t.Fatalf("tools: %s", strings.Join(names, " "))
	}
	if tool, _ := tools.Find(ts, tools.RunScript); tool.Description != script.Guide {
		t.Fatal("run_script's description must be script.Guide")
	}
	def, _ := tools.Find(ts, tools.DefineRecordType)
	if d := def.InputSchema.Properties["id_strategy"].Description; !strings.HasPrefix(d, "Leave this out.") {
		t.Fatalf("id_strategy must steer towards the default: %q", d)
	}
	if req := strings.Join(def.InputSchema.Required, ","); req != "name,schema" {
		t.Fatalf("define_record_type required: %s", req)
	}
}

func TestToolsEndToEnd(t *testing.T) {
	ts := newTools(t)
	mustCall(t, ts, tools.DefineRecordType, `{"name":"invoice","schema":{"type":"object","properties":{"total":{"type":"number"}},"required":["total"]},
		"id_strategy":{"segments":[{"fixed":"INV-"},{"sequence":{"name":"inv","pad":4}}]}}`)
	if out := mustCall(t, ts, tools.PreviewID, `{"record_type":"invoice"}`); out != `{"id":"INV-0001"}` {
		t.Fatalf("preview_id: %s", out)
	}
	if out := mustCall(t, ts, tools.ValidateRecordType, `{"name":"x","schema":{"type":"object"}}`); out != `{"valid":true}` {
		t.Fatalf("validate: %s", out)
	}
	if _, err := call(t, ts, tools.ValidateRecordType, `{"name":"x","schema":{"type":"string"}}`); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("validate a non-object schema: %v", err)
	}
	if out := mustCall(t, ts, tools.ListRecordTypes, ``); !strings.HasPrefix(out, `{"recordTypes":[{"name":"invoice"`) {
		t.Fatalf("list_record_types: %s", out)
	}
	for _, total := range []string{"10", "250", "40"} {
		mustCall(t, ts, tools.InsertRecord, `{"record_type":"invoice","data":{"total":`+total+`},"labels":["q1"]}`)
	}
	if _, err := call(t, ts, tools.InsertRecord, `{"record_type":"invoice","data":{}}`); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("insert violating the schema: %v", err)
	}
	if out := mustCall(t, ts, tools.ListRecords, `{"record_type":"invoice","filter":{"op":"gt","field":"total","value":30},"sort":[{"field":"total","desc":true}]}`); !strings.Contains(out, `"INV-0002"`) || strings.Contains(out, `"INV-0001"`) || strings.Index(out, "INV-0002") > strings.Index(out, "INV-0003") {
		t.Fatalf("list_records: %s", out)
	}
	if out := mustCall(t, ts, tools.ListRecords, `{"record_type":"invoice","limit":2}`); !strings.Contains(out, `"limit":2,"total":3,"hasMore":true,"nextCursor":`) {
		t.Fatalf("list_records page metadata: %s", out)
	}
	if out := mustCall(t, ts, tools.ListRecords, `{"record_type":"invoice","filter":{"op":"gt","field":"total","value":100}}`); !strings.HasSuffix(out, `"limit":50,"total":1,"hasMore":false}`) {
		t.Fatalf("list_records last page metadata: %s", out)
	}
	if out := mustCall(t, ts, tools.CountRecords, `{"record_type":"invoice"}`); out != `{"count":3}` {
		t.Fatalf("count_records: %s", out)
	}
	if out := mustCall(t, ts, tools.CountRecords, `{"record_type":"invoice","filter":{"op":"gt","field":"total","value":30}}`); out != `{"count":2}` {
		t.Fatalf("count_records filtered: %s", out)
	}
	if _, err := call(t, ts, tools.CountRecords, `{"record_type":"nope"}`); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("count_records on an unknown type: %v", err)
	}
	if out := mustCall(t, ts, tools.GetRecord, `{"record_type":"invoice","id":"INV-0003"}`); !strings.Contains(out, `"total":40`) {
		t.Fatalf("get_record: %s", out)
	}
	if out := mustCall(t, ts, tools.ListLabels, `{"record_type":"invoice"}`); out != `{"labels":[{"label":"q1","count":3}]}` {
		t.Fatalf("list_labels: %s", out)
	}
	if out := mustCall(t, ts, tools.ListLabels, `{"record_type":"invoice","filter":{"op":"gt","field":"total","value":100}}`); out != `{"labels":[{"label":"q1","count":1}]}` {
		t.Fatalf("list_labels filtered: %s", out)
	}
	if out := mustCall(t, ts, tools.UpdateRecord, `{"record_type":"invoice","id":"INV-0003","data":{"total":45}}`); !strings.Contains(out, `"labels":[]`) {
		t.Fatalf("update_record must replace labels: %s", out)
	}
	if out := mustCall(t, ts, tools.RunScript, `{"code":"list('invoice').reduce((s, r) => s + r.data.total, 0)"}`); !strings.Contains(out, `"value":305`) {
		t.Fatalf("run_script: %s", out)
	}
	if out := mustCall(t, ts, tools.DeleteRecord, `{"record_type":"invoice","id":"INV-0001"}`); out != `{"deleted":"INV-0001"}` {
		t.Fatalf("delete_record: %s", out)
	}
	if _, err := call(t, ts, tools.GetRecord, `{"record_type":"invoice","id":"INV-0001"}`); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("deleted record: %v", err)
	}
	out := mustCall(t, ts, tools.GetRecordType, `{"name":"invoice"}`)
	if !strings.Contains(out, `"versions":[1]`) || !strings.Contains(out, `"fields":[{"path":"/total"`) {
		t.Fatalf("get_record_type: %s", out)
	}
}

func TestInputErrors(t *testing.T) {
	ts := newTools(t)
	mustCall(t, ts, tools.DefineRecordType, `{"name":"note","schema":{"type":"object"}}`)
	cases := []struct {
		name  string
		tool  string
		input string
		msg   string
	}{
		{"not JSON", tools.GetRecord, `{`, "not valid JSON"},
		{"missing required", tools.GetRecord, `{"record_type":"note"}`, "id"},
		{"unknown property", tools.GetRecord, `{"record_type":"note","id":"x","extra":1}`, "extra"},
		{"wrong type", tools.ListRecords, `{"record_type":"note","limit":"ten"}`, "limit"},
		{"bad filter shape", tools.ListRecords, `{"record_type":"note","filter":{"status":"open"}}`, "status"},
		{"bad id strategy", tools.DefineRecordType, `{"name":"x","schema":{"type":"object"},"id_strategy":{"segments":[{"nope":1}]}}`, "nope"},
		{"script error with logs", tools.RunScript, `{"code":"console.log('step 1'); list('ghost')"}`, "console output:\nstep 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := call(t, ts, tc.tool, tc.input)
			if !errors.Is(err, errs.ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error %q lacks %q", err, tc.msg)
			}
		})
	}
}

// Tools are values: an application may change a description without
// affecting other sets.
func TestToolsAreIndependentValues(t *testing.T) {
	a, b := newTools(t), newTools(t)
	a[0].Description = "custom"
	a[1].InputSchema.Properties["name"].Description = "custom"
	if b[0].Description == "custom" || b[1].InputSchema.Properties["name"].Description == "custom" {
		t.Fatal("tool sets share state")
	}
}

func TestListRecordsSortForms(t *testing.T) {
	ts := newTools(t)
	mustCall(t, ts, tools.DefineRecordType, `{"name":"item","schema":{"type":"object"}}`)
	for _, d := range []string{`{"qty":2,"kind":"b"}`, `{"qty":9,"kind":"a"}`, `{"qty":5,"kind":"a"}`} {
		mustCall(t, ts, tools.InsertRecord, `{"record_type":"item","data":`+d+`}`)
	}
	order := func(out string) string {
		p := struct {
			Items []struct {
				Data struct {
					Qty int `json:"qty"`
				} `json:"data"`
			} `json:"items"`
		}{}
		if err := json.Unmarshal([]byte(out), &p); err != nil {
			t.Fatal(err)
		}
		qs := []string{}
		for _, it := range p.Items {
			qs = append(qs, fmt.Sprint(it.Data.Qty))
		}
		return strings.Join(qs, ",")
	}
	cases := []struct {
		sort string
		want string
	}{
		{`"qty"`, "2,5,9"},
		{`"-qty"`, "9,5,2"},
		{`{"field":"qty","desc":true}`, "9,5,2"},
		{`["kind","-qty"]`, "9,5,2"},
		{`[{"field":"kind","desc":true},"qty"]`, "2,5,9"},
	}
	for _, tc := range cases {
		if got := order(mustCall(t, ts, tools.ListRecords, `{"record_type":"item","sort":`+tc.sort+`}`)); got != tc.want {
			t.Errorf("sort %s: got %s, want %s", tc.sort, got, tc.want)
		}
	}
	for _, bad := range []string{`3`, `[true]`, `{"field":"qty","direction":"desc"}`, `"-"`} {
		if _, err := call(t, ts, tools.ListRecords, `{"record_type":"item","sort":`+bad+`}`); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("sort %s: want ErrInvalid, got %v", bad, err)
		}
	}
}

// failingStore fails every record read with an infrastructure error.
type failingStore struct {
	*memstore.Store
	err error
}

func (f failingStore) GetRecord(context.Context, string, string) (store.Record, error) {
	return store.Record{}, f.err
}

func TestEngineErrorsPassThrough(t *testing.T) {
	down := errors.New("db down")
	m := memstore.New()
	e, err := mneme.New(mneme.Config{Schemas: m, Records: failingStore{Store: m, err: down}})
	if err != nil {
		t.Fatal(err)
	}
	ts := tools.Query(e, script.Options{})
	_, err = call(t, ts, tools.GetRecord, `{"record_type":"x","id":"1"}`)
	if !errors.Is(err, down) || errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want the engine's error unchanged, got %v", err)
	}
	_, err = call(t, ts, tools.RunScript, `{"code":"get('x', '1')"}`)
	if !errors.Is(err, down) {
		t.Fatalf("run_script must surface the store failure, got %v", err)
	}
}

func TestInstructionsMentionOnlyPresentTools(t *testing.T) {
	m := memstore.New()
	e, err := mneme.New(mneme.Config{Schemas: m, Records: m, Sequences: m})
	if err != nil {
		t.Fatal(err)
	}
	all := tools.All(e, script.Options{})
	sets := map[string][]tools.Tool{
		"all":    all,
		"design": tools.Design(e),
		"query":  tools.Query(e, script.Options{}),
		"write":  tools.Write(e),
	}
	for name, ts := range sets {
		t.Run(name, func(t *testing.T) {
			text := tools.Instructions(ts)
			if !strings.Contains(text, "record type") {
				t.Fatalf("the introduction is missing: %s", text)
			}
			for _, tool := range all {
				_, present := tools.Find(ts, tool.Name)
				if mentioned := regexp.MustCompile(`\b` + tool.Name + `\b`).MatchString(text); mentioned && !present {
					t.Errorf("%s is mentioned but not offered", tool.Name)
				}
			}
		})
	}
}
