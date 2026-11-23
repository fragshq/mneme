// Package script runs read-only JavaScript over Mneme records, so a caller
// (typically an LLM behind an MCP query tool) can join, aggregate, reshape or
// compute over data in one request, with logic that the filter AST cannot
// express.
//
// A script is the body of an async function: it may use top-level return and
// await, and if it has no return, the value of its last expression statement
// is the result. These are all equivalent:
//
//	return list("order").length
//
//	const orders = await list("order")
//	return orders.length
//
//	list("order").length
//
// The result is converted with JSON.stringify; undefined becomes null.
//
// The script sees these globals, all synchronous (awaiting them is harmless):
//
//	list(type, {filter, sort, limit})  every matching record, as an array
//	count(type, filter)                the number of matching records
//	get(type, id)                      one record, or null
//	describe(type)                     the record type across its versions
//	types()                            [{name, title, description, latestVersion}]
//	console.log/info/warn/error/debug  lines returned in Result.Logs
//
// Records have their JSON form: {recordType, id, schemaVersion, data, createdAt,
// updatedAt, labels}, with the payload under data. Filters and sorts follow
// package query: filter is the filter AST ({op, field, value} / {op, filters});
// sort is a field name ("-total" for descending), a {field, desc} object or an
// array of either. list pages through the store internally and returns all
// matches, up to limit when given.
//
// There is no access to writes, I/O, timers or modules. Every run gets a fresh
// runtime and is bounded by Options: wall-clock time, records read, store
// calls and output size. Exceeding a limit stops the script (it cannot be
// caught) and returns an error matching ErrLimit. Errors the script causes
// (syntax errors, uncaught exceptions, bad arguments) match ErrScript; both
// match errs.ErrInvalid. Store failures are returned as they are.
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"

	"github.com/fragshq/mneme"
	"github.com/fragshq/mneme/errs"
	"github.com/fragshq/mneme/query"
	"github.com/fragshq/mneme/schema"
)

// Errors returned by Run. Both match errs.ErrInvalid.
var (
	// ErrScript reports a problem in the script itself: a syntax error, an
	// uncaught exception, or a result that is not JSON-serializable.
	ErrScript = errs.Invalidf("script failed")
	// ErrLimit reports that the script exceeded one of its Options limits.
	ErrLimit = errs.Invalidf("script limit exceeded")
)

// Reader is the read-only part of the engine a script may use. *mneme.Engine
// implements it.
type Reader interface {
	Get(ctx context.Context, recordType string, id string) (mneme.Record, error)
	List(ctx context.Context, recordType string, q query.Query) (query.Page[mneme.Record], error)
	Count(ctx context.Context, recordType string, filter *query.Filter) (int, error)
	ListRecordTypes(ctx context.Context) ([]schema.Definition, error)
	DescribeRecordType(ctx context.Context, name string) (mneme.RecordTypeDescription, error)
}

// Options bounds a run. Zero values take the defaults.
type Options struct {
	// Timeout is the wall-clock limit, store calls included (default 5s).
	Timeout time.Duration
	// MaxRecords bounds the records read in total by list and get (default 10000).
	MaxRecords int
	// MaxCalls bounds the store calls; each page list fetches counts (default 1000).
	MaxCalls int
	// MaxOutputBytes bounds the JSON result (default 1 MiB).
	MaxOutputBytes int
	// MaxLogLines bounds the console lines kept; later ones are counted and
	// dropped (default 100).
	MaxLogLines int
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = 5 * time.Second
	}
	if o.MaxRecords <= 0 {
		o.MaxRecords = 10000
	}
	if o.MaxCalls <= 0 {
		o.MaxCalls = 1000
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = 1 << 20
	}
	if o.MaxLogLines <= 0 {
		o.MaxLogLines = 100
	}
	return o
}

// Result is the outcome of a run. Logs is never nil, and is filled even
// when Run returns an error, to help the author debug the script.
type Result struct {
	Value json.RawMessage `json:"value"`
	Logs  []string        `json:"logs"`
}

var errTimeout = errors.New("script timeout")

// Run executes code against r. The context is passed to every store call,
// so tenancy carried in it applies to the script too.
func Run(ctx context.Context, r Reader, code string, opts Options) (Result, error) {
	opts = opts.withDefaults()
	runCtx, cancel := context.WithTimeoutCause(ctx, opts.Timeout, errTimeout)
	defer cancel()
	s := &run{ctx: runCtx, reader: r, opts: opts, vm: goja.New(), logs: []string{}}
	res, err := s.exec(code)
	logs := s.logs
	if s.dropped > 0 {
		logs = append(logs, fmt.Sprintf("... %d more lines dropped", s.dropped))
	}
	return Result{Value: res, Logs: logs}, err
}

// run is the state of one execution. Bindings run on the goroutine that
// executes the script, so only Interrupt is called concurrently.
type run struct {
	ctx      context.Context
	reader   Reader
	opts     Options
	vm       *goja.Runtime
	calls    int
	records  int
	logs     []string
	dropped  int
	abort    error
	toJSON   goja.Callable
	fromJSON goja.Callable
}

const (
	prefix = "(async function () {"
	suffix = "\n})()"
)

func (s *run) exec(code string) (json.RawMessage, error) {
	prog, err := compile(code)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrScript, err)
	}
	if err := s.install(); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(s.ctx, func() { s.vm.Interrupt(errTimeout) })
	defer stop()

	v, err := s.vm.RunProgram(prog)
	if err != nil {
		return nil, s.failure(err)
	}
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		return nil, fmt.Errorf("%w: unexpected result %v", ErrScript, v)
	}
	switch p.State() {
	case goja.PromiseStateRejected:
		return nil, fmt.Errorf("%w: %s", ErrScript, describeThrown(p.Result()))
	case goja.PromiseStatePending:
		return nil, fmt.Errorf("%w: the script awaited a promise that never settled", ErrScript)
	}
	out := p.Result()
	if goja.IsUndefined(out) {
		return json.RawMessage("null"), nil
	}
	js, err := s.toJSON(goja.Undefined(), out)
	if err != nil {
		if s.abort != nil || errors.As(err, new(*goja.InterruptedError)) {
			return nil, s.failure(err)
		}
		return nil, fmt.Errorf("%w: the result is not JSON-serializable: %v", ErrScript, err)
	}
	if goja.IsUndefined(js) {
		return nil, fmt.Errorf("%w: the result is not JSON-serializable (a function or symbol)", ErrScript)
	}
	raw := js.String()
	if len(raw) > s.opts.MaxOutputBytes {
		return nil, fmt.Errorf("%w: the result is %d bytes, more than the %d allowed", ErrLimit, len(raw), s.opts.MaxOutputBytes)
	}
	return json.RawMessage(raw), nil
}

// failure maps an error from goja to the error Run returns.
func (s *run) failure(err error) error {
	if s.abort != nil {
		return s.abort
	}
	if errors.As(err, new(*goja.InterruptedError)) {
		return s.ctxErr()
	}
	return fmt.Errorf("%w: %v", ErrScript, err)
}

// ctxErr is the error for a finished context: a timeout of ours, or the
// caller's own cancellation.
func (s *run) ctxErr() error {
	if errors.Is(context.Cause(s.ctx), errTimeout) {
		return fmt.Errorf("%w: the script ran longer than %s", ErrLimit, s.opts.Timeout)
	}
	return context.Cause(s.ctx)
}

// compile wraps code in an async function and, when its last statement is an
// expression, returns that expression's value.
func compile(code string) (*goja.Program, error) {
	src := prefix + code + suffix
	file, err := parser.ParseFile(nil, "script.js", src, 0)
	if err != nil {
		return nil, syntaxError(err, code)
	}
	if body := wrapperBody(file); len(body) > 0 {
		if last, ok := body[len(body)-1].(*ast.ExpressionStatement); ok {
			body[len(body)-1] = &ast.ReturnStatement{Return: last.Idx0(), Argument: last.Expression}
		}
	}
	return goja.CompileAST(file, false)
}

// syntaxError reports the first parse error at its position in code rather
// than in the wrapped source.
func syntaxError(err error, code string) error {
	list := parser.ErrorList{}
	if !errors.As(err, &list) || len(list) == 0 {
		return err
	}
	first := list[0]
	at := first.Offset - len(prefix)
	if at >= len(code) {
		return fmt.Errorf("syntax error: unexpected end of script (an unclosed bracket, brace or string?)")
	}
	at = max(at, 0)
	before := code[:at]
	line := strings.Count(before, "\n") + 1
	col := at - strings.LastIndex(before, "\n")
	return fmt.Errorf("syntax error at line %d, column %d: %s", line, col, first.Message)
}

// wrapperBody returns the statements of the wrapping function, or nil if the
// code broke out of the wrapper (e.g. "})(); (function () {").
func wrapperBody(file *ast.Program) []ast.Statement {
	if len(file.Body) != 1 {
		return nil
	}
	st, ok := file.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return nil
	}
	call, ok := st.Expression.(*ast.CallExpression)
	if !ok {
		return nil
	}
	fn, ok := call.Callee.(*ast.FunctionLiteral)
	if !ok || fn.Body == nil {
		return nil
	}
	return fn.Body.List
}

// describeThrown renders a thrown value, with its stack when it is an Error.
func describeThrown(v goja.Value) string {
	if st, ok := stackOf(v); ok {
		return st
	}
	return v.String()
}
