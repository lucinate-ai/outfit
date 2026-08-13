// outfit's own diagnostic output: the level control, the logger the API hosts
// build, and the middleware that summarises every request the control API
// serves. The engine's output is a separate thing entirely — it is captured to
// the supervisor's log file and served over /v1/logs; this is what outfit says
// about itself while serving that engine.
//
// The logger is injected rather than global (see Daemon.Logger and
// Supervisor.Logger): a nil logger discards, so a daemon constructed directly
// in a test stays silent and the CLI is the one place that decides where
// records go.

package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// LevelEnvVar names the environment variable setting outfit's log level. The
// --log-level flag beats it, matching how --harness beats OUTFIT_HARNESS.
const LevelEnvVar = "OUTFIT_LOG_LEVEL"

// levelNames is the accepted set, in the order the error message lists them.
// slog understands more spellings than these (INFO+2, and every level offset),
// which is why parsing is a switch rather than slog.Level.UnmarshalText: the
// accepted vocabulary is outfit's to state, and an error has to name it.
var levelNames = []string{"debug", "info", "warn", "error"}

// LevelNames lists the accepted level names, for the CLI's flag help and its
// tab completion. It is a copy of the same list ParseLevel switches over, so
// what completes and what parses cannot drift.
func LevelNames() []string {
	return append([]string(nil), levelNames...)
}

// ParseLevel turns a configured level name into a slog level. The empty string
// is the default, info — so a caller can hand it whatever it resolved without
// testing for "unset" first. Anything else unrecognised is an error naming the
// accepted values: a mistyped level that quietly logged at the default would
// be discovered only when the log was needed.
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q: expected one of %s",
		name, strings.Join(levelNames, ", "))
}

// ResolveLevel applies the precedence both API hosts share: the --log-level
// flag, else LevelEnvVar, else info. It lives here rather than in each command
// so the two cannot drift.
func ResolveLevel(flagValue string) (slog.Level, error) {
	if strings.TrimSpace(flagValue) == "" {
		flagValue = os.Getenv(LevelEnvVar)
	}
	return ParseLevel(flagValue)
}

// NewLogger builds the logger an API host writes to. The writer is a parameter
// rather than os.Stderr directly so a command passes os.Stderr at call time —
// a handler built at init would hold the real file, and the daemon tests
// redirect the variable before invoking the command.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

// discardLogger is what a nil Logger field resolves to. slog.Default() was the
// alternative and is deliberately not used: it would make every existing test
// print, and would let an unrelated caller's global handler decide what a
// daemon logs.
var discardLogger = slog.New(slog.DiscardHandler)

// loggerOr substitutes the discarding logger for a nil one, so every call site
// can log unconditionally.
func loggerOr(l *slog.Logger) *slog.Logger {
	if l == nil {
		return discardLogger
	}
	return l
}

// recorder wraps a ResponseWriter to capture what the reply turned out to be:
// the status code and how many bytes of body went out. The status defaults to
// 200 because a handler that writes a body without calling WriteHeader has
// sent one — writeJSON always calls it, but the default keeps a future handler
// honest.
//
// It deliberately does not forward Flusher or Hijacker: no control endpoint
// streams today. If a streaming logs endpoint is ever added, this wrapper has
// to forward Flusher first, or the stream will buffer to the end of the
// request.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// summarize logs one record per request the control API serves, after the
// response is complete.
//
// It wraps the authentication layer rather than sitting inside it, which is
// the point: a request rejected for a bad token is one of the two things this
// exists to make visible, and an unrouted path (the mux's own 404) is how a
// client calling a misspelt endpoint shows up. Both would be invisible from
// inside a handler.
//
// The record carries no header and no body. The token therefore cannot leak
// through it — there is no code path here that reads one, which is a stronger
// guarantee than remembering to redact — and neither a pushed deploy config's
// serve args nor the engine output served over /v1/logs is copied into a log
// that a service manager forwards to a shared journal.
func summarize(logger *slog.Logger, next http.Handler) http.Handler {
	log := loggerOr(logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			// A handler that wrote nothing at all still replied 200.
			status = http.StatusOK
		}
		log.Log(r.Context(), levelForStatus(status), "api request",
			slog.String("method", r.Method),
			// RequestURI carries the query, which is only ever a cursor or a
			// bound. A future endpoint taking something sensitive in a query
			// string would have to revisit this.
			slog.String("path", r.URL.RequestURI()),
			slog.Int("status", status),
			slog.Duration("duration", time.Since(start)),
			slog.Int("bytes", rec.bytes),
			// Host and port as the connection reports them. Behind a proxy
			// this is the proxy: outfit does not trust X-Forwarded-For, since
			// the API is meant to be reached directly.
			slog.String("remote", r.RemoteAddr),
		)
	})
}

// levelForStatus grades a summary by outcome, which is what makes a single
// level knob useful: raising it to warn silences a fleet's polling without
// silencing the rejected token, the malformed cursor or the failed start.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
