package schema

import (
	"fmt"
	"strings"

	"github.com/fragshq/mneme/errs"
)

// Issue is one schema violation at an instance location (a JSON pointer).
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError reports that a record does not conform to its schema.
// errors.Is(err, errs.ErrInvalid) is true for it.
type ValidationError struct {
	RecordType string  `json:"recordType"`
	Version    int     `json:"version"`
	Issues     []Issue `json:"issues"`
}

func (e *ValidationError) Error() string {
	b := strings.Builder{}
	fmt.Fprintf(&b, "mneme: record does not conform to %s v%d", e.RecordType, e.Version)
	for i, is := range e.Issues {
		if i == 0 {
			b.WriteString(": ")
		} else {
			b.WriteString("; ")
		}
		p := is.Path
		if p == "" {
			p = "/"
		}
		fmt.Fprintf(&b, "%s: %s", p, is.Message)
	}
	return b.String()
}

// Is makes errors.Is(err, errs.ErrInvalid) succeed.
func (e *ValidationError) Is(target error) bool { return target == errs.ErrInvalid }
