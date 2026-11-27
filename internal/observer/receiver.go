// Package observer accepts metadata from a selected Claude hook or participating
// MCP server. Each endpoint is bound to one client source before any requests arrive.
package observer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/0xmhha/trace-agent/internal/collect"
)

// Hook and MCP select the trusted observation contract.
const (
	Hook = collect.ClaudeHook
	MCP  = collect.MCPObserver
)

// endpoint returns the POST path of an observer kind. Each kind has its own
// path, so a credential issued for one contract cannot deliver another.
func endpoint(kind collect.Kind) string {
	switch kind {
	case Hook:
		return "/v1/hooks"
	case MCP:
		return "/v1/mcp"
	}
	return ""
}

// Sink persists observations only for sources that the desktop has registered.
type Sink interface {
	Registered(context.Context, string) (bool, error)
	Append(context.Context, collect.Evidence) error
}

// Source returns the same independently configurable adapter before or after binding.
func Source(parent *collect.Adapter, kind collect.Kind, serverName string) (*collect.Adapter, error) {
	return collect.ObserverAdapter(parent, kind, serverName)
}

// Receiver owns the listener associated with its source-specific bearer credential.
type Receiver struct {
	a          *collect.Adapter
	db         Sink
	digest     [32]byte
	host, path string
	port       int
	http       *http.Server
	workers    chan struct{}
}

// Listen binds one IPv4 loopback port. A zero port selects an available port.
func Listen(db Sink, parent *collect.Adapter, kind collect.Kind, serverName string, port int, bearer string) (*Receiver, error) {
	a, err := Source(parent, kind, serverName)
	if err != nil {
		return nil, err
	}
	path := endpoint(kind)
	if db == nil || path == "" || port < 0 || port > 65535 || len(bearer) < 32 || len(bearer) > 256 || strings.IndexFunc(bearer, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, errors.New("invalid observation endpoint")
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	r := &Receiver{db: db, a: a, host: ln.Addr().String(), port: ln.Addr().(*net.TCPAddr).Port, path: path, digest: sha256.Sum256([]byte("Bearer " + bearer)), workers: make(chan struct{}, 4)}
	r.http = &http.Server{Handler: r, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = r.http.Serve(ln) }()
	return r, nil
}

// Source returns the adapter that must be registered for delivery.
func (r *Receiver) Source() *collect.Adapter { return r.a }

// URL returns the complete local POST endpoint.
func (r *Receiver) URL() string { return "http://" + r.host + r.path }

// Port returns the allocated port.
func (r *Receiver) Port() int { return r.port }

// Close drains accepted requests.
func (r *Receiver) Close(ctx context.Context) error { return r.http.Shutdown(ctx) }

func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.Host != r.host || req.URL.Path != r.path || req.URL.RawQuery != "" || len(req.Header.Values("Origin")) != 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	token := sha256.Sum256([]byte(req.Header.Get("Authorization")))
	if len(req.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(token[:], r.digest[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if content, _, err := mime.ParseMediaType(req.Header.Get("Content-Type")); err != nil || content != "application/json" {
		http.Error(w, "unsupported content", http.StatusUnsupportedMediaType)
		return
	}
	select {
	case r.workers <- struct{}{}:
		defer func() { <-r.workers }()
	default:
		http.Error(w, "busy", http.StatusTooManyRequests)
		return
	}
	registered, err := r.db.Registered(req.Context(), r.a.Source())
	if err != nil || !registered {
		http.Error(w, "source unavailable", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 256<<10))
	if err != nil {
		http.Error(w, "invalid payload", http.StatusRequestEntityTooLarge)
		return
	}
	e, err := r.a.ProjectObservation(body)
	if err != nil {
		http.Error(w, "invalid metadata", http.StatusBadRequest)
		return
	}
	if err = r.db.Append(req.Context(), e); err != nil {
		http.Error(w, "observation not accepted", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, "{}")
}
