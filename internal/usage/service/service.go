package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Config wires the service. Root is the absolute path of the transcript root
// and RootLabel its display form without the home directory. Extension may
// be nil; the endpoint is then reported as disabled and setExtensionEnabled
// is refused. OnError may be nil; when set, Run hands it the error of a scan
// pass or a prune that failed, because Run itself keeps going.
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
	OnError   func(error)
}

// ErrExtensionUnavailable reports a setExtensionEnabled request to a service
// that was configured without an ExtensionControl.
var ErrExtensionUnavailable = errors.New("usage/service: extension endpoint not available")

// Service scans transcripts in the background and answers UI requests.
type Service struct {
	cfg Config
	// chunkBytes is the size of one Scan and Apply step.
	chunkBytes int64
	methods    map[string]handler

	// scanGate holds one token: a scan pass takes it, so passes never overlap.
	scanGate chan struct{}
	// timelineGate bounds the transcripts read for timeline pages at once.
	timelineGate chan struct{}
	// wake lets a request start the next pass without waiting for the ticker.
	wake chan struct{}
	// windowMu makes the read-compare-write of setWindowDays atomic.
	windowMu sync.Mutex

	mu sync.Mutex
	// epoch advances whenever a full pass becomes necessary. A pass may clear
	// the pending flag or remember a file only when the epoch it started in is
	// still current, so a pass overtaken by a rescan cannot hide the rescan.
	epoch uint64
	// stopEpoch advances whenever consent is withdrawn; a running pass stops
	// at its next chunk boundary when it sees the change.
	stopEpoch  uint64
	pending    bool
	filesTotal int
	filesDone  int
	seen       map[string]fileStamp
	lastPrune  time.Time
}

// New validates cfg. Store, Root, Scan, Timeline, Evaluate and Now are
// required; Location defaults to UTC and Interval to DefaultInterval.
func New(cfg Config) (*Service, error) {
	for _, required := range []struct {
		name    string
		missing bool
	}{
		{"store", cfg.Store == nil},
		{"root", cfg.Root == ""},
		{"scan function", cfg.Scan == nil},
		{"timeline function", cfg.Timeline == nil},
		{"evaluate function", cfg.Evaluate == nil},
		{"clock", cfg.Now == nil},
	} {
		if required.missing {
			return nil, fmt.Errorf("usage/service: %s is required", required.name)
		}
	}
	if !filepath.IsAbs(cfg.Root) {
		return nil, errors.New("usage/service: root must be an absolute path")
	}
	if cfg.Interval < 0 {
		return nil, errors.New("usage/service: interval must not be negative")
	}
	if cfg.Interval == 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	if cfg.RootLabel == "" {
		// The last path element never contains the home directory.
		cfg.RootLabel = filepath.Base(cfg.Root)
	}
	s := &Service{
		cfg:          cfg,
		chunkBytes:   usage.ScanChunkBytes,
		scanGate:     make(chan struct{}, 1),
		timelineGate: make(chan struct{}, usage.MaxTimelineReads),
		wake:         make(chan struct{}, 1),
		// The first pass of a process is always a full one.
		pending: true,
		seen:    make(map[string]fileStamp),
	}
	s.methods = s.methodTable()
	return s, nil
}

// DefaultInterval is the pause between scans.
const DefaultInterval = 5 * time.Second

// pruneInterval is the shortest time between two Store.Prune calls.
const pruneInterval = time.Hour

// Run scans until ctx is cancelled and returns ctx.Err(). It does nothing
// while consent is not granted.
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.cycle(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-s.wake:
		}
	}
}

// cycle is one iteration of Run: a scan pass and, when due, a prune.
func (s *Service) cycle(ctx context.Context) {
	consented, err := s.scan(ctx)
	if err != nil {
		s.report(ctx, err)
	}
	if !consented || ctx.Err() != nil {
		return
	}
	if err = s.pruneIfDue(ctx); err != nil {
		s.report(ctx, err)
	}
}

// report hands a background failure to the owner. A failure caused by
// shutting down is not one.
func (s *Service) report(ctx context.Context, err error) {
	if s.cfg.OnError != nil && ctx.Err() == nil {
		s.cfg.OnError(err)
	}
}

// pruneIfDue applies the retention limits at most once per pruneInterval.
func (s *Service) pruneIfDue(ctx context.Context) error {
	now := s.cfg.Now()
	s.mu.Lock()
	last := s.lastPrune
	s.mu.Unlock()
	// A clock that moved backwards must not postpone pruning indefinitely.
	if !last.IsZero() && !now.Before(last) && now.Sub(last) < pruneInterval {
		return nil
	}
	usageBefore := now.AddDate(0, 0, -usage.MaxWindowDays)
	extBefore := now.AddDate(0, 0, -usage.ExtRetentionDays)
	if err := s.cfg.Store.Prune(ctx, usageBefore, extBefore); err != nil {
		return fmt.Errorf("prune usage data: %w", err)
	}
	s.mu.Lock()
	s.lastPrune = now
	s.mu.Unlock()
	return nil
}

// ScanOnce performs a single scan pass over the root: every *.jsonl of a
// project directory and of its */subagents/ directories whose project is not
// excluded and that is inside the collection window or already has a cursor.
// A file that fails is marked unreadable and the pass continues. It returns
// nil without reading when consent is not granted.
func (s *Service) ScanOnce(ctx context.Context) error {
	_, err := s.scan(ctx)
	return err
}

// scan runs one pass while holding the scan gate. consented reports whether
// collection was allowed when the pass ended.
func (s *Service) scan(ctx context.Context) (consented bool, err error) {
	select {
	case s.scanGate <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-s.scanGate }()
	return s.pass(ctx)
}

// SetConsent grants or withdraws transcript collection. Only the native side
// calls it, after its own confirmation dialog; it is not a Handle method.
func (s *Service) SetConsent(ctx context.Context, granted bool) error {
	if err := s.cfg.Store.SetConsent(ctx, granted); err != nil {
		return fmt.Errorf("store consent: %w", err)
	}
	if !granted {
		s.withdraw()
		return nil
	}
	s.invalidate()
	s.signal()
	return nil
}

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
	h, ok := s.methods[method]
	if !ok {
		return nil, badRequest("unknown method")
	}
	response, err := h(ctx, params)
	if err != nil {
		return nil, err
	}
	usage.NonNil(response)
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode %s response: %w", method, err)
	}
	return encoded, nil
}

// invalidate records that a full pass is needed: everything this process
// remembers about files is dropped, and only a pass that starts afterwards
// can clear the pending flag.
func (s *Service) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	s.pending = true
	clear(s.seen)
}

// withdraw makes a running pass stop at its next chunk boundary.
func (s *Service) withdraw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopEpoch++
}

// signal wakes Run. A wake-up that is already queued covers this one too.
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// progress is the scan state reported by status and used for the coverage
// overlay.
type progress struct {
	pending    bool
	filesTotal int
	filesDone  int
}

func (s *Service) progress() progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return progress{pending: s.pending, filesTotal: s.filesTotal, filesDone: s.filesDone}
}
