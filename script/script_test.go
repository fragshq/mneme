package script_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/schema"
	"github.com/fragshq/mneme/script"
	"github.com/fragshq/mneme/store/memstore"
)

// fixture: 5 customers and 12 orders, with a page size of 2 so list must
// page through the store.
func newEngine(t *testing.T) *mneme.Engine {
	t.Helper()
	ctx := context.Background()
	m := memstore.New()
	e, err := mneme.New(mneme.Config{Schemas: m, Records: m, Sequences: m, MaxPageSize: 2, DefaultPageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	obj := &schema.JSONSchema{Type: "object"}
	for _, name := range []string{"customer", "order"} {
		if _, err := e.Define(ctx, schema.Definition{Name: name, Title: name + "s", Schema: obj}); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{}
	for i, n := range []string{"ann", "bob", "cy", "dee", "eve"} {
		data, _ := json.Marshal(map[string]any{"name": n, "vip": i%2 == 0})
		r, err := e.Insert(ctx, "customer", data)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	for i := 0; i < 12; i++ {
		data, _ := json.Marshal(map[string]any{"customerId": ids[i%3], "total": (i + 1) * 10, "status": []string{"open", "paid"}[i%2]})
		if _, err := e.Insert(ctx, "order", data, "batch"); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func run(t *testing.T, e script.Reader, code string, opts script.Options) (script.Result, error) {
	t.Helper()
	return script.Run(context.Background(), e, code, opts)
}

func TestResultForms(t *testing.T) {
	e := newEngine(t)
	cases := []struct {
		name string
		code string
		want string
	}{
		{"top-level return", `return list("order").length`, `12`},
		{"await", `const orders = await list("order")
return orders.length`, `12`},
		{"last expression", `const orders = list("order"); orders.length`, `12`},
		{"parenthesized last expression", `const n = list("order").length; (n * 2)`, `24`},
		{"main function", `function main() {
  return list("customer").map(c => c.data.name)
}
main()`, `["ann","bob","cy","dee","eve"]`},
		{"async main", `async function main() { return (await list("customer")).length }
main();`, `5`},
		{"arrow IIFE", `(() => ({ n: list("customer").length }))()`, `{"n":5}`},
		{"no result", `const x = 1;`, `null`},
		{"explicit undefined", `return undefined`, `null`},
		{"string", `return "hi"`, `"hi"`},
		{"date", `return new Date(0)`, `"1970-01-01T00:00:00.000Z"`},
		{"trailing comment", "return 1 // done", `1`},
		{"no writes", `[typeof insert, typeof update, typeof remove, typeof require, typeof fetch]`, `["undefined","undefined","undefined","undefined","undefined"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := run(t, e, tc.code, script.Options{})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(res.Value) != tc.want {
				t.Fatalf("got %s, want %s", res.Value, tc.want)
			}
		})
	}
}

func TestListOptions(t *testing.T) {
	e := newEngine(t)
	cases := []struct {
		name string
		code string
		want string
	}{
		{"limit", `list("order", { limit: 3 }).length`, `3`},
		{"sort string desc", `list("order", { sort: "-total", limit: 2 }).map(o => o.data.total)`, `[120,110]`},
		{"sort object", `list("order", { sort: {field: "total", desc: true}, limit: 1 })[0].data.total`, `120`},
		{"sort array", `list("order", { sort: ["status", "-total"], limit: 2 }).map(o => o.data.total)`, `[110,90]`},
		{"compound filter", `list("order", { filter: { op: "and", filters: [
			{ op: "gte", field: "/total", value: 50 }, { op: "eq", field: "status", value: "open" } ] } }).length`, `4`},
		{"labels", `list("order", { filter: { op: "contains", field: "$labels", value: "batch" } }).length`, `12`},
		{"record shape", `const o = list("order", {limit: 1})[0]; [o.recordType, o.schemaVersion, typeof o.id, typeof o.createdAt, o.labels]`, `["order",1,"string","string",["batch"]]`},
		{"get missing", `get("order", "nope")`, `null`},
		{"count all", `count("order")`, `12`},
		{"count filtered", `count("order", { op: "eq", field: "status", value: "paid" })`, `6`},
		{"count unknown type is catchable", `try { count("nope") } catch (e) { return "caught" }`, `"caught"`},
		{"types", `types().map(t => t.name + ":" + t.title + ":" + t.latestVersion)`, `["customer:customers:1","order:orders:1"]`},
		{"describe", `describe("order").latestVersion`, `1`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := run(t, e, tc.code, script.Options{})
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if string(res.Value) != tc.want {
				t.Fatalf("got %s, want %s", res.Value, tc.want)
			}
		})
	}
}

func TestLogs(t *testing.T) {
	e := newEngine(t)
	res, err := run(t, e, `console.log("n =", 3, {a: [1]}); console.error(new Error("boom")); for (let i = 0; i < 5; i++) console.log(i)`, script.Options{MaxLogLines: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`n = 3 {"a":[1]}`, "Error: boom", "0", "... 4 more lines dropped"}
	if strings.Join(res.Logs, "|") != strings.Join(want, "|") {
		t.Fatalf("logs %q", res.Logs)
	}
	if string(res.Value) != "null" {
		t.Fatalf("value %s", res.Value)
	}
	// logs survive errors
	res, err = run(t, e, `console.log("before"); throw new Error("x")`, script.Options{})
	if err == nil || len(res.Logs) != 1 || res.Logs[0] != "before" {
		t.Fatalf("logs on error: %q %v", res.Logs, err)
	}
}

func TestScriptErrors(t *testing.T) {
	e := newEngine(t)
	cases := []struct {
		name string
		code string
		msg  string
	}{
		{"unclosed", `return (`, "unexpected end of script"},
		{"syntax position", `const a = 1
const = 2`, "line 2, column 7"},
		{"plain Error from bindings", `try { list() } catch (e) { throw new Error(e.name) }`, "Error: Error"},
		{"throw", `throw new Error("custom failure")`, "custom failure"},
		{"reference error", `missing.thing`, "missing is not defined"},
		{"unknown type", `list("ghost")`, `list("ghost")`},
		{"bad filter op", `list("order", { filter: { op: "like", field: "x", value: 1 } })`, "unknown filter op"},
		{"mongo-style filter", `list("order", { filter: { status: "open" } })`, "{op, field, value}"},
		{"unknown option", `list("order", { where: {} })`, "where"},
		{"bad limit", `list("order", { limit: 1.5 })`, "limit"},
		{"bad sort", `list("order", { sort: 3 })`, "sort"},
		{"missing type", `list()`, "type must be a non-empty string"},
		{"cyclic result", `const a = {}; a.self = a; a`, "not JSON-serializable"},
		{"bigint result", `10n`, "not JSON-serializable"},
		{"function result", `return () => 1`, "not JSON-serializable"},
		{"never settles", `await new Promise(() => {})`, "never settled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, e, tc.code, script.Options{})
			if !errors.Is(err, script.ErrScript) || !errors.Is(err, errs.ErrInvalid) || errors.Is(err, script.ErrLimit) {
				t.Fatalf("want ErrScript, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error %q lacks %q", err, tc.msg)
			}
		})
	}
}

func TestScriptCanCatchItsOwnMistakes(t *testing.T) {
	e := newEngine(t)
	res, err := run(t, e, `try { list("ghost") } catch (e) { return "caught: " + e.message.includes("ghost") }`, script.Options{})
	if err != nil || string(res.Value) != `"caught: true"` {
		t.Fatalf("%s %v", res.Value, err)
	}
}

func TestLimits(t *testing.T) {
	e := newEngine(t)
	cases := []struct {
		name string
		code string
		opts script.Options
		msg  string
	}{
		{"timeout", `while (true) {}`, script.Options{Timeout: 50 * time.Millisecond}, "longer than 50ms"},
		{"records", `list("order")`, script.Options{MaxRecords: 5}, "more than 5 records"},
		{"records with get", `for (let i = 0; i < 3; i++) list("customer"); 1`, script.Options{MaxRecords: 12}, "more than 12 records"},
		{"calls", `for (;;) get("order", "x")`, script.Options{MaxCalls: 10}, "more than 10 store calls"},
		{"output", `"x".repeat(100)`, script.Options{MaxOutputBytes: 50}, "more than the 50 allowed"},
		{"limit is not catchable", `for (;;) { try { list("order") } catch (e) {} }`, script.Options{MaxRecords: 30}, "more than 30 records"},
		{"timeout is not catchable", `for (;;) { try { while (true) {} } catch (e) {} }`, script.Options{Timeout: 50 * time.Millisecond}, "longer than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(t, e, tc.code, tc.opts)
			if !errors.Is(err, script.ErrLimit) || !errors.Is(err, errs.ErrInvalid) {
				t.Fatalf("want ErrLimit, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error %q lacks %q", err, tc.msg)
			}
		})
	}
	// a limit within the budget is fine
	if res, err := run(t, e, `list("order", {limit: 5}).length`, script.Options{MaxRecords: 5}); err != nil || string(res.Value) != "5" {
		t.Fatalf("limit within budget: %s %v", res.Value, err)
	}
}

// slowReader blocks every store call until the context ends, and can fail
// with an infrastructure error.
type slowReader struct {
	script.Reader
	fail error
}

func (r slowReader) Get(ctx context.Context, recordType string, id string) (mneme.Record, error) {
	if r.fail != nil {
		return mneme.Record{}, r.fail
	}
	<-ctx.Done()
	return mneme.Record{}, ctx.Err()
}

func TestTimeoutDuringStoreCall(t *testing.T) {
	e := newEngine(t)
	start := time.Now()
	_, err := run(t, slowReader{Reader: e}, `try { get("order", "x") } catch (e) {} ; return 1`, script.Options{Timeout: 50 * time.Millisecond})
	if !errors.Is(err, script.ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout did not stop the store call")
	}
}

func TestStoreFailuresPassThrough(t *testing.T) {
	e := newEngine(t)
	down := errors.New("db down")
	_, err := run(t, slowReader{Reader: e, fail: down}, `try { get("order", "x") } catch (e) {} ; return 1`, script.Options{})
	if !errors.Is(err, down) || errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want the store error, got %v", err)
	}
}

func TestCallerCancellation(t *testing.T) {
	e := newEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := script.Run(ctx, e, `while (true) {}`, script.Options{})
	if !errors.Is(err, context.Canceled) || errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRunsAreIsolated(t *testing.T) {
	e := newEngine(t)
	if _, err := run(t, e, `globalThis.leak = 1; Array.prototype.evil = 1`, script.Options{}); err != nil {
		t.Fatal(err)
	}
	res, err := run(t, e, `[typeof leak, typeof [].evil]`, script.Options{})
	if err != nil || string(res.Value) != `["undefined","undefined"]` {
		t.Fatalf("%s %v", res.Value, err)
	}
}

func TestGuideExampleRuns(t *testing.T) {
	e := newEngine(t)
	_, example, ok := strings.Cut(script.Guide, "\nExample:\n")
	if !ok {
		t.Fatal("Guide has no example")
	}
	code := strings.ReplaceAll(example, "\n  ", "\n")
	res, err := run(t, e, code, script.Options{})
	if err != nil {
		t.Fatalf("%v\n%s", err, code)
	}
	want := `[{"customer":"cy","total":180},{"customer":"ann","total":140},{"customer":"bob","total":100}]`
	if string(res.Value) != want {
		t.Fatalf("got %s", res.Value)
	}
}
