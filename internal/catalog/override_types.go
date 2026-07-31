package catalog

import (
	"encoding/json"
	"errors"
)

// Metadata-override vocabulary.
//
// These are model types, not storage detail. The projection code in this
// package reads and merges patches without ever touching a database; only the
// SQL that loads and persists them lives in internal/catalogstore. Keeping the
// types here is what lets that split happen without the store owning the
// domain's language.

// MetadataOverrideKey identifies a user-applied metadata override target.
type MetadataOverrideKey struct {
	TargetKind string
	TargetID   string
}

// MetadataOverridePatch stores field-level override values keyed by apply field name.
type MetadataOverridePatch map[string]json.RawMessage

// MetadataOverrideRecord is one persisted override as the API returns it.
type MetadataOverrideRecord struct {
	TargetKind string                `json:"targetKind"`
	TargetID   string                `json:"targetId"`
	Fields     MetadataOverridePatch `json:"fields"`
	UpdatedAt  string                `json:"updatedAt,omitempty"`
}

// ErrMetadataOverrideNotFound is returned when no override exists for a target.
var ErrMetadataOverrideNotFound = errors.New("metadata override not found")
