package script

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dop251/goja"

	"mneme"
	"mneme/errs"
	"mneme/jsonx"
	"mneme/query"
)

// install defines the globals a script may use.
func (s *run) install() error {
	vm := s.vm
	jsonObj := vm.Get("JSON").ToObject(vm)
	stringify, ok1 := goja.AssertFunction(jsonObj.Get("stringify"))
	parse, ok2 := goja.AssertFunction(jsonObj.Get("parse"))
	if !ok1 || !ok2 {
		return errors.New("script: JSON builtins unavailable")
	}
	s.toJSON = stringify
	s.fromJSON = parse

	console := vm.NewObject()
	for _, name := range []string{"log", "info", "warn", "error", "debug"} {
		if err := console.Set(name, s.log); err != nil {
			return err
		}
	}
	for name, fn := range map[string]func(call goja.FunctionCall) goja.Value{
		"list":     s.list,
		"count":    s.count,
		"get":      s.get,
		"describe": s.describe,
		"types":    s.types,
	} {
		if err := vm.Set(name, fn); err != nil {
			return err
		}
	}
	return vm.Set("console", console)
}

// list(type, {filter, sort, limit}) returns every matching record.
func (s *run) list(call goja.FunctionCall) goja.Value {
	recordType := s.stringArg(call, 0, "list", "type")
	q, limit := s.listQuery(call.Argument(1))
	out := []mneme.Record{}
	for {
		want := s.opts.MaxRecords - s.records + 1
		if limit > 0 && limit-len(out) < want {
			want = limit - len(out)
		}
		q.Limit = want
		if !s.spendCall() {
			return goja.Undefined()
		}
		page, err := s.reader.List(s.ctx, recordType, q)
		if err != nil {
			return s.storeError(err, fmt.Sprintf("list(%q)", recordType))
		}
		out = append(out, page.Items...)
		if !s.spendRecords(len(page.Items)) {
			return goja.Undefined()
		}
		if page.NextCursor == "" || (limit > 0 && len(out) >= limit) {
			break
		}
		q.Cursor = page.NextCursor
	}
	return s.value(out)
}

// count(type, filter) returns the number of matching records. It reads no
// records, so it is not charged to the records budget.
func (s *run) count(call goja.FunctionCall) goja.Value {
	recordType := s.stringArg(call, 0, "count", "type")
	f, err := query.ParseFilter(s.marshal(call.Argument(1)))
	if err != nil {
		s.throw(fmt.Sprintf("count: %v", err))
	}
	if !s.spendCall() {
		return goja.Undefined()
	}
	n, err := s.reader.Count(s.ctx, recordType, f)
	if err != nil {
		return s.storeError(err, fmt.Sprintf("count(%q)", recordType))
	}
	return s.vm.ToValue(n)
}

// get(type, id) returns one record, or null when there is none.
func (s *run) get(call goja.FunctionCall) goja.Value {
	recordType := s.stringArg(call, 0, "get", "type")
	id := s.stringArg(call, 1, "get", "id")
	if !s.spendCall() {
		return goja.Undefined()
	}
	r, err := s.reader.Get(s.ctx, recordType, id)
	if errors.Is(err, errs.ErrNotFound) && s.ctx.Err() == nil {
		return goja.Null()
	}
	if err != nil {
		return s.storeError(err, fmt.Sprintf("get(%q, %q)", recordType, id))
	}
	if !s.spendRecords(1) {
		return goja.Undefined()
	}
	return s.value(r)
}

// describe(type) returns the record type merged across its versions.
func (s *run) describe(call goja.FunctionCall) goja.Value {
	recordType := s.stringArg(call, 0, "describe", "type")
	if !s.spendCall() {
		return goja.Undefined()
	}
	d, err := s.reader.DescribeRecordType(s.ctx, recordType)
	if err != nil {
		return s.storeError(err, fmt.Sprintf("describe(%q)", recordType))
	}
	return s.value(d)
}

// types() lists the record types.
func (s *run) types(goja.FunctionCall) goja.Value {
	if !s.spendCall() {
		return goja.Undefined()
	}
	defs, err := s.reader.ListRecordTypes(s.ctx)
	if err != nil {
		return s.storeError(err, "types()")
	}
	return s.value(mneme.Summaries(defs))
}

// listArgs is the JSON form of list's second argument.
type listArgs struct {
	Filter *query.Filter `json:"filter"`
	Sort   query.Sorts   `json:"sort"`
	Limit  *float64      `json:"limit"`
}

// listQuery decodes list's options; limit 0 means no limit.
func (s *run) listQuery(arg goja.Value) (query.Query, int) {
	q := query.Query{}
	if goja.IsUndefined(arg) || goja.IsNull(arg) {
		return q, 0
	}
	if _, ok := arg.(*goja.Object); !ok {
		s.throw("list: the second argument must be an object like {filter, sort, limit}")
	}
	args := listArgs{}
	if err := jsonx.DecodeStrict(s.marshal(arg), &args); err != nil {
		s.throw(fmt.Sprintf("list: bad options (expected {filter, sort, limit}, a filter looks like {op, field, value}): %v", err))
	}
	q.Filter = args.Filter
	q.Sort = args.Sort
	limit := 0
	if args.Limit != nil {
		l := *args.Limit
		if l < 1 || l != float64(int(l)) {
			s.throw("list: limit must be a positive integer")
		}
		limit = int(l)
	}
	return q, limit
}

// log implements console.*: strings print as they are, other values as JSON.
func (s *run) log(call goja.FunctionCall) goja.Value {
	if len(s.logs) >= s.opts.MaxLogLines {
		s.dropped++
		return goja.Undefined()
	}
	parts := []string{}
	for _, a := range call.Arguments {
		parts = append(parts, s.format(a))
	}
	s.logs = append(s.logs, strings.Join(parts, " "))
	return goja.Undefined()
}

func (s *run) format(v goja.Value) string {
	if _, isErr := stackOf(v); isErr {
		return v.String()
	}
	if _, ok := v.(*goja.Object); !ok {
		return v.String()
	}
	js, err := s.toJSON(goja.Undefined(), v)
	if err != nil || goja.IsUndefined(js) {
		return v.String()
	}
	return js.String()
}

// stackOf returns the stack trace of a JS Error, or false for other values.
func stackOf(v goja.Value) (string, bool) {
	o, ok := v.(*goja.Object)
	if !ok {
		return "", false
	}
	st := o.Get("stack")
	if st == nil || goja.IsUndefined(st) || goja.IsNull(st) {
		return "", false
	}
	return st.String(), true
}

// spendCall charges one store call to the budget. On false the script has
// been stopped and the binding must return at once.
func (s *run) spendCall() bool {
	if err := s.ctx.Err(); err != nil {
		s.fail(s.ctxErr())
		return false
	}
	s.calls++
	if s.calls > s.opts.MaxCalls {
		s.fail(fmt.Errorf("%w: more than %d store calls", ErrLimit, s.opts.MaxCalls))
		return false
	}
	return true
}

// spendRecords charges n records read to the budget. On false the script
// has been stopped and the binding must return at once.
func (s *run) spendRecords(n int) bool {
	s.records += n
	if s.records > s.opts.MaxRecords {
		s.fail(fmt.Errorf("%w: more than %d records read; narrow the filter or pass a limit", ErrLimit, s.opts.MaxRecords))
		return false
	}
	return true
}

// storeError throws caller mistakes into the script, where they can be
// caught, and stops the run on anything else (timeouts, store failures).
func (s *run) storeError(err error, what string) goja.Value {
	if s.ctx.Err() != nil {
		return s.fail(s.ctxErr())
	}
	if errors.Is(err, errs.ErrInvalid) || errors.Is(err, errs.ErrNotFound) {
		s.throw(fmt.Sprintf("%s: %v", what, err))
	}
	return s.fail(err)
}

// fail stops the script with an error it cannot catch.
func (s *run) fail(err error) goja.Value {
	if s.abort == nil {
		s.abort = err
	}
	s.vm.Interrupt(err)
	return goja.Undefined()
}

// throw raises a catchable JS Error.
func (s *run) throw(msg string) {
	ctor, ok := goja.AssertConstructor(s.vm.Get("Error"))
	if !ok {
		panic(s.vm.NewGoError(errors.New(msg)))
	}
	e, err := ctor(nil, s.vm.ToValue(msg))
	if err != nil {
		panic(err)
	}
	panic(e)
}

func (s *run) stringArg(call goja.FunctionCall, i int, fn string, name string) string {
	a := call.Argument(i)
	if _, ok := a.Export().(string); !ok || a.String() == "" {
		s.throw(fmt.Sprintf("%s: %s must be a non-empty string", fn, name))
	}
	return a.String()
}

// value converts v to plain JS data through JSON.
func (s *run) value(v any) goja.Value {
	raw, err := json.Marshal(v)
	if err != nil {
		return s.fail(err)
	}
	out, err := s.fromJSON(goja.Undefined(), s.vm.ToValue(string(raw)))
	if err != nil {
		return s.fail(err)
	}
	return out
}

// marshal converts a JS value to JSON.
func (s *run) marshal(v goja.Value) []byte {
	js, err := s.toJSON(goja.Undefined(), v)
	if err != nil {
		s.throw(err.Error())
	}
	if goja.IsUndefined(js) {
		return []byte("null")
	}
	return []byte(js.String())
}
