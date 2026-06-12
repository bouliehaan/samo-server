//go:build unix

package chapteraudio

import (
	"os/exec"
	"syscall"
)

// deprioritizeDecoder drops a started ffmpeg child to minimum CPU priority.
// Chapter analysis is a background nicety; serving the API is the job. Without
// this, a library-wide pass (the first boot after an engine version bump
// re-analyzes every book) saturates the CPU and request latency climbs into
// the seconds — which surfaced as "the web UI sits on the booting screen and
// the phone's login times out" right after a deploy. Best-effort: a failure to
// renice never blocks the decode.
func deprioritizeDecoder(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 19)
}
