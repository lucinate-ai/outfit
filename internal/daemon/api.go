package daemon

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/lucinate-ai/outfit/internal/remote"
)

// TokenEnvVar names the environment variable carrying the control API's
// bearer token. It is read from the environment — reached by the Outfit's
// adjacent .env — never from a flag, so the secret stays off the command line
// and out of the process table.
const TokenEnvVar = "OUTFIT_API_TOKEN"

// DefaultAPIAddr is where the control API listens unless --api-addr says
// otherwise: port 4242 on all interfaces, so fleet clients on the network can
// reach it (which is why a non-loopback listen demands a token).
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

// Handler builds the control API: status, start, stop, metrics, and deploy
// config, all JSON, all behind the bearer token (when one is set).
func (d *Daemon) Handler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Status())
	})
	mux.HandleFunc("POST /v1/start", func(w http.ResponseWriter, r *http.Request) {
		if err := d.StartEngine(); err != nil {
			status := http.StatusBadRequest
			if state, _, _ := d.Sup.Status(); state == StateRunning {
				status = http.StatusConflict
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, d.Status())
	})
	mux.HandleFunc("POST /v1/stop", func(w http.ResponseWriter, r *http.Request) {
		if err := d.Sup.Stop(); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, d.Status())
	})
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, d.Metrics(r.Context()))
	})
	mux.HandleFunc("PUT /v1/deploy-config", func(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]string{"message": msg})
	})
	return authenticated(token, mux)
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
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
