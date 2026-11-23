package store

import "fmt"

// The constructors below build the errors stores return, so every store
// reports the same condition with the same message. Each matches its
// sentinel with errors.Is.

// TypeNotFound reports that no version of a record type exists.
func TypeNotFound(recordType string) error {
	return fmt.Errorf("%w: record type %s", ErrNotFound, recordType)
}

// VersionNotFound reports that a version of a record type does not exist;
// version 0 means the latest.
func VersionNotFound(recordType string, version int) error {
	if version == 0 {
		return TypeNotFound(recordType)
	}
	return fmt.Errorf("%w: record type %s v%d", ErrNotFound, recordType, version)
}

// VersionExists reports that a version of a record type is already stored.
func VersionExists(recordType string, version int) error {
	return fmt.Errorf("%w: record type %s v%d already exists", ErrConflict, recordType, version)
}

// RecordNotFound reports that a record does not exist.
func RecordNotFound(recordType string, id string) error {
	return fmt.Errorf("%w: record %s/%s", ErrNotFound, recordType, id)
}

// RecordExists reports that a record ID is already taken in its record type.
func RecordExists(recordType string, id string) error {
	return fmt.Errorf("%w: record %s/%s already exists", ErrConflict, recordType, id)
}
