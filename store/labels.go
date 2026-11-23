package store

import (
	"sort"

	"mneme/query"
)

// CountLabels computes ListLabels' result: the distinct labels of the docs
// matching filter (nil matches all), with their counts, sorted by label in
// byte order. Like Paginate, it suits stores that cannot evaluate filters
// natively.
func CountLabels(docs []*Doc, filter *query.Filter) []LabelCount {
	counts := map[string]int{}
	for _, d := range docs {
		if query.Match(filter, d) {
			for _, l := range d.Record.Labels { // a set, by contract
				counts[l]++
			}
		}
	}
	return sortedCounts(counts)
}

func sortedCounts(counts map[string]int) []LabelCount {
	out := make([]LabelCount, 0, len(counts))
	for l, n := range counts {
		out = append(out, LabelCount{Label: l, Count: n})
	}
	sort.Slice(out, func(i int, j int) bool { return out[i].Label < out[j].Label })
	return out
}
