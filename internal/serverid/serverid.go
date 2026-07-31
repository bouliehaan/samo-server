// Package serverid owns this server's stable identity.
//
// The ID is minted once, on first boot, and persists for the life of the
// database. Clients key their local catalog mirror, downloads, and playback
// progress by it instead of by the server URL, so an address change (LAN IP to
// tunnel hostname, http to https, a new port) no longer reads as a different
// server and no longer orphans on-device data.
package serverid

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// idBytes is the entropy behind a server ID. 16 bytes matches the user and
// token IDs elsewhere in the codebase.
const idBytes = 16

// Prefix marks the value as a server identity in logs and on the wire.
const Prefix = "srv-"

func newID() (string, error) {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate server id: %w", err)
	}
	return Prefix + hex.EncodeToString(buf), nil
}

// Ensure returns this server's identity, minting and persisting one if the
// database does not have it yet.
//
// Safe to call concurrently and on every boot: the insert is conditional, so
// whichever caller wins the race, every caller reads back the same row. The ID
// is never regenerated once stored -- doing so would detach every client from
// its local data.
func Ensure(ctx context.Context, db *sql.DB) (string, error) {
	if db == nil {
		return "", fmt.Errorf("server identity: nil database")
	}

	candidate, err := newID()
	if err != nil {
		return "", err
	}

	// Conditional insert first, then an unconditional read. The read is what
	// determines the result, so a caller that loses the insert race still
	// returns the winner's ID rather than its own discarded candidate.
	if _, err := db.ExecContext(ctx, `
		INSERT INTO server_identity (singleton, server_id)
		VALUES (TRUE, ?)
		ON CONFLICT (singleton) DO NOTHING`, candidate); err != nil {
		return "", fmt.Errorf("store server identity: %w", err)
	}

	var stored string
	if err := db.QueryRowContext(ctx, `
		SELECT server_id FROM server_identity WHERE singleton = TRUE`).Scan(&stored); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("server identity missing after insert")
		}
		return "", fmt.Errorf("load server identity: %w", err)
	}

	return stored, nil
}
