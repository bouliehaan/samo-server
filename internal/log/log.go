// Package log is samo-server's leveled logger.
//
// It replaces bare log.Printf everywhere. The problem with Printf-only logging
// on an appliance is not style, it is that the operator has no dial: a scanner
// walking 100k files and a failed database write are equally loud, so the
// interesting line is buried, and journald fills with per-file chatter that
// nobody can turn off. SAMO_LOG_LEVEL is that dial.
//
// The output stays plain text on stderr — journald and `docker logs` are the
// consumers, and a human reads them. This is deliberately not JSON: structured
// output helps when a log aggregator is parsing it, which is not the
// deployment this targets. slog does the leveling underneath, so switching the
// handler later is a one-line change here rather than a sweep.
package log

import (
	"context"
	"fmt"
	stdlog "log"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

// levelVar is the live level. Held in an slog.LevelVar so it can be changed at
// runtime without rebuilding the handler.
var levelVar = new(slog.LevelVar)

var handler atomic.Pointer[slog.Logger]

func init() {
	levelVar.Set(parseLevel(os.Getenv("SAMO_LOG_LEVEL")))
	rebuild()
}

func rebuild() {
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: levelVar,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// The stdlib logger already stamps the date/time in the format
			// operators are used to seeing from this server, and journald adds
			// its own. Dropping slog's keeps lines short and familiar.
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
	handler.Store(slog.New(h))
}

// parseLevel maps SAMO_LOG_LEVEL onto a slog level. Unset or unrecognised
// means info: an appliance should be readable by default, and a typo in the
// env var must not silence the log.
func parseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug", "trace":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetLevel changes the active level at runtime. Accepts the same names as
// SAMO_LOG_LEVEL.
func SetLevel(name string) {
	levelVar.Set(parseLevel(name))
}

// Level reports the active level name, for /health and startup banners.
func Level() string {
	switch levelVar.Level() {
	case slog.LevelDebug:
		return "debug"
	case slog.LevelWarn:
		return "warn"
	case slog.LevelError:
		return "error"
	default:
		return "info"
	}
}

// Enabled reports whether a level would be emitted. Use it to skip building an
// expensive message that would be discarded.
func Enabled(level slog.Level) bool {
	return handler.Load().Enabled(context.Background(), level)
}

func logf(level slog.Level, format string, args ...any) {
	l := handler.Load()
	if !l.Enabled(context.Background(), level) {
		return
	}
	l.Log(context.Background(), level, fmt.Sprintf(format, args...))
}

// Debugf is for per-item detail — the file the scanner is on, the URL a probe
// tried. Off by default; this is what an operator turns on to diagnose.
func Debugf(format string, args ...any) { logf(slog.LevelDebug, format, args...) }

// Infof is for things that happened once and matter: startup, a scan finishing,
// a feature being enabled or disabled.
func Infof(format string, args ...any) { logf(slog.LevelInfo, format, args...) }

// Warnf is for a degraded-but-continuing condition: a provider that failed and
// will be retried, a config that was ignored.
func Warnf(format string, args ...any) { logf(slog.LevelWarn, format, args...) }

// Errorf is for something that failed and will not be retried on its own.
func Errorf(format string, args ...any) { logf(slog.LevelError, format, args...) }

// Printf is the drop-in for the stdlib call it replaces, logging at info.
// It exists so the many `Logger func(format string, args ...any)` service
// options keep working unchanged; prefer an explicit level in new code.
func Printf(format string, args ...any) { logf(slog.LevelInfo, format, args...) }

// Fatalf logs at error and exits. Reserve it for main's startup path — a
// background goroutine should degrade, not take the process down.
func Fatalf(format string, args ...any) {
	logf(slog.LevelError, format, args...)
	os.Exit(1)
}

// Fatal logs its arguments at error and exits.
func Fatal(args ...any) {
	logf(slog.LevelError, "%s", fmt.Sprint(args...))
	os.Exit(1)
}

// StdLogger returns a *log.Logger that funnels into this package at the given
// level. It is the bridge for APIs that demand the stdlib type — http.Server's
// ErrorLog, and the channels service's subprocess stderr pump — so their output
// obeys SAMO_LOG_LEVEL like everything else.
func StdLogger(level slog.Level) *stdlog.Logger {
	return stdlog.New(levelWriter{level: level}, "", 0)
}

type levelWriter struct{ level slog.Level }

func (w levelWriter) Write(p []byte) (int, error) {
	logf(w.level, "%s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// Levels re-exported so callers don't need to import log/slog just to name one.
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)
