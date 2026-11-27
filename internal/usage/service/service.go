package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var errNotImplemented = errors.New("usage/service: not implemented")

// Config wires the service. Root is the absolute path of the transcript root
// and RootLabel its display form without the home directory. Extension may
// be nil; the endpoint is then reported as disabled and setExtensionEnabled
// is refused.
type Config struct {
	Store     Store
	Root      string
	RootLabel string
	Scan      ScanFunc
	Timeline  TimelineFunc
	Evaluate  EvaluateFunc
	Now       func() time.Time
	Location  *time.Location
	Interval  time.Duration
	Extension ExtensionControl
}

// Service scans transcripts in the background and answers UI requests.
type Service struct{ cfg Config }

// New validates cfg. Store, Root, Scan, Timeline, Evaluate and Now are
// required; Location defaults to UTC and Interval to DefaultInterval.
func New(cfg Config) (*Service, error) { return &Service{cfg: cfg}, errNotImplemented }

// DefaultInterval is the pause between scans.
const DefaultInterval = 5 * time.Second

// Run scans until ctx is cancelled and returns ctx.Err(). It does nothing
// while consent is not granted.
func (s *Service) Run(ctx context.Context) error { return errNotImplemented }

// ScanOnce performs a single scan pass over the root: every *.jsonl of a
// project directory and of its */subagents/ directories whose project is not
// excluded and that is inside the collection window or already has a cursor.
// A file that fails is marked unreadable and the pass continues. It returns
// nil without reading when consent is not granted.
func (s *Service) ScanOnce(ctx context.Context) error { return errNotImplemented }

// SetConsent grants or withdraws transcript collection. Only the native side
// calls it, after its own confirmation dialog; it is not a Handle method.
func (s *Service) SetConsent(ctx context.Context, granted bool) error { return errNotImplemented }

// Handle serves one UI request. method is one of the usage.Method constants;
// the request and response types are listed in internal/usage/api.go. The
// response has usage.NonNil applied before encoding. Unknown methods and
// invalid params return usage.ErrBadRequest; an unknown session returns
// usage.ErrNotFound.
//
// Responsibilities that belong to Handle and to no other package:
//   - Coverage of Overview, Tools, Projects, SessionPage and InsightReport is
//     taken from Store.Coverage for the request and then overlaid: disabled
//     without consent, scanning while a full pass is pending.
//   - SessionDetail.Source and TimelinePage.Source: available when the
//     transcript opens, missing when the file does not exist (the page is
//     then empty, not an error), unreadable when it exists but cannot be
//     read.
//   - Timeline pages get the session's extension events inserted as ext
//     items: an event with a tool-use ID directly after the tool_call item
//     with that ID on the same page; an event without one before the first
//     item that is later in time, only inside the page's time span. Events
//     that match no page stay visible through extEvents.
//   - setExtensionEnabled calls Extension.SetEnabled and then stores the
//     outcome with Store.SetExtension.
func (s *Service) Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	return nil, errNotImplemented
}
