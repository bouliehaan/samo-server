package scanner

import (
	"context"
	"database/sql"
	"log"
	"sync/atomic"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
)

type subprocessProgress struct {
	db    *sql.DB
	jobID string
	seen  atomic.Int64
	stop  chan struct{}
}

func newSubprocessProgress(db *sql.DB, jobID string) *subprocessProgress {
	return &subprocessProgress{db: db, jobID: jobID, stop: make(chan struct{})}
}

func (p *subprocessProgress) Start() {
	if p == nil || p.db == nil || p.jobID == "" {
		return
	}
	p.SetActivity("starting scan (subprocess)")
	p.persistSeen(0)
	go p.heartbeat()
}

func (p *subprocessProgress) Stop() {
	if p == nil {
		return
	}
	close(p.stop)
}

func (p *subprocessProgress) Flush() {
	if p == nil {
		return
	}
	p.persistSeen(int(p.seen.Load()))
}

func (p *subprocessProgress) SetSeen(total int) {
	if p == nil || p.jobID == "" {
		return
	}
	if total < 0 {
		total = 0
	}
	p.seen.Store(int64(total))
}

func (p *subprocessProgress) SetActivity(msg string) {
	if p == nil || msg == "" {
		return
	}
	log.Printf("scanner: subprocess job %q: %s", p.jobID, msg)
}

func (p *subprocessProgress) persistSeen(total int) {
	if p == nil || p.db == nil || p.jobID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := storage.Retry(ctx, 10, func() error {
		_, err := p.db.ExecContext(ctx,
			`UPDATE scan_jobs SET files_seen = ? WHERE id = ? AND status IN ('running', 'pending')`,
			total, p.jobID)
		return err
	})
	if err != nil {
		log.Printf("scanner: subprocess scan job %q progress update failed: %v", p.jobID, err)
	}
}

func (p *subprocessProgress) heartbeat() {
	if p == nil || p.jobID == "" {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			p.persistSeen(int(p.seen.Load()))
		}
	}
}
