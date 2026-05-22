package libraries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bouliehaan/samo-server/internal/scanner"
)

func listLibraries(ctx context.Context, db *sql.DB, limit, offset int) (Page, error) {
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE path NOT LIKE 'samo://%'`).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("count libraries: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, name, kind, media_type, path, description, item_count, created_at, updated_at, last_scan_at
		FROM libraries
		WHERE path NOT LIKE 'samo://%'
		ORDER BY kind, name
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return Page{}, fmt.Errorf("list libraries: %w", err)
	}
	defer rows.Close()

	items, err := scanLibraryRows(rows)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func getLibrary(ctx context.Context, db *sql.DB, id string) (Library, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, name, kind, media_type, path, description, item_count, created_at, updated_at, last_scan_at
		FROM libraries
		WHERE id = ?`, id)
	item, err := scanLibraryRow(row)
	if err == sql.ErrNoRows {
		return Library{}, ErrNotFound
	}
	if err != nil {
		return Library{}, fmt.Errorf("get library: %w", err)
	}
	return item, nil
}

func insertLibrary(ctx context.Context, db *sql.DB, item Library) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, description, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		item.ID, item.Name, item.Kind, nullableString(item.MediaType), item.Path, item.Description)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrDuplicatePath
		}
		return fmt.Errorf("insert library: %w", err)
	}
	return nil
}

func updateLibrary(ctx context.Context, db *sql.DB, id string, input UpdateLibraryInput) (Library, error) {
	current, err := getLibrary(ctx, db, id)
	if err != nil {
		return Library{}, err
	}
	if isProtectedLibrary(current) {
		return Library{}, ErrProtectedLibrary
	}

	name := current.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" {
			return Library{}, ErrInvalidLibrary
		}
	}
	description := current.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}

	if input.Path != nil {
		return relocateLibrary(ctx, db, current, strings.TrimSpace(*input.Path), name, description)
	}

	_, err = db.ExecContext(ctx, `
		UPDATE libraries
		SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, name, description, id)
	if err != nil {
		return Library{}, fmt.Errorf("update library: %w", err)
	}
	return getLibrary(ctx, db, id)
}

func relocateLibrary(ctx context.Context, db *sql.DB, current Library, newPath, name, description string) (Library, error) {
	if newPath == "" {
		return Library{}, ErrInvalidLibrary
	}
	absolute, err := filepath.Abs(newPath)
	if err != nil {
		return Library{}, fmt.Errorf("resolve library path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Library{}, ErrPathNotDirectory
		}
		return Library{}, fmt.Errorf("stat library path: %w", err)
	}
	if !info.IsDir() {
		return Library{}, ErrPathNotDirectory
	}

	newID := scanner.LibraryID(current.Kind, current.MediaType, absolute)
	if newID == current.ID && absolute == current.Path {
		_, err = db.ExecContext(ctx, `
			UPDATE libraries
			SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`, name, description, current.ID)
		if err != nil {
			return Library{}, fmt.Errorf("update library metadata: %w", err)
		}
		return getLibrary(ctx, db, current.ID)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Library{}, err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, description, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		newID, name, current.Kind, nullableString(current.MediaType), absolute, description)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return Library{}, ErrDuplicatePath
		}
		return Library{}, fmt.Errorf("insert relocated library: %w", err)
	}

	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE media_files SET library_id = ? WHERE library_id = ?`, []any{newID, current.ID}},
		{`UPDATE shelf_items SET library_id = ? WHERE library_id = ?`, []any{newID, current.ID}},
		{`UPDATE podcast_episodes SET library_id = ? WHERE library_id = ?`, []any{newID, current.ID}},
	} {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return Library{}, fmt.Errorf("relocate library children: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, current.ID); err != nil {
		return Library{}, fmt.Errorf("delete old library: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	return getLibrary(ctx, db, newID)
}

func deleteLibrary(ctx context.Context, db *sql.DB, id string) error {
	current, err := getLibrary(ctx, db, id)
	if err != nil {
		return err
	}
	if isProtectedLibrary(current) {
		return ErrProtectedLibrary
	}

	result, err := db.ExecContext(ctx, `DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete library: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete library rows: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func upsertConfiguredLibrary(ctx context.Context, db *sql.DB, item Library) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, name, kind, media_type, path, description, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
		  name = excluded.name,
		  kind = excluded.kind,
		  media_type = excluded.media_type,
		  path = excluded.path,
		  updated_at = CURRENT_TIMESTAMP`,
		item.ID, item.Name, item.Kind, nullableString(item.MediaType), item.Path, item.Description)
	if err != nil {
		return fmt.Errorf("upsert configured library: %w", err)
	}
	return nil
}

func listScanJobs(ctx context.Context, db *sql.DB, limit, offset int) (ScanJobPage, error) {
	var total int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_jobs`).Scan(&total); err != nil {
		return ScanJobPage{}, fmt.Errorf("count scan jobs: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, status, scope, library_id, trigger_source, started_at, finished_at, error,
		       files_seen, files_pruned, items_pruned
		FROM scan_jobs
		ORDER BY started_at DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return ScanJobPage{}, fmt.Errorf("list scan jobs: %w", err)
	}
	defer rows.Close()

	items, err := scanJobRows(rows)
	if err != nil {
		return ScanJobPage{}, err
	}
	return ScanJobPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func getScanJob(ctx context.Context, db *sql.DB, id string) (ScanJob, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, status, scope, library_id, trigger_source, started_at, finished_at, error,
		       files_seen, files_pruned, items_pruned
		FROM scan_jobs
		WHERE id = ?`, id)
	item, err := scanJobRow(row)
	if err == sql.ErrNoRows {
		return ScanJob{}, ErrScanJobNotFound
	}
	if err != nil {
		return ScanJob{}, fmt.Errorf("get scan job: %w", err)
	}
	return item, nil
}

func insertScanJob(ctx context.Context, db *sql.DB, job ScanJob) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO scan_jobs (
		  id, status, scope, library_id, trigger_source, started_at, finished_at, error,
		  files_seen, files_pruned, items_pruned
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Status, job.Scope, nullableString(job.LibraryID), job.TriggerSource,
		job.StartedAt.UTC().Format(time.RFC3339), timeString(job.FinishedAt), job.Error,
		job.FilesSeen, job.FilesPruned, job.ItemsPruned)
	if err != nil {
		return fmt.Errorf("insert scan job: %w", err)
	}
	return nil
}

func updateScanJob(ctx context.Context, db *sql.DB, job ScanJob) error {
	_, err := db.ExecContext(ctx, `
		UPDATE scan_jobs
		SET status = ?, finished_at = ?, error = ?, files_seen = ?, files_pruned = ?, items_pruned = ?
		WHERE id = ?`,
		job.Status, timeString(job.FinishedAt), job.Error, job.FilesSeen, job.FilesPruned, job.ItemsPruned, job.ID)
	if err != nil {
		return fmt.Errorf("update scan job: %w", err)
	}
	return nil
}

func scanLibraryRows(rows *sql.Rows) ([]Library, error) {
	var items []Library
	for rows.Next() {
		item, err := scanLibraryRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanLibraryRow(scanner interface {
	Scan(dest ...any) error
}) (Library, error) {
	var item Library
	var mediaType sql.NullString
	var createdAt, updatedAt, lastScanAt sql.NullString
	if err := scanner.Scan(
		&item.ID, &item.Name, &item.Kind, &mediaType, &item.Path, &item.Description, &item.ItemCount,
		&createdAt, &updatedAt, &lastScanAt,
	); err != nil {
		return Library{}, err
	}
	item.MediaType = mediaType.String
	item.CreatedAt = parseTimePtr(createdAt)
	item.UpdatedAt = parseTimePtr(updatedAt)
	item.LastScanAt = parseTimePtr(lastScanAt)
	return item, nil
}

func scanJobRows(rows *sql.Rows) ([]ScanJob, error) {
	var items []ScanJob
	for rows.Next() {
		item, err := scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanJobRow(scanner interface {
	Scan(dest ...any) error
}) (ScanJob, error) {
	var item ScanJob
	var libraryID sql.NullString
	var startedAt string
	var finishedAt sql.NullString
	if err := scanner.Scan(
		&item.ID, &item.Status, &item.Scope, &libraryID, &item.TriggerSource, &startedAt, &finishedAt,
		&item.Error, &item.FilesSeen, &item.FilesPruned, &item.ItemsPruned,
	); err != nil {
		return ScanJob{}, err
	}
	item.LibraryID = libraryID.String
	item.StartedAt = parseTime(startedAt)
	item.FinishedAt = parseTimePtr(finishedAt)
	return item, nil
}

func isProtectedLibrary(item Library) bool {
	return strings.HasPrefix(item.Path, "samo://")
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func timeString(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339)
}

func parseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func parseTimePtr(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed := parseTime(value.String)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}
