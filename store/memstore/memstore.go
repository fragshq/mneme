// Package memstore is an in-memory implementation of every Mneme storage
// interface. It defines the reference behaviour for the conformance suite and
// is handy for tests and prototypes. Data is lost when the process exits.
//
// Records are decoded once, when written, and kept as immutable store.Docs:
// a write replaces the Doc, never changes it. So a list evaluates the query
// over the stored Docs without decoding or copying them, and copies only the
// records it returns.
package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"mneme/query"
	"mneme/schema"
	"mneme/store"
)

// Store implements store.SchemaStore, store.RecordStore and
// store.SequenceProvider. The zero value is not usable; call New.
type Store struct {
	mu        sync.RWMutex
	defs      map[string][]schema.Definition   // name -> versions ascending
	records   map[string]map[string]*store.Doc // record type -> id -> record
	sequences map[seqKey]int64
}

type seqKey struct{ recordType, name, scope string }

var (
	_ store.SchemaStore      = (*Store)(nil)
	_ store.RecordStore      = (*Store)(nil)
	_ store.SequenceProvider = (*Store)(nil)
)

// New returns an empty store.
func New() *Store {
	return &Store{
		defs:      map[string][]schema.Definition{},
		records:   map[string]map[string]*store.Doc{},
		sequences: map[seqKey]int64{},
	}
}

// PutDefinition implements store.SchemaStore.
func (s *Store) PutDefinition(_ context.Context, d schema.Definition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.defs[d.Name]
	for _, v := range vs {
		if v.Version == d.Version {
			return store.VersionExists(d.Name, d.Version)
		}
	}
	d, err := d.Clone() // callers must not be able to mutate stored versions
	if err != nil {
		return err
	}
	vs = append(vs, d)
	sort.Slice(vs, func(i int, j int) bool { return vs[i].Version < vs[j].Version })
	s.defs[d.Name] = vs
	return nil
}

// GetDefinition implements store.SchemaStore.
func (s *Store) GetDefinition(_ context.Context, name string, version int) (schema.Definition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vs := s.defs[name]
	if len(vs) == 0 {
		return schema.Definition{}, store.TypeNotFound(name)
	}
	if version == 0 {
		return vs[len(vs)-1].Clone()
	}
	for _, v := range vs {
		if v.Version == version {
			return v.Clone()
		}
	}
	return schema.Definition{}, store.VersionNotFound(name, version)
}

// ListDefinitions implements store.SchemaStore.
func (s *Store) ListDefinitions(context.Context) ([]schema.Definition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]schema.Definition, 0, len(s.defs))
	for _, vs := range s.defs {
		d, err := vs[len(vs)-1].Clone()
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i int, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListVersions implements store.SchemaStore.
func (s *Store) ListVersions(_ context.Context, name string) ([]schema.Definition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vs := s.defs[name]
	if len(vs) == 0 {
		return nil, store.TypeNotFound(name)
	}
	out := make([]schema.Definition, 0, len(vs))
	for _, v := range vs {
		d, err := v.Clone()
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// doc copies r, so the caller can't mutate the stored record, and decodes
// its data.
func doc(r store.Record) (*store.Doc, error) {
	d, err := store.NewDoc(r.Normalized())
	if err != nil {
		return nil, fmt.Errorf("record %s/%s: decoding data: %w", r.RecordType, r.ID, err)
	}
	return d, nil
}

// InsertRecord implements store.RecordStore.
func (s *Store) InsertRecord(_ context.Context, r store.Record) error {
	d, err := doc(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.records[r.RecordType]
	if m == nil {
		m = map[string]*store.Doc{}
		s.records[r.RecordType] = m
	}
	if _, ok := m[r.ID]; ok {
		return store.RecordExists(r.RecordType, r.ID)
	}
	m[r.ID] = d
	return nil
}

// GetRecord implements store.RecordStore.
func (s *Store) GetRecord(_ context.Context, recordType string, id string) (store.Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.records[recordType][id]
	if !ok {
		return store.Record{}, store.RecordNotFound(recordType, id)
	}
	return d.Record.Normalized(), nil
}

// UpdateRecord implements store.RecordStore.
func (s *Store) UpdateRecord(_ context.Context, r store.Record) error {
	d, err := doc(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[r.RecordType][r.ID]; !ok {
		return store.RecordNotFound(r.RecordType, r.ID)
	}
	s.records[r.RecordType][r.ID] = d
	return nil
}

// DeleteRecord implements store.RecordStore.
func (s *Store) DeleteRecord(_ context.Context, recordType string, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[recordType][id]; !ok {
		return store.RecordNotFound(recordType, id)
	}
	delete(s.records[recordType], id)
	return nil
}

// ListRecords implements store.RecordStore using the reference evaluator.
// It snapshots the type's Docs under the lock and evaluates the query after
// releasing it: Docs are never modified, only replaced.
func (s *Store) ListRecords(_ context.Context, recordType string, q query.Query) (query.Page[store.Record], error) {
	page, err := store.Paginate(s.snapshot(recordType), q)
	if err != nil {
		return page, err
	}
	for i, r := range page.Items {
		page.Items[i] = r.Normalized()
	}
	return page, nil
}

// ListLabels implements store.RecordStore using the reference evaluator,
// over a snapshot of the type's Docs like ListRecords.
func (s *Store) ListLabels(_ context.Context, recordType string, filter *query.Filter) ([]store.LabelCount, error) {
	return store.CountLabels(s.snapshot(recordType), filter), nil
}

// CountRecords implements store.RecordStore using the reference evaluator,
// over a snapshot of the type's Docs like ListRecords.
func (s *Store) CountRecords(_ context.Context, recordType string, filter *query.Filter) (int, error) {
	return store.Count(s.snapshot(recordType), filter), nil
}

// snapshot returns the type's Docs, taken under the read lock. Docs are
// never modified, only replaced, so they can be read after releasing it.
func (s *Store) snapshot(recordType string) []*store.Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	docs := make([]*store.Doc, 0, len(s.records[recordType]))
	for _, d := range s.records[recordType] {
		docs = append(docs, d)
	}
	return docs
}

// DeleteDefinitions implements store.SchemaStore.
func (s *Store) DeleteDefinitions(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.defs[name]) == 0 {
		return store.TypeNotFound(name)
	}
	delete(s.defs, name)
	return nil
}

// DeleteRecords implements store.RecordStore.
func (s *Store) DeleteRecords(_ context.Context, recordType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, recordType)
	return nil
}

// DeleteSequences implements store.SequenceProvider.
func (s *Store) DeleteSequences(_ context.Context, recordType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.sequences {
		if k.recordType == recordType {
			delete(s.sequences, k)
		}
	}
	return nil
}

// Next implements store.SequenceProvider.
func (s *Store) Next(_ context.Context, recordType string, name string, scope string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := seqKey{recordType, name, scope}
	s.sequences[k]++
	return s.sequences[k], nil
}
