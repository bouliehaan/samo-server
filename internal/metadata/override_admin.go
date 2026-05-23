package metadata

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

type MetadataOverrideView struct {
	TargetKind    string         `json:"targetKind"`
	TargetID      string         `json:"targetId"`
	Fields        map[string]any `json:"fields"`
	AllowedFields []string       `json:"allowedFields"`
	UpdatedAt     string         `json:"updatedAt,omitempty"`
}

type MetadataOverrideClearRequest struct {
	Fields []string `json:"fields"`
}

func (s *MetadataApplyService) GetOverride(ctx context.Context, targetKind, targetID string) (MetadataOverrideView, error) {
	if s == nil || s.db == nil {
		return MetadataOverrideView{}, ErrMetadataApplyDisabled
	}
	kind, err := ParseApplyTargetKind(strings.TrimSpace(targetKind))
	if err != nil {
		return MetadataOverrideView{}, err
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return MetadataOverrideView{}, ErrApplyNotFound
	}
	record, err := catalog.GetMetadataOverride(ctx, s.db, string(kind), targetID)
	if err != nil {
		return MetadataOverrideView{}, err
	}
	mediaType := catalog.ShelfMediaTypeBook
	if kind == ApplyTargetShelfItem {
		mediaType, err = s.loadShelfMediaType(ctx, targetID)
		if err != nil {
			return MetadataOverrideView{}, err
		}
	}
	fields := map[string]any{}
	for key, raw := range record.Fields {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return MetadataOverrideView{}, err
		}
		fields[key] = value
	}
	return MetadataOverrideView{
		TargetKind:    string(kind),
		TargetID:      targetID,
		Fields:        fields,
		AllowedFields: AllowedFieldsForTarget(kind, mediaType),
		UpdatedAt:     record.UpdatedAt,
	}, nil
}

func (s *MetadataApplyService) DeleteOverride(ctx context.Context, targetKind, targetID string) error {
	if s == nil || s.db == nil {
		return ErrMetadataApplyDisabled
	}
	kind, err := ParseApplyTargetKind(strings.TrimSpace(targetKind))
	if err != nil {
		return err
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return ErrApplyNotFound
	}
	return catalog.DeleteMetadataOverride(ctx, s.db, string(kind), targetID)
}

func (s *MetadataApplyService) ClearOverrideFields(ctx context.Context, targetKind, targetID string, fields []string) error {
	if s == nil || s.db == nil {
		return ErrMetadataApplyDisabled
	}
	kind, err := ParseApplyTargetKind(strings.TrimSpace(targetKind))
	if err != nil {
		return err
	}
	targetID = strings.TrimSpace(targetID)
	if targetID == "" {
		return ErrApplyNotFound
	}
	mediaType := catalog.ShelfMediaTypeBook
	if kind == ApplyTargetShelfItem {
		mediaType, err = s.loadShelfMediaType(ctx, targetID)
		if err != nil {
			return err
		}
	}
	fields, err = validateClearFields(kind, mediaType, fields)
	if err != nil {
		return err
	}
	return catalog.ClearMetadataOverrideFields(ctx, s.db, string(kind), targetID, fields)
}

func AllowedFieldsForTarget(kind ApplyTargetKind, mediaType catalog.ShelfMediaType) []string {
	return allowedFieldsForTarget(kind, mediaType)
}

func validateClearFields(kind ApplyTargetKind, mediaType catalog.ShelfMediaType, fields []string) ([]string, error) {
	fields = normalizeApplyFields(fields)
	if len(fields) == 0 {
		return nil, ErrEmptyApplyFields
	}
	allowed := allowedFieldsForTarget(kind, mediaType)
	allowedSet := map[string]struct{}{}
	for _, field := range allowed {
		allowedSet[field] = struct{}{}
	}
	for _, field := range fields {
		if _, ok := allowedSet[field]; !ok {
			return nil, ErrInvalidApplyField
		}
	}
	return fields, nil
}
