// Package service schedules transcript scans and dispatches the requests of
// the usage pages. It owns no storage and no parsing; both are injected.
package service

import (
	"context"
	"io"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Store is the persistence the service needs. internal/usage/db implements it.
type Store interface {
	usage.ExtSink

	Settings(ctx context.Context) (usage.Settings, error)
	SetConsent(ctx context.Context, granted bool) error
	SetWindowDays(ctx context.Context, days int) error
	SetExtension(ctx context.Context, enabled bool, port int) error
	SetExcluded(ctx context.Context, project string, excluded bool) error
	DataVersion(ctx context.Context) (int64, error)

	Cursor(ctx context.Context, path string) (cursor usage.FileCursor, ok bool, err error)
	Apply(ctx context.Context, file usage.FileRef, batch usage.Batch) error
	MarkUnreadable(ctx context.Context, file usage.FileRef) error
	Clear(ctx context.Context) error
	ResetCursors(ctx context.Context) error
	Prune(ctx context.Context, usageBefore, extBefore time.Time) error

	Coverage(ctx context.Context, q usage.Query) (usage.Coverage, error)
	Overview(ctx context.Context, q usage.Query, loc *time.Location) (usage.Overview, error)
	Tools(ctx context.Context, q usage.Query) (usage.Tools, error)
	Projects(ctx context.Context, q usage.Query) (usage.Projects, error)
	Sessions(ctx context.Context, q usage.SessionQuery) (usage.SessionPage, error)
	Session(ctx context.Context, sessionID string) (usage.SessionDetail, error)
	StreamFile(ctx context.Context, sessionID, agentID string) (path string, ok bool, err error)
	ExtSources(ctx context.Context, q usage.Query) (sources []usage.ExtSourceRow, rejected int64, err error)
	ExtEvents(ctx context.Context, q usage.ExtQuery) (usage.ExtPage, error)
}

// ExtensionControl starts and stops the event endpoint. The desktop process
// implements it; the service only records the outcome.
type ExtensionControl interface {
	// SetEnabled starts or stops the endpoint and returns the port it is
	// bound to, or the last bound port when it was stopped.
	SetEnabled(ctx context.Context, enabled bool) (port int, err error)
	// Endpoint reports the live endpoint without its credential.
	Endpoint() usage.ExtEndpoint
}

// ScanFunc parses transcript lines; transcript.Scan implements it.
type ScanFunc func(r io.Reader, in usage.ScanInput) (usage.Batch, error)

// TimelineFunc builds a timeline page; timeline.Read implements it.
type TimelineFunc func(r io.ReadSeeker, req usage.TimelineRequest) (usage.TimelinePage, error)

// EvaluateFunc produces the insight report; insight.Evaluate implements it.
type EvaluateFunc func(in usage.InsightInput, now time.Time) usage.InsightReport
