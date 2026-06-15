// Package node implements the per-host daemon: it buffers events posted by the
// local hook and serves a pull API to the collector. It binds loopback by
// default so a data-center host is reachable only through an `ssh -L` tunnel,
// giving DC and LAN the same security posture.
package node

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/quink/tiger-eye/internal/config"
	"github.com/quink/tiger-eye/internal/event"
)

// maxWait caps a single long-poll. Kept under typical proxy/idle timeouts so a
// pull over `ssh -L` does not get torn down mid-wait.
const maxWait = 25 * time.Second

// Server is the per-host node daemon. It buffers events posted by the local
// hook and serves a pull API to the collector. Create one with New, then call
// ListenAndServe to start it.
type Server struct {
	buf     *buffer
	token   string
	machine string
	addr    string
	port    int
	srv     *http.Server
}

// New creates a Server that will listen on loopback at the given port. The
// token and machine fields use the same conventions as the node subcommand
// flags; empty machine falls back to os.Hostname.
func New(port int, token, machine string) *Server {
	if machine == "" {
		machine, _ = os.Hostname()
	}
	return &Server{
		buf:     newBuffer(2048),
		token:   token,
		machine: machine,
		addr:    "127.0.0.1",
		port:    port,
	}
}

// Port returns the configured listen port.
func (s *Server) Port() int { return s.port }

// Machine returns the machine name stamped on events.
func (s *Server) Machine() string { return s.machine }

// ListenAndServe starts the HTTP server. It blocks until the server exits.
func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/ingest", s.auth(s.handleIngest))
	mux.HandleFunc("/events", s.auth(s.handleEvents))
	mux.HandleFunc("/sessions", s.auth(s.handleSessions))

	listen := fmt.Sprintf("%s:%d", s.addr, s.port)
	fmt.Fprintf(os.Stderr, "tiger-eye node listening on %s (machine=%s)\n", listen, s.machine)
	s.srv = &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server. Call after ListenAndServe returns
// or from a separate goroutine.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv != nil {
		return s.srv.Shutdown(ctx)
	}
	return nil
}

// Run parses flags and starts the node daemon. Flags:
//
//	-addr     listen address (default 127.0.0.1)
//	-port     listen port
//	-token    bearer token; falls back to $TIGER_EYE_TOKEN
//	-machine  machine name stamped on events; falls back to os.Hostname
func Run(args []string) error {
	fs := flag.NewFlagSet("node", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1", "listen address (loopback by default; use ssh -L to reach remotely)")
	port := fs.Int("port", config.DefaultPort, "listen port")
	token := fs.String("token", os.Getenv("TIGER_EYE_TOKEN"), "bearer token (or set TIGER_EYE_TOKEN)")
	machine := fs.String("machine", "", "machine name stamped on events (default: hostname)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s := New(*port, *token, *machine)
	s.addr = *addr
	return s.ListenAndServe()
}

// auth wraps a handler with constant-time bearer-token verification. An empty
// configured token disables auth (loopback-only dev convenience).
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := r.Header.Get("Authorization")
			want := "Bearer " + s.token
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"machine":%q}`, s.machine)
}

func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var e event.Event
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if e.Machine == "" {
		e.Machine = s.machine
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	seq := s.buf.append(e)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"seq":%d}`, seq)
}

type eventsResponse struct {
	Events  []event.Event `json:"events"`
	LastSeq uint64        `json:"last_seq"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	wait := parseWait(r.URL.Query().Get("wait"))

	evs, last, notify := s.buf.since(since)
	if len(evs) == 0 && wait > 0 {
		// Long-poll: block until a new event arrives, the wait elapses, or the
		// client disconnects.
		select {
		case <-notify:
			evs, last, _ = s.buf.since(since)
		case <-time.After(wait):
		case <-r.Context().Done():
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(eventsResponse{Events: evs, LastSeq: last})
}

func (s *Server) handleSessions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(readSessions())
}

func parseWait(s string) time.Duration {
	ms, err := strconv.Atoi(s)
	if err != nil || ms <= 0 {
		return 0
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxWait {
		return maxWait
	}
	return d
}
