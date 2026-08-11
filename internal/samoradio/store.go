package samoradio

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrNotFound means no device with that id.
	ErrNotFound = errors.New("samo-radio device not found")
	// ErrInvalid means the input could not produce a usable device row.
	ErrInvalid = errors.New("invalid samo-radio device")
	// ErrDisabled means the feature has no database behind it.
	ErrDisabled = errors.New("samo-radio is not available")
)

// parseStoredTime accepts both RFC3339 (what we write) and the legacy
// CURRENT_TIMESTAMP format, matching internal/channels and internal/users so
// timestamp handling does not vary per package.
func parseStoredTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, format := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(format, raw); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func newID() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "samoradio_" + hex.EncodeToString(buf), nil
}

// normalizeBaseURL trims and validates a control endpoint. A device URL with a
// path or query would silently produce broken command URLs later, so it is
// rejected here rather than at the first play.
func normalizeBaseURL(raw string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(raw), "/")
	if value == "" {
		return "", fmt.Errorf("%w: base url required", ErrInvalid)
	}
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: base url must be http or https", ErrInvalid)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: base url needs a host", ErrInvalid)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("%w: base url must not include a path", ErrInvalid)
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

const deviceColumns = `id, name, base_url, control_token, stream_base_url,
	token_id, token_user_id, enabled, last_seen_at, last_error, created_at, updated_at`

func scanDevice(scanner interface{ Scan(...any) error }) (Device, error) {
	var (
		device                  Device
		lastSeen, created, upd  string
		controlToken, streamURL string
		tokenID, tokenUserID    string
	)
	if err := scanner.Scan(
		&device.ID,
		&device.Name,
		&device.BaseURL,
		&controlToken,
		&streamURL,
		&tokenID,
		&tokenUserID,
		&device.Enabled,
		&lastSeen,
		&device.LastError,
		&created,
		&upd,
	); err != nil {
		return Device{}, err
	}
	device.controlToken = controlToken
	device.tokenID = tokenID
	device.tokenUserID = tokenUserID
	device.StreamBaseURL = streamURL
	device.Paired = strings.TrimSpace(tokenID) != ""
	device.LastSeenAt = parseStoredTime(lastSeen)
	device.CreatedAt = parseStoredTime(created)
	device.UpdatedAt = parseStoredTime(upd)
	return device, nil
}

func listDevices(ctx context.Context, db *sql.DB) ([]Device, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+deviceColumns+` FROM samo_radio_devices ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	devices := []Device{}
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func getDevice(ctx context.Context, db *sql.DB, id string) (Device, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Device{}, ErrNotFound
	}
	row := db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM samo_radio_devices WHERE id = ?`, id)
	device, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return device, err
}

func insertDevice(ctx context.Context, db *sql.DB, input CreateDeviceInput) (Device, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Device{}, fmt.Errorf("%w: name required", ErrInvalid)
	}
	baseURL, err := normalizeBaseURL(input.BaseURL)
	if err != nil {
		return Device{}, err
	}
	streamBaseURL := strings.TrimRight(strings.TrimSpace(input.StreamBaseURL), "/")
	if streamBaseURL != "" {
		if streamBaseURL, err = normalizeBaseURL(streamBaseURL); err != nil {
			return Device{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return Device{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO samo_radio_devices
			(id, name, base_url, control_token, stream_base_url, token_id, token_user_id,
			 enabled, last_seen_at, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', '', TRUE, '', '', ?, ?)`,
		id, name, baseURL, strings.TrimSpace(input.ControlToken), streamBaseURL, now, now,
	)
	if err != nil {
		return Device{}, err
	}
	return getDevice(ctx, db, id)
}

func updateDevice(ctx context.Context, db *sql.DB, id string, input UpdateDeviceInput) (Device, error) {
	device, err := getDevice(ctx, db, id)
	if err != nil {
		return Device{}, err
	}
	name := device.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
		if name == "" {
			return Device{}, fmt.Errorf("%w: name required", ErrInvalid)
		}
	}
	baseURL := device.BaseURL
	if input.BaseURL != nil {
		if baseURL, err = normalizeBaseURL(*input.BaseURL); err != nil {
			return Device{}, err
		}
	}
	streamBaseURL := device.StreamBaseURL
	if input.StreamBaseURL != nil {
		trimmed := strings.TrimSpace(*input.StreamBaseURL)
		if trimmed == "" {
			streamBaseURL = ""
		} else if streamBaseURL, err = normalizeBaseURL(trimmed); err != nil {
			return Device{}, err
		}
	}
	controlToken := device.controlToken
	if input.ControlToken != nil {
		controlToken = strings.TrimSpace(*input.ControlToken)
	}
	enabled := device.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	_, err = db.ExecContext(ctx, `
		UPDATE samo_radio_devices
		SET name = ?, base_url = ?, control_token = ?, stream_base_url = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		name, baseURL, controlToken, streamBaseURL, enabled,
		time.Now().UTC().Format(time.RFC3339), device.ID,
	)
	if err != nil {
		return Device{}, err
	}
	return getDevice(ctx, db, device.ID)
}

func setDeviceToken(ctx context.Context, db *sql.DB, id, tokenID, tokenUserID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE samo_radio_devices
		SET token_id = ?, token_user_id = ?, last_error = '', updated_at = ?
		WHERE id = ?`,
		strings.TrimSpace(tokenID), strings.TrimSpace(tokenUserID),
		time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// markReachable records a successful conversation. Written on every successful
// state fetch so the UI can show "last seen" for a device that has since gone
// quiet, which is the difference between "it is off" and "it never worked".
func markReachable(ctx context.Context, db *sql.DB, id string) {
	_, _ = db.ExecContext(ctx, `
		UPDATE samo_radio_devices SET last_seen_at = ?, last_error = '' WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id,
	)
}

func markError(ctx context.Context, db *sql.DB, id, message string) {
	if len(message) > 500 {
		message = message[:500]
	}
	_, _ = db.ExecContext(ctx, `UPDATE samo_radio_devices SET last_error = ? WHERE id = ?`, message, id)
}

func deleteDevice(ctx context.Context, db *sql.DB, id string) error {
	result, err := db.ExecContext(ctx, `DELETE FROM samo_radio_devices WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}
