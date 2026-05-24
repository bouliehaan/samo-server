package metadata

import (
	"context"
	"database/sql"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
)

// MetadataApplyService persists user-confirmed metadata candidates as
// overrides in `metadata_overrides`. It is intentionally domain-aware:
// audiobooks, podcasts, podcast episodes, podcast feeds, and music tracks
// all have separate apply targets so we can validate field sets, candidate
// shapes, and store the override under the correct target_kind.
var _ = catalog.OverrideKindAudiobook // ensure the catalog import is used

// CoverDownloader is the minimal interface the apply service needs to convert
// an external image URL into a locally-cached cover entry. The covers package
// implements it directly; tests can supply a stub.
type CoverDownloader interface {
	DownloadFromURL(ctx context.Context, url string) (*catalog.Image, error)
}

type MetadataApplyService struct {
	db              *sql.DB
	coverDownloader CoverDownloader
	logger          func(string, ...any)
}

// MetadataApplyOptions extends NewMetadataApplyService with optional
// dependencies. Existing callers can keep using NewMetadataApplyService and
// only pay for the downloader by wiring it through main.
type MetadataApplyOptions struct {
	CoverDownloader CoverDownloader
	Logger          func(string, ...any)
}

func NewMetadataApplyService(db *sql.DB) *MetadataApplyService {
	return &MetadataApplyService{db: db}
}

func NewMetadataApplyServiceWithOptions(db *sql.DB, opts MetadataApplyOptions) *MetadataApplyService {
	service := &MetadataApplyService{db: db, coverDownloader: opts.CoverDownloader, logger: opts.Logger}
	if service.logger == nil {
		service.logger = func(string, ...any) {}
	}
	return service
}

// resolveCoverInCandidate downloads the candidate's cover URL into the local
// cover cache when a downloader is configured. On success the candidate's
// Cover gets a stable ID and local Path so apply paths can persist a usable
// reference instead of an external URL. Failures are logged but do not block
// the apply; the URL form is retained.
func (s *MetadataApplyService) resolveCoverInCandidate(ctx context.Context, candidate SearchResult) SearchResult {
	if s == nil || s.coverDownloader == nil {
		return candidate
	}
	if candidate.Cover == nil {
		return candidate
	}
	url := strings.TrimSpace(candidate.Cover.URL)
	if url == "" {
		return candidate
	}
	image, err := s.coverDownloader.DownloadFromURL(ctx, url)
	if err != nil || image == nil {
		if s.logger != nil && err != nil {
			s.logger("cover download failed for %s: %v", url, err)
		}
		return candidate
	}
	merged := *candidate.Cover
	merged.ID = image.ID
	merged.Path = image.Path
	if image.MimeType != "" {
		merged.MimeType = image.MimeType
	}
	merged.URL = url
	candidate.Cover = &merged
	return candidate
}

func (s *MetadataApplyService) Preview(ctx context.Context, request MetadataApplyRequest) (MetadataApplyPreview, error) {
	if s == nil || s.db == nil {
		return MetadataApplyPreview{}, ErrMetadataApplyDisabled
	}
	kind, targetID, fields, candidate, err := s.normalizeRequest(request)
	if err != nil {
		return MetadataApplyPreview{}, err
	}

	before, after, applied, skipped, err := s.mergeTarget(ctx, kind, targetID, candidate, fields, true)
	if err != nil {
		return MetadataApplyPreview{}, err
	}

	return MetadataApplyPreview{
		TargetKind:      string(kind),
		TargetID:        targetID,
		AllowedFields:   allowedFieldsForTarget(kind),
		RequestedFields: fields,
		AppliedFields:   applied,
		SkippedFields:   skipped,
		Before:          before,
		After:           after,
	}, nil
}

func (s *MetadataApplyService) Apply(ctx context.Context, request MetadataApplyRequest) (MetadataApplyResult, error) {
	if s == nil || s.db == nil {
		return MetadataApplyResult{}, ErrMetadataApplyDisabled
	}
	kind, targetID, fields, candidate, err := s.normalizeRequest(request)
	if err != nil {
		return MetadataApplyResult{}, err
	}

	_, _, applied, skipped, err := s.mergeTarget(ctx, kind, targetID, candidate, fields, false)
	if err != nil {
		return MetadataApplyResult{}, err
	}

	return MetadataApplyResult{
		TargetKind:    string(kind),
		TargetID:      targetID,
		AppliedFields: applied,
		SkippedFields: skipped,
	}, nil
}

func (s *MetadataApplyService) normalizeRequest(request MetadataApplyRequest) (ApplyTargetKind, string, []string, SearchResult, error) {
	kind, err := ParseApplyTargetKind(strings.TrimSpace(request.TargetKind))
	if err != nil {
		return "", "", nil, SearchResult{}, err
	}
	targetID := strings.TrimSpace(request.TargetID)
	if targetID == "" {
		return "", "", nil, SearchResult{}, ErrApplyNotFound
	}
	candidate := request.Candidate
	candidate.Provider = strings.TrimSpace(candidate.Provider)
	if strings.TrimSpace(candidate.Title) == "" && strings.TrimSpace(candidate.ID) == "" {
		return "", "", nil, SearchResult{}, ErrInvalidRequest
	}

	fields, err := validateApplyFields(kind, request.Fields)
	if err != nil {
		return "", "", nil, SearchResult{}, err
	}
	if err := validateCandidateForTarget(kind, candidate); err != nil {
		return "", "", nil, SearchResult{}, err
	}
	return kind, targetID, fields, candidate, nil
}

func (s *MetadataApplyService) mergeTarget(
	ctx context.Context,
	kind ApplyTargetKind,
	targetID string,
	candidate SearchResult,
	fields []string,
	dryRun bool,
) (before any, after any, applied []string, skipped []string, err error) {
	if !dryRun {
		candidate = s.resolveCoverInCandidate(ctx, candidate)
	}
	switch kind {
	case ApplyTargetAudiobook:
		return s.applyAudiobook(ctx, targetID, candidate, fields, dryRun)
	case ApplyTargetPodcast:
		return s.applyPodcast(ctx, targetID, candidate, fields, dryRun)
	case ApplyTargetPodcastEpisode:
		return s.applyPodcastEpisode(ctx, targetID, candidate, fields, dryRun)
	case ApplyTargetMusicArtist:
		return s.applyMusicArtist(ctx, targetID, candidate, fields, dryRun)
	case ApplyTargetMusicAlbum:
		return s.applyMusicAlbum(ctx, targetID, candidate, fields, dryRun)
	case ApplyTargetMusicTrack:
		return s.applyMusicTrack(ctx, targetID, candidate, fields, dryRun)
	case ApplyTargetPodcastFeed:
		return s.applyPodcastFeed(ctx, targetID, candidate, fields, dryRun)
	default:
		return nil, nil, nil, nil, ErrInvalidApplyTarget
	}
}

func validateCandidateForTarget(kind ApplyTargetKind, candidate SearchResult) error {
	mediaTypeName := strings.ToLower(strings.TrimSpace(candidate.MediaType))
	switch kind {
	case ApplyTargetAudiobook:
		if mediaTypeName != "" && mediaTypeName != "audiobook" && mediaTypeName != "book" {
			return ErrApplyCandidateKind
		}
	case ApplyTargetPodcast:
		if mediaTypeName != "" && mediaTypeName != "podcast" {
			return ErrApplyCandidateKind
		}
	case ApplyTargetPodcastEpisode:
		if mediaTypeName != "" && mediaTypeName != "podcast" && mediaTypeName != "podcastepisode" {
			return ErrApplyCandidateKind
		}
	case ApplyTargetMusicArtist:
		if mediaTypeName != "" && mediaTypeName != "musicartist" && mediaTypeName != "artist" {
			return ErrApplyCandidateKind
		}
	case ApplyTargetMusicAlbum:
		if mediaTypeName != "" && mediaTypeName != "musicalbum" && mediaTypeName != "album" {
			return ErrApplyCandidateKind
		}
	case ApplyTargetMusicTrack:
		if mediaTypeName != "" && mediaTypeName != "musictrack" && mediaTypeName != "track" && mediaTypeName != "recording" {
			return ErrApplyCandidateKind
		}
	case ApplyTargetPodcastFeed:
		if mediaTypeName != "" && mediaTypeName != "podcast" {
			return ErrApplyCandidateKind
		}
	}
	return nil
}

func partitionApplyFields(fields []string, candidate SearchResult) (applied, skipped []string) {
	for _, field := range fields {
		if candidateHasValue(candidate, field) {
			applied = append(applied, field)
		} else {
			skipped = append(skipped, field)
		}
	}
	return applied, skipped
}
