package scanner

import (
	"sync/atomic"
)

// scanState holds per-invocation scanner state passed through Navidrome-style phases.
type scanState struct {
	fullScan        bool
	scanMode        string
	subpaths        []string
	changesDetected atomic.Bool
}

func newScanState(fullScan bool, scanMode string, subpaths []string) *scanState {
	state := &scanState{
		fullScan: fullScan,
		scanMode: scanMode,
		subpaths: subpaths,
	}
	if fullScan {
		state.changesDetected.Store(true)
	}
	return state
}

func (s *scanState) noteChange() {
	s.changesDetected.Store(true)
}
