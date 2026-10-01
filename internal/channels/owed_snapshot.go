package channels

import (
	"context"
	"time"
)

// owedSnapshot uses the scheduler's discovery and already-heard rules against
// a private store. Keep stored credit, settled rows and disabled-source rows;
// the latter are still explained as held by the service. No decision is made
// and no writes reach the real obligation store.
func (e *Engine) owedSnapshot(ctx context.Context, now time.Time, stored []Obligation) (*Engine, []Obligation) {
	snapshot := *e
	memory := NewMemoryObligations()
	_ = memory.Notice(ctx, stored, now)
	snapshot.Obligations = memory
	snapshot.ReadOnly = false // discovery writes only to this private memory
	snapshot.refreshObligations(ctx, now, snapshot.enumerationEnv(ctx, now, snapshot.location()))
	snapshot.ReadOnly = true
	current, _ := memory.List(ctx, now)
	return &snapshot, current
}
