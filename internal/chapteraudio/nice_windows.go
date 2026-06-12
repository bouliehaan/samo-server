//go:build windows

package chapteraudio

import "os/exec"

// deprioritizeDecoder is a no-op on Windows; syscall.Setpriority is not
// available there. The unix build carries the real implementation (and the
// rationale).
func deprioritizeDecoder(_ *exec.Cmd) {}
