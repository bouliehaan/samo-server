package libraries

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/bouliehaan/samo-server/internal/storage"
)

// scanProgressReporter keeps scan_jobs.files_seen and the in-memory current path
// updated while a scan runs. files_seen is flushed at most every ~2s (heartbeat)
// so progress updates do not contend with per-file catalog writes.
type scanProgressReporter struct {
	service *Service
	jobID   string
	seen    atomic.Int64
	stop    chan struct{}
}

func (s *Service) newScanProgressReporter(jobID string) *scanProgressReporter {
	return &scanProgressReporter{
		service: s,
		jobID:   jobID,
		stop:    make(chan struct{}),
	}
}

func (r *scanProgressReporter) Start() {
	r.setActivity("starting scan")
	r.persistSeen(0)
	go r.heartbeat()
}

func (r *scanProgressReporter) Stop() {
	close(r.stop)
}

// Flush writes the latest files_seen once (with retries). Call after the scan
// goroutine returns so the UI and terminal job row see the final count.
func (r *scanProgressReporter) Flush() {
	r.persistSeen(int(r.seen.Load()))
}

func (r *scanProgressReporter) SetActivity(msg string) {
	r.setActivity(msg)
}

func (r *scanProgressReporter) AddSeen(delta int) {
	if delta <= 0 {
		return
	}
	r.seen.Add(int64(delta))
}

func (r *scanProgressReporter) SetSeen(total int) {
	if total < 0 {
		total = 0
	}
	r.seen.Store(int64(total))
}

func (r *scanProgressReporter) setActivity(msg string) {
	r.service.scanMu.Lock()
	if r.service.activeJobID == r.jobID {
		r.service.activeScanPath = msg
	}
	r.service.scanMu.Unlock()
}

func (r *scanProgressReporter) persistSeen(total int) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	err := storage.Retry(ctx, 10, func() error {
		_, err := r.service.db.ExecContext(ctx,
			`UPDATE scan_jobs SET files_seen = ? WHERE id = ? AND status IN (?, ?)`,
			total, r.jobID, ScanStatusRunning, ScanStatusPending)
		return err
	})
	if err != nil {
		log.Printf("libraries: scan job %q progress update failed: %v", r.jobID, err)
	}
}

func (r *scanProgressReporter) heartbeat() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	pulse := 0
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			pulse++
			seen := int(r.seen.Load())
			r.persistSeen(seen)
			if pulse%5 == 0 {
				log.Printf("libraries: scan job %q still running (%d files enumerated)", r.jobID, seen)
			}
		}
	}
}
