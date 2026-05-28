package api

import (
	"net/http"
	"time"
)

type serverActivityResponse struct {
	StartedAt     time.Time `json:"startedAt"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	TotalItems    int       `json:"totalItems"`
	Catalog       any       `json:"catalog"`
	LastScan      any       `json:"lastScan,omitempty"`
}

func (s *Server) serverActivity(w http.ResponseWriter, r *http.Request) {
	overview := s.catalog.Overview()
	total := overview.Music.TrackCount + overview.Audiobook.AudiobookCount + overview.Podcast.EpisodeCount

	response := serverActivityResponse{
		StartedAt:     s.startedAt,
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		TotalItems:    total,
		Catalog:       overview,
	}
	if s.libraries != nil {
		jobs, err := s.libraries.ListScanJobs(r.Context(), 1, 0)
		if err == nil && len(jobs.Items) > 0 {
			response.LastScan = jobs.Items[0]
		}
	}
	writeJSON(w, http.StatusOK, response)
}
