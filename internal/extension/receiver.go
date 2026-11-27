// Package extension accepts events from third-party tools on a loopback HTTP
// endpoint. It validates, masks and caps every field before handing events
// to the usage store, and counts what it refuses.
package extension

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// EventsPath is the only path the endpoint serves.
const EventsPath = "/v1/events"

// Listener address parts. The endpoint is never bound to another interface.
const (
	loopbackNetwork = "tcp4"
	loopbackIP      = "127.0.0.1"
	urlScheme       = "http://"
	maxPort         = 65535
)

// Server limits. A sender that stalls on headers, on the body or on reading
// the response is cut off instead of holding a connection open.
const (
	readHeaderTimeout = 2 * time.Second
	readTimeout       = 3 * time.Second
	writeTimeout      = 3 * time.Second
	idleTimeout       = 10 * time.Second
	maxHeaderBytes    = 8 << 10
)

// Errors of NewHandler and Listen arguments.
var (
	errNilSink     = errors.New("extension: sink is nil")
	errNilClock    = errors.New("extension: clock is nil")
	errBadPort     = errors.New("extension: port out of range")
	errBadHost     = errors.New("extension: host is not a loopback address with a port")
	errBadBearer   = errors.New("extension: bearer must be 32 to 256 characters without spaces or control characters")
	errNilReceiver = errors.New("extension: receiver is not listening")
)

// NewHandler returns the handler of the event endpoint for the listener
// address host ("127.0.0.1:port"). bearer is the credential senders present
// in the Authorization header; now supplies the receive time.
func NewHandler(sink usage.ExtSink, host, bearer string, now func() time.Time) (http.Handler, error) {
	if err := checkArguments(sink, bearer, now); err != nil {
		return nil, err
	}
	if err := checkHost(host); err != nil {
		return nil, err
	}
	return newHandler(sink, host, bearer, now, rand.Reader), nil
}

// Receiver owns the loopback listener of the event endpoint.
type Receiver struct {
	server *http.Server
	host   string
	port   int
	// done is closed when the serving goroutine has returned; serveErr is
	// written before that and read only after it.
	done     chan struct{}
	serveErr error
}

// Listen binds 127.0.0.1 on port and serves the event endpoint. A zero port
// selects a free one.
func Listen(sink usage.ExtSink, port int, bearer string, now func() time.Time) (*Receiver, error) {
	if err := checkArguments(sink, bearer, now); err != nil {
		return nil, err
	}
	if port < 0 || port > maxPort {
		return nil, errBadPort
	}
	ln, err := net.Listen(loopbackNetwork, net.JoinHostPort(loopbackIP, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("extension: listen: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		closeErr := ln.Close()
		return nil, errors.Join(fmt.Errorf("extension: unexpected listener address %q", ln.Addr()), closeErr)
	}
	host := addr.String()
	r := &Receiver{
		host: host,
		port: addr.Port,
		done: make(chan struct{}),
		server: &http.Server{
			Handler:           newHandler(sink, host, bearer, now, rand.Reader),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
		},
	}
	// The goroutine ends when Close shuts the server down or the listener
	// fails; Close waits for it.
	go func() {
		defer close(r.done)
		if err := r.server.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			r.serveErr = err
		}
	}()
	return r, nil
}

// URL returns the full POST URL of the endpoint.
func (r *Receiver) URL() string {
	if r == nil || r.server == nil {
		return ""
	}
	return urlScheme + r.host + EventsPath
}

// Port returns the bound port.
func (r *Receiver) Port() int {
	if r == nil {
		return 0
	}
	return r.port
}

// Close stops accepting requests and drains the ones in flight. When ctx
// ends before the drain does, the remaining connections are cut and the
// context error is returned. Close may be called more than once.
func (r *Receiver) Close(ctx context.Context) error {
	if r == nil || r.server == nil {
		return errNilReceiver
	}
	shutdownErr := r.server.Shutdown(ctx)
	if shutdownErr != nil {
		// Shutdown gave up waiting; cutting the connections is what lets the
		// serving goroutine and the handlers end.
		shutdownErr = errors.Join(fmt.Errorf("extension: shutdown: %w", shutdownErr), r.server.Close())
	}
	<-r.done
	if r.serveErr != nil {
		return errors.Join(shutdownErr, fmt.Errorf("extension: serve: %w", r.serveErr))
	}
	return shutdownErr
}

// checkArguments validates what NewHandler and Listen share.
func checkArguments(sink usage.ExtSink, bearer string, now func() time.Time) error {
	if sink == nil {
		return errNilSink
	}
	if now == nil {
		return errNilClock
	}
	if !validBearer(bearer) {
		return errBadBearer
	}
	return nil
}

// checkHost accepts only a loopback IP literal with a port, because the Host
// header comparison is the defence against DNS rebinding and must never be
// satisfied by a public name.
func checkHost(host string) error {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return errBadHost
	}
	ip := net.ParseIP(name)
	if ip == nil || !ip.IsLoopback() {
		return errBadHost
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > maxPort {
		return errBadHost
	}
	return nil
}
