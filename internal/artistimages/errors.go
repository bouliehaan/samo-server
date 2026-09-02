package artistimages

import (
	"context"
	"errors"
	"net"

	"github.com/bouliehaan/samo-server/internal/covers"
)

var (
	ErrBackfillNotRunning   = errors.New("no artist image backfill is running")
	ErrBackfillNotAvailable = errors.New("artist image backfill is not available")
)

// deezerAPIError is a fault Deezer reports in-band, with HTTP 200 and an error
// object where the results belong — quota exhaustion, most often. It is always
// about us or about Deezer, never about the artist.
type deezerAPIError struct {
	Message string
}

func (e *deezerAPIError) Error() string { return "deezer: " + e.Message }

// A failure to look at two ways. "Transient" asks whether the far end answered
// the question at all, and decides whether the artist may be written off.
// "Retryable" asks whether asking again in a moment could change the answer,
// and decides whether to spend another request. They are not the same question:
// a 403 from a CDN that has blocked our egress range is entirely transient —
// it will lift, and it says nothing whatever about the artist — yet retrying it
// is pointless at best, and at worst it is what deepens the block.
func classifyHTTPStatus(code int) (transient, retryable bool) {
	switch {
	case code == 401, code == 403, code == 407, code == 451:
		// Refused on who we are, not on what we asked. Half a second changes
		// nothing.
		return true, false
	case code == 408, code == 425, code == 429:
		return true, true
	case code >= 500:
		return true, true
	}
	// 404, 410, and anything else: the host answered, and the answer is no.
	return false, false
}

// isTransientLookupError reports whether err leaves the question unanswered
// rather than answering it "no". The distinction is what keeps a blocked CDN or
// an exhausted quota — which fail identically for every artist alive — from
// being written into the negative cache as proof that no photo exists.
func isTransientLookupError(err error) bool {
	transient, _ := classifyLookupError(err)
	return transient
}

// shouldRetryDownload reports whether the same request is worth making again.
func shouldRetryDownload(err error) bool {
	_, retryable := classifyLookupError(err)
	return retryable
}

func classifyLookupError(err error) (transient, retryable bool) {
	if err == nil {
		return false, false
	}

	var status *covers.HTTPStatusError
	if errors.As(err, &status) {
		return classifyHTTPStatus(status.StatusCode)
	}

	var apiErr *deezerAPIError
	if errors.As(err, &apiErr) {
		// Deezer's quota window is seconds wide, so a short wait genuinely helps.
		return true, true
	}

	if errors.Is(err, context.Canceled) {
		// A cancelled run is going away; nothing to retry into.
		return true, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true, true
	}

	// A transport failure — refused, reset, timed out, DNS — is never an answer
	// about the artist, and often clears on its own.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true, true
	}

	return false, false
}
