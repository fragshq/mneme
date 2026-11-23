package mneme

import (
	"slices"
	"unicode/utf8"

	"mneme/errs"
)

// Label limits.
const (
	MaxLabels      = 64
	MaxLabelLength = 128 // characters
)

// normalizeLabels validates labels and returns them as a sorted,
// de-duplicated, non-nil set.
func normalizeLabels(labels []string) ([]string, error) {
	for _, l := range labels {
		if l == "" {
			return nil, errs.Invalidf("labels must not be empty strings")
		}
		if n := utf8.RuneCountInString(l); n > MaxLabelLength {
			return nil, errs.Invalidf("label %.20q... is %d characters, max %d", l, n, MaxLabelLength)
		}
	}
	out := slices.Compact(slices.Sorted(slices.Values(labels)))
	if len(out) > MaxLabels {
		return nil, errs.Invalidf("%d labels, max %d", len(out), MaxLabels)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
