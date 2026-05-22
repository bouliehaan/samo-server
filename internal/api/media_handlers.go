package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bouliehaan/samo-server/internal/catalog"
	"github.com/bouliehaan/samo-server/internal/files"
)

func (s *Server) getMediaFile(w http.ResponseWriter, r *http.Request) {
	item, err := s.filesService().GetMediaFile(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFilesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) streamMediaFile(w http.ResponseWriter, r *http.Request) {
	if err := s.filesService().ServeMediaFile(r.Context(), r.PathValue("id"), w, r); err != nil {
		writeFilesError(w, err)
	}
}

func (s *Server) streamMusicTrack(w http.ResponseWriter, r *http.Request) {
	track, err := s.catalog.MusicTrack(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	fileID, err := mediaFileIDFromRequest(r, track.AudioFiles)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.filesService().ServeMediaFile(r.Context(), fileID, w, r); err != nil {
		writeFilesError(w, err)
	}
}

func (s *Server) streamShelfEpisode(w http.ResponseWriter, r *http.Request) {
	episode, err := s.catalog.PodcastEpisode(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	fileID, err := mediaFileIDFromRequest(r, episode.AudioFiles)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.filesService().ServeMediaFile(r.Context(), fileID, w, r); err != nil {
		writeFilesError(w, err)
	}
}

func (s *Server) streamShelfItem(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.ShelfItem(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	fileID, err := mediaFileIDFromRequest(r, item.AudioFiles)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.filesService().ServeMediaFile(r.Context(), fileID, w, r); err != nil {
		writeFilesError(w, err)
	}
}

func mediaFileIDFromRequest(r *http.Request, files []catalog.AudioFile) (string, error) {
	if len(files) == 0 {
		return "", errNoAudioFiles()
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("mediaFileId")); raw != "" {
		for _, file := range files {
			if file.ID == raw {
				return raw, nil
			}
		}
		return "", errUnknownMediaFile()
	}
	return files[0].ID, nil
}

func errNoAudioFiles() error { return fmt.Errorf("no audio files available") }
func errUnknownMediaFile() error {
	return fmt.Errorf("mediaFileId does not belong to this item")
}

func (s *Server) serveMusicAlbumCover(w http.ResponseWriter, r *http.Request) {
	album, err := s.catalog.MusicAlbum(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	s.serveCatalogImage(w, r, album.Images)
}

func (s *Server) serveShelfItemCover(w http.ResponseWriter, r *http.Request) {
	item, err := s.catalog.ShelfItem(r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	var images []catalog.Image
	if item.Cover != nil {
		images = []catalog.Image{*item.Cover}
	}
	s.serveCatalogImage(w, r, images)
}

func (s *Server) serveCatalogImage(w http.ResponseWriter, r *http.Request, images []catalog.Image) {
	path := firstImagePath(images)
	if path == "" {
		writeError(w, http.StatusNotFound, "cover not found")
		return
	}
	if err := s.filesService().ServeLocalPath(r.Context(), path, w, r); err != nil {
		writeFilesError(w, err)
	}
}

func firstImagePath(images []catalog.Image) string {
	for _, image := range images {
		if path := strings.TrimSpace(image.Path); path != "" {
			return path
		}
	}
	return ""
}

func (s *Server) filesService() *files.Service {
	if s.files == nil {
		panic("files service is not configured")
	}
	return s.files
}

func writeFilesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrNotFound):
		writeError(w, http.StatusNotFound, "media file not found")
	case errors.Is(err, files.ErrForbidden):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, files.ErrMissing):
		writeError(w, http.StatusNotFound, "media file is missing on disk")
	case errors.Is(err, files.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
