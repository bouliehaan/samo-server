-- migration 018: uploaded cover art for internet radio stations
--
-- image_url remains for legacy remote URLs; cover_id points at a row in
-- extracted_covers for admin-uploaded thumbnails served via
-- /api/v1/media/covers/{id}/image.

ALTER TABLE internet_radio_stations ADD COLUMN cover_id TEXT NOT NULL DEFAULT '';
