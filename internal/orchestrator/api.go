// The work list API: the orchestrator's HTTP surface. While the run works,
// the work list — the items file's items joined with the run's record for
// each, and the output each item's agent kept — is queryable and mutable
// from another client, the gateway's server pattern: an address and a token
// flag, the loopback affordance, the bearer on every path, and a 404 that
// names the surface.

package orchestrator

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spinloop-ai/spinloop/internal/daemon"
)

// The statuses the work list API answers with, named for the 404's sake:
// the surface's replies are JSON, and these are its whole set.
const (
	statusBadRequest          = http.StatusBadRequest
	statusNotFound            = http.StatusNotFound
	statusConflict            = http.StatusConflict
	statusInternalServerError = http.StatusInternalServerError
)

// DefaultListen is where the work list API answers when --listen is not
// given: a fixed port on every interface, the gateway's own rule.
const DefaultListen = ":4010"

// LoopbackListen is where `--loopback` binds the work list API: the default
// port on loopback, the safe bind a local-only orchestrator wants — one that
// Listen's token check accepts without a token.
const LoopbackListen = "127.0.0.1:4010"

// pathsServed is the surface the work list API answers, for the 404 that
// names it.
var pathsServed = []string{
	"/v1/items", "/v1/items/{id}", "/v1/items/{id}/log", "/v1/items/{id}/abort", "/v1/items/{id}/retry", "/health",
}

// Handler is the work list API: the work list it serves, the token its
// callers present, and the log its lines go to.
type Handler struct {
	wl    *WorkList
	token string
	log   *slog.Logger
}

// NewHandler builds the work list API over the work list the run works. The
// token is the one callers must present — empty on loopback, where none is
// needed, as the gateway's allows.
func NewHandler(wl *WorkList, token string, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Handler{wl: wl, token: token, log: log}
}

// Listen opens the work list API's listener, applying the daemon's exposure
// rule: a non-loopback address is refused without a token, because it would
// put the agents' output — and the work's controls — on the network for
// anyone to read.
func Listen(addr, token string) (net.Listener, error) {
	if token == "" && !loopbackAddr(addr) {
		return nil, fmt.Errorf(
			"refusing to serve the work list API on non-loopback %q without a token: "+
				"pass --api-token-file <path>, set %s, or pass --api-token — "+
				"or bind loopback, e.g. --listen 127.0.0.1:4010, which needs none",
			addr, daemon.TokenEnvVar)
	}
	return net.Listen("tcp", addr)
}

// loopbackAddr reports whether a listen address binds only loopback. An
// empty or wildcard host binds every interface, so it is not loopback.
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

// ServeHTTP is the work list API's whole surface: the paths it serves, a
// health check that touches no file, and a 404 that names the rest — every
// call logged with its outcome once the response is complete.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var handler http.HandlerFunc
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/health":
		handler = h.handleHealth
	case r.Method == http.MethodGet && r.URL.Path == "/v1/items":
		handler = h.handleList
	case r.Method == http.MethodPost && r.URL.Path == "/v1/items":
		handler = h.handleAdd
	default:
		id, action, ok := itemPath(r.URL.Path)
		switch {
		case ok && action == "" && r.Method == http.MethodDelete:
			handler = h.handleRemove(id)
		case ok && action == "log" && r.Method == http.MethodGet:
			handler = h.handleLog(id)
		case ok && action == "abort" && r.Method == http.MethodPost:
			handler = h.handleAbort(id)
		case ok && action == "retry" && r.Method == http.MethodPost:
			handler = h.handleRetry(id)
		default:
			handler = h.notFound(r)
		}
	}

	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w}
	h.authenticate(handler)(rec, r)
	status := rec.status
	if status == 0 {
		// A handler that wrote nothing at all still replied 200.
		status = http.StatusOK
	}
	h.log.Log(r.Context(), levelForStatus(status), "api call",
		slog.String("method", r.Method),
		// RequestURI carries the item id for /v1/items/{id} paths, and any
		// query — the work list API takes none, so there is nothing there to
		// leak.
		slog.String("path", r.URL.RequestURI()),
		slog.Int("status", status),
		slog.Duration("duration", time.Since(start)),
		slog.String("remote", r.RemoteAddr),
	)
}

// statusRecorder wraps a ResponseWriter to capture the status a handler
// answered with, so the call log can report the outcome — a refusal or a
// success alike — after the handler has already written it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

// levelForStatus grades a call log by outcome: a refusal the caller could
// have avoided is a warning, a fault on the orchestrator's side an error,
// and a call that simply answers is debug — quiet at the default level, and
// available where an operator turns it up.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}

// itemPath pulls the id — and the action, where the path carries one — out
// of /v1/items/<id>[/<action>].
func itemPath(path string) (id, action string, ok bool) {
	rest, found := strings.CutPrefix(path, "/v1/items/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		if parts[0] == "" {
			return "", "", false
		}
		return parts[0], "", true
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		return parts[0], parts[1], true
	}
	return "", "", false
}

// authenticate gates every request behind the bearer token, on the daemon's
// terms: an empty token means no auth, which Listen permits on loopback
// only.
func (h *Handler) authenticate(next http.HandlerFunc) http.HandlerFunc {
	if h.token == "" {
		return next
	}
	want := []byte("Bearer " + h.token)
	return func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeError(w, http.StatusUnauthorized, fmt.Errorf("missing or invalid bearer token"))
			return
		}
		next(w, r)
	}
}

// notFound answers a method or path the API does not serve, naming the
// surface it does.
func (h *Handler) notFound(r *http.Request) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, statusNotFound, fmt.Errorf(
			"the orchestrator serves %s, not %s %s", strings.Join(pathsServed, ", "), r.Method, r.URL.Path))
	}
}

// handleHealth answers that the orchestrator is up. It touches no file and
// no work on purpose: it is how an operator tells the orchestrator down from
// the fleet down.
func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleList answers the work list: every item the items file carries, in
// the file's order, with its record — backlog where there is none.
func (h *Handler) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": h.wl.List()})
}

// addItemBody is one item as the caller gives it: the fields the items file
// takes.
type addItemBody struct {
	ID           string   `json:"id"`
	Instructions string   `json:"instructions"`
	Dir          string   `json:"dir"`
	Tags         []string `json:"tags"`
	Priority     int      `json:"priority"`
}

// handleAdd takes an item the caller gives: the file's validation on its
// fields, the refusal where the file already carries the id, the refusal
// where the state records it ended, and the file re-written with it where it
// is accepted.
func (h *Handler) handleAdd(w http.ResponseWriter, r *http.Request) {
	var body addItemBody
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, statusBadRequest, fmt.Errorf("reading the request: %v", err))
		return
	}
	if err := json.Unmarshal(data, &body); err != nil {
		writeError(w, statusBadRequest, fmt.Errorf("the request is not a JSON item: %v", err))
		return
	}
	item := Item{
		ID: body.ID, Instructions: body.Instructions, Dir: body.Dir,
		Tags: body.Tags, Priority: body.Priority,
	}
	if err := h.wl.Add(item); err != nil {
		writeError(w, apiStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"object": "item", "id": item.ID})
}

// handleLog answers one item's kept agent output; an item with none is
// answered as having none, not as a fault.
func (h *Handler) handleLog(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		out, ok, err := h.wl.Log(id)
		if err != nil {
			writeError(w, apiStatus(err), err)
			return
		}
		var body any
		if ok {
			body = out
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "log": body})
	}
}

// handleRemove takes an item out of the work list: the items file, its
// record, and its kept output — a running item is refused, naming it and
// the abort that goes first, and an id the file does not carry is refused,
// naming it.
func (h *Handler) handleRemove(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := h.wl.Remove(id); err != nil {
			writeError(w, apiStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	}
}

// handleAbort stops a running item's agent the way a clean interrupt stops
// it and puts the item back in the backlog; an item that is not running is
// refused, naming the item and its state.
func (h *Handler) handleAbort(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := h.wl.Abort(id); err != nil {
			writeError(w, apiStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	}
}

// handleRetry puts a failed item back in the backlog; an item that has not
// failed is refused, naming the item and its state.
func (h *Handler) handleRetry(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := h.wl.Retry(id); err != nil {
			writeError(w, apiStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
	}
}

// writeJSON sends a JSON reply.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError sends a JSON error in the shape the gateway's surface uses.
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": err.Error(), "type": "orchestrator_error"}})
}
