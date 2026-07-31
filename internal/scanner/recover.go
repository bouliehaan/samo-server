package scanner

import (
	"fmt"
	"runtime/debug"

	"github.com/bouliehaan/samo-server/internal/log"
)

// recoverToError runs fn and converts a panic into an ordinary error.
//
// The scanner parses whatever bytes are on disk: truncated downloads, files
// renamed to the wrong extension, and tag frames written by decades of buggy
// encoders. Third-party parsers do panic on that input — an out-of-range slice
// on a malformed ID3 frame is the classic — and because probing runs on its own
// goroutine, no caller's recover() covers it. The panic would take the whole
// server down, and the next scan would reach the same file and take it down
// again: a permanent crash loop caused by one bad file in a library.
//
// Turning the panic into an error demotes that to "this one file failed to
// probe", which is a state the scanner already knows how to carry.
func recoverToError(what string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panicked: %v", what, r)
			log.Warnf("panic recovered while scanning %s: %v\n%s", what, r, debug.Stack())
		}
	}()
	return fn()
}
