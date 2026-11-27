// Package extension accepts events from third-party tools on a loopback HTTP
// endpoint. It validates, masks and caps every field before handing events
// to the usage store, and counts what it refuses.
package extension

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// EventsPath is the only path the endpoint serves.
const EventsPath = "/v1/events"

var errNotImplemented = errors.New("extension: not implemented")

// NewHandler returns the handler of the event endpoint for the listener
// address host ("127.0.0.1:port"). bearer is the credential senders present
// in the Authorization header; now supplies the receive time.
func NewHandler(sink usage.ExtSink, host, bearer string, now func() time.Time) (http.Handler, error) {
	return nil, errNotImplemented
}

// Receiver owns the loopback listener of the event endpoint.
type Receiver struct{}

// Listen binds 127.0.0.1 on port and serves the event endpoint. A zero port
// selects a free one.
func Listen(sink usage.ExtSink, port int, bearer string, now func() time.Time) (*Receiver, error) {
	return nil, errNotImplemented
}

// URL returns the full POST URL of the endpoint.
func (r *Receiver) URL() string { return "" }

// Port returns the bound port.
func (r *Receiver) Port() int { return 0 }

// Close stops accepting requests and drains the ones in flight.
func (r *Receiver) Close(ctx context.Context) error { return errNotImplemented }
