package daemon

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// TokenEnvVar names the environment variable carrying the control API's
// bearer token. It is read from the environment — reached by the Outfit's
// adjacent .env — never from a flag, so the secret stays off the command line
// and out of the process table.
const TokenEnvVar = "OUTFIT_API_TOKEN"

// DefaultAPIPort is the control API's port. A fleet client reaching a node
// that states no port assumes this one, so the two sides share the constant
// rather than repeating the number.
const DefaultAPIPort = 4242

// DefaultAPIAddr is where the control API listens unless --api-addr says
// otherwise: DefaultAPIPort on all interfaces, so fleet clients on the network
// can reach it (which is why a non-loopback listen demands a token).
const DefaultAPIAddr = ":4242"

// Listen opens the control API's listener, enforcing the exposure rule: a
// non-loopback address with no token configured is refused — the API can
// start and stop processes, so exposing it unauthenticated beyond the machine
// is never the right default.
func Listen(addr, token string) (net.Listener, error) {
	if token == "" && !loopbackAddr(addr) {
		return nil, fmt.Errorf(
			"refusing to serve the control API on non-loopback %q without a token: set %s (e.g. in the Outfit's .env), or bind loopback with --api-addr 127.0.0.1:4242",
			addr, TokenEnvVar)
	}
	return net.Listen("tcp", addr)
}

// loopbackAddr reports whether a listen address binds only loopback. An empty
// or wildcard host binds every interface, so it is not loopback.
func loopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Route names one endpoint of the control API: the ServeMux pattern it is
// registered under, and the OpenAPI schema its success reply carries.
type Route struct {
	// Pattern is the Go 1.22 method pattern, e.g. "GET /v1/status".
	Pattern string
	// ResponseSchema names this route's success reply in docs/openapi.yaml.
	ResponseSchema string
}

// Routes is the control API's surface, in one list. Handler registers from it
// and the OpenAPI drift test checks docs/openapi.yaml against it, so the mux,
// the spec and this list cannot disagree — a ServeMux cannot enumerate its own
// patterns, which is why the list exists at all. Adding an endpoint means
// adding a line here, a handler in Handler, and a path in the spec; leaving any
// of the three out fails the test.
func Routes() []Route {
	return []Route{
		{"GET /v1/status", "StatusResponse"},
		{"POST /v1/start", "StatusResponse"},
		{"POST /v1/stop", "StatusResponse"},
		{"GET /v1/metrics", "Stats"},
		{"GET /v1/logs", "LogsResponse"},
		{"PUT /v1/deploy-config", "Message"},
	}
}

// Handler builds the control API: status, start, stop, metrics, logs, and
// deploy config, all JSON, all behind the bearer token (when one is set).
func (d *Daemon) Handler(token string) http.Handler {
	handlers := map[string]http.HandlerFunc{
		"GET /v1/status":        d.handleStatus,
		"POST /v1/start":        d.handleStart,
		"POST /v1/stop":         d.handleStop,
		"GET /v1/metrics":       d.handleMetrics,
		"GET /v1/logs":          d.handleLogs,
		"PUT /v1/deploy-config": d.handleDeployConfig,
	}
	mux := http.NewServeMux()
	for _, route := range Routes() {
		handler, ok := handlers[route.Pattern]
		if !ok {
			// Routes() and the table above are edited together; a mismatch is
			// a programming error, not a runtime condition.
			panic("daemon: no handler for route " + route.Pattern)
		}
		mux.HandleFunc(route.Pattern, handler)
	}
	return authenticated(token, mux)
}

func (d *Daemon) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.Status())
}

func (d *Daemon) handleStart(w http.ResponseWriter, r *http.Request) {
	// A start may carry its own deploy config: push-then-start in one
	// call. The already-running check comes first so a 409 stores
	// nothing.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if state, engine, _ := d.Sup.Status(); state == StateRunning {
			writeError(w, http.StatusConflict,
				fmt.Errorf("an engine is already running (%s); stop it first", engine))
			return
		}
		var dc remote.DeployConfig
		if err := json.Unmarshal(body, &dc); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("decoding deploy config: %w", err))
			return
		}
		if err := d.Push(dc); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := d.StartEngine(); err != nil {
		status := http.StatusBadRequest
		if state, _, _ := d.Sup.Status(); state == StateRunning {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, d.Status())
}

func (d *Daemon) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := d.Sup.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, d.Status())
}

func (d *Daemon) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, d.Metrics(r.Context()))
}

// handleLogs serves a bounded slice of the engine's captured output. It is
// read-only: it never touches the engine, so it answers whether the engine is
// running, stopped or crashed — the last of which is when it matters most.
//
// offset is where to read from; omitting it reads the end of the log. limit
// bounds the read and is capped by the daemon regardless of what was asked.
func (d *Daemon) handleLogs(w http.ResponseWriter, r *http.Request) {
	offset, err := int64Param(r, "offset", TailLog)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	limit, err := int64Param(r, "limit", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if offset < 0 && offset != TailLog {
		writeError(w, http.StatusBadRequest, fmt.Errorf("offset cannot be negative"))
		return
	}
	out, err := ReadLog(d.Sup.LogPath, offset, int(limit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// int64Param reads an optional integer query parameter, reporting a
// non-numeric value rather than silently treating it as the default — a
// mistyped cursor should not quietly re-read the whole tail.
func int64Param(r *http.Request, name string, missing int64) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return missing, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number, got %q", name, raw)
	}
	return v, nil
}

func (d *Daemon) handleDeployConfig(w http.ResponseWriter, r *http.Request) {
	var dc remote.DeployConfig
	if err := json.NewDecoder(r.Body).Decode(&dc); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decoding deploy config: %w", err))
		return
	}
	if err := d.Push(dc); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	msg := "stored"
	if state, _, _ := d.Sup.Status(); state == StateRunning {
		msg = "stored; the running engine is untouched — it takes effect on next start"
	}
	writeJSON(w, http.StatusOK, Message{Message: msg})
}

// Message is the control API's plain acknowledgement reply, used where there
// is nothing to report but that the request was accepted.
type Message struct {
	Message string `json:"message"`
}

// Error is the control API's failure reply. Every non-2xx status carries one.
type Error struct {
	Error string `json:"error"`
}

// authenticated gates every request behind the bearer token. An empty token
// means no auth — allowed only on loopback, which Listen enforces.
func authenticated(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, Error{Error: err.Error()})
}
