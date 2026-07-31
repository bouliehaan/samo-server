package api

import (
	"context"
	"database/sql"
	"net/http"
	"sync"
	"time"
)

const (
	// healthPingTimeout bounds the database probe. A healthy local Postgres
	// answers in microseconds; anything slower than this is a problem the
	// operator wants surfaced, not waited on.
	healthPingTimeout = 2 * time.Second

	// healthCacheTTL throttles the probe. /health is unauthenticated and is
	// polled by container/systemd health checks and by clients deciding which
	// address to use, so an uncached ping would let anyone saturate the
	// connection pool from outside. One second keeps the answer honest while
	// collapsing a flood into a single round-trip.
	healthCacheTTL = time.Second
)

// healthResponse is the reachability-and-liveness probe. Clients use it to
// decide whether an address is usable AND is the server they already know
// (see ServerID); orchestrators use the status code to decide whether to
// restart. Fields are additive per the /api/v1 compatibility contract.
type healthResponse struct {
	OK        bool      `json:"ok"`
	Service   string    `json:"service"`
	Timestamp time.Time `json:"timestamp"`
	// ServerID lets a client confirm that an address it just probed is the
	// server it already knows, which is what makes local-vs-remote endpoint
	// selection safe: reachability alone cannot tell two servers apart.
	ServerID      string       `json:"serverId,omitempty"`
	UptimeSeconds int64        `json:"uptimeSeconds"`
	Checks        healthChecks `json:"checks"`
}

type healthChecks struct {
	Database databaseHealth `json:"database"`
}

// databaseHealth reports both reachability and pool pressure. The pool numbers
// are what turn "the server feels slow" into an answer: WaitCount climbing
// means requests are queueing for a connection, which is the shape scans and
// interactive traffic contending produce.
type databaseHealth struct {
	OK         bool   `json:"ok"`
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
	LatencyMs  int64  `json:"latencyMs"`
	InUse      int    `json:"inUse"`
	Idle       int    `json:"idle"`
	MaxOpen    int    `json:"maxOpen"`
	WaitCount  int64  `json:"waitCount"`
}

// healthProbe caches the most recent database probe so a burst of health
// requests costs one round-trip rather than one each.
type healthProbe struct {
	mu     sync.Mutex
	last   databaseHealth
	lastAt time.Time
}

func (p *healthProbe) check(ctx context.Context, db *sql.DB) databaseHealth {
	if db == nil {
		// No handle at all — the API is running in a configuration that has no
		// database (unit tests, and nothing else). Report it plainly rather
		// than claiming a failure the operator can't act on.
		return databaseHealth{OK: true, Configured: false}
	}

	p.mu.Lock()
	if !p.lastAt.IsZero() && time.Since(p.lastAt) < healthCacheTTL {
		cached := p.last
		p.mu.Unlock()
		return cached
	}
	p.mu.Unlock()

	pingCtx, cancel := context.WithTimeout(ctx, healthPingTimeout)
	defer cancel()

	started := time.Now()
	err := db.PingContext(pingCtx)
	latency := time.Since(started)

	stats := db.Stats()
	result := databaseHealth{
		OK:         err == nil,
		Configured: true,
		LatencyMs:  latency.Milliseconds(),
		InUse:      stats.InUse,
		Idle:       stats.Idle,
		MaxOpen:    stats.MaxOpenConnections,
		WaitCount:  stats.WaitCount,
	}
	if err != nil {
		result.Error = err.Error()
	}

	p.mu.Lock()
	p.last = result
	p.lastAt = time.Now()
	p.mu.Unlock()
	return result
}

// health answers the liveness probe. It returns 503 when the database is
// unreachable: a samo-server without Postgres can serve nothing but this
// endpoint, so reporting 200 would tell a container runtime, a systemd
// watchdog, or an uptime monitor that a completely broken box is fine — which
// is exactly how a self-hosted appliance ends up dead for a week unnoticed.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	database := s.healthProbe.check(r.Context(), s.db)

	status := http.StatusOK
	if !database.OK {
		status = http.StatusServiceUnavailable
	}

	writeJSON(w, status, healthResponse{
		OK:        database.OK,
		Service:   "samo-server",
		Timestamp: time.Now().UTC(),
		// Still reported when unhealthy: the identity is cached after the first
		// success, so a client probing a degraded server can still tell which
		// server it reached.
		ServerID:      s.serverIdentity(r.Context()),
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Checks:        healthChecks{Database: database},
	})
}
