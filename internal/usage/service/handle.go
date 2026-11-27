package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Request limits that are not part of the shared contract.
const (
	// maxRangeSpan is the widest range of an aggregate request: the widest
	// window plus one day, so a window that ends at the end of today fits.
	maxRangeSpan = (usage.MaxWindowDays + 1) * 24 * time.Hour
	// maxBinHours keeps the bucket width inside the widest range.
	maxBinHours = (usage.MaxWindowDays + 1) * 24
	// maxNameLen bounds every identifier and search string of a request.
	maxNameLen = 1024
	// maxCursorLen bounds an opaque page cursor. A timeline cursor carries
	// the tool calls that are still open, so it is larger than a name.
	maxCursorLen = 1 << 20
	// statusRangeSlack moves the start of the status coverage range inside
	// the collection window. The store computes the window from its own,
	// later reading of the clock; without the slack that reading would
	// classify the whole window as starting before the window.
	statusRangeSlack = 2 * time.Hour
)

// handler serves one method. It returns a pointer to the response value.
type handler func(ctx context.Context, params json.RawMessage) (any, error)

// methodTable is the only place that binds a method name to its handler.
func (s *Service) methodTable() map[string]handler {
	return map[string]handler{
		usage.MethodStatus:       s.handleStatus,
		usage.MethodOverview:     s.handleOverview,
		usage.MethodSessions:     s.handleSessions,
		usage.MethodSession:      s.handleSession,
		usage.MethodTimeline:     s.handleTimeline,
		usage.MethodTools:        s.handleTools,
		usage.MethodProjects:     s.handleProjects,
		usage.MethodInsights:     s.handleInsights,
		usage.MethodExtensions:   s.handleExtensions,
		usage.MethodExtEvents:    s.handleExtEvents,
		usage.MethodSetExcluded:  s.handleSetExcluded,
		usage.MethodSetWindow:    s.handleSetWindow,
		usage.MethodRescan:       s.handleRescan,
		usage.MethodClearData:    s.handleClearData,
		usage.MethodSetExtension: s.handleSetExtension,
	}
}

func badRequest(format string, args ...any) error {
	return fmt.Errorf("%w: %s", usage.ErrBadRequest, fmt.Sprintf(format, args...))
}

// decode reads params into T. An empty body and null mean the zero request;
// anything else must be a single JSON value of the right shape.
func decode[T any](params json.RawMessage) (T, error) {
	var request T
	body := bytes.TrimSpace(params)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return request, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&request); err != nil {
		return request, badRequest("decode params: %v", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return request, badRequest("params hold more than one value")
	}
	return request, nil
}

// noParams accepts the body of a method without a request type.
func noParams(params json.RawMessage) error {
	_, err := decode[struct{}](params)
	return err
}

func checkName(field, value string, required bool) error {
	if required && value == "" {
		return badRequest("%s is required", field)
	}
	if len(value) > maxNameLen {
		return badRequest("%s is too long", field)
	}
	return nil
}

func checkCursor(cursor string) error {
	if len(cursor) > maxCursorLen {
		return badRequest("cursor is too long")
	}
	return nil
}

// pageLimit applies the default and the cap of a list request.
func pageLimit(limit int) (int, error) {
	switch {
	case limit < 0:
		return 0, badRequest("limit must not be negative")
	case limit == 0:
		return usage.DefaultPageSize, nil
	}
	return min(limit, usage.MaxPageSize), nil
}

func checkRange(r usage.Range) error {
	if !r.From.Before(r.Until) {
		return badRequest("range must start before it ends")
	}
	if r.Until.Sub(r.From) > maxRangeSpan {
		return badRequest("range is wider than %d days", usage.MaxWindowDays+1)
	}
	return nil
}

func decodeQuery(params json.RawMessage) (usage.Query, error) {
	q, err := decode[usage.Query](params)
	if err != nil {
		return q, err
	}
	if q.BinHours < 0 || q.BinHours > maxBinHours {
		return q, badRequest("binHours is out of range")
	}
	return q, errors.Join(checkRange(q.Range), checkName("project", q.Project, false))
}

func decodeSessionQuery(params json.RawMessage) (usage.SessionQuery, error) {
	q, err := decode[usage.SessionQuery](params)
	if err != nil {
		return q, err
	}
	switch q.Sort {
	case "":
		q.Sort = usage.SortLast
	case usage.SortLast, usage.SortTokens, usage.SortErrors, usage.SortCost:
	default:
		return q, badRequest("unknown sort order")
	}
	if q.Limit, err = pageLimit(q.Limit); err != nil {
		return q, err
	}
	return q, errors.Join(
		checkRange(q.Range),
		checkName("project", q.Project, false),
		checkName("search", q.Search, false),
		checkCursor(q.Cursor),
	)
}

func decodeExtQuery(params json.RawMessage) (usage.ExtQuery, error) {
	q, err := decode[usage.ExtQuery](params)
	if err != nil {
		return q, err
	}
	// A zero bound is open on that side, so only a range with both bounds
	// can be inverted.
	if r := q.Range; !r.From.IsZero() && !r.Until.IsZero() && !r.From.Before(r.Until) {
		return q, badRequest("range must start before it ends")
	}
	switch q.Level {
	case "", usage.ExtDebug, usage.ExtInfo, usage.ExtWarn, usage.ExtFail:
	default:
		return q, badRequest("unknown level")
	}
	if q.Limit, err = pageLimit(q.Limit); err != nil {
		return q, err
	}
	return q, errors.Join(
		checkName("source", q.Source, false),
		checkName("sessionId", q.SessionID, false),
		checkName("toolUseId", q.ToolUseID, false),
		checkCursor(q.Cursor),
	)
}

// overlay puts the service's knowledge on top of the store's coverage: the
// store cannot know that collection is off or that a full pass is pending.
func overlay(coverage usage.Coverage, consent bool, scan progress) usage.Coverage {
	switch {
	case !consent:
		coverage.State = usage.CoverageDisabled
	case scan.pending:
		coverage.State = usage.CoverageScanning
	}
	return coverage
}

// coverage reports how completely the stored data covers q. Callers read it
// before the data it describes: a pass that finishes in between then leaves
// the answer too cautious, never too confident.
func (s *Service) coverage(ctx context.Context, q usage.Query) (usage.Coverage, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return usage.Coverage{}, err
	}
	return s.coverageWith(ctx, q, settings.Consent, s.progress())
}

func (s *Service) coverageWith(ctx context.Context, q usage.Query, consent bool, scan progress) (usage.Coverage, error) {
	coverage, err := s.cfg.Store.Coverage(ctx, q)
	if err != nil {
		return usage.Coverage{}, fmt.Errorf("read coverage: %w", err)
	}
	return overlay(coverage, consent, scan), nil
}

func (s *Service) settings(ctx context.Context) (usage.Settings, error) {
	settings, err := s.cfg.Store.Settings(ctx)
	if err != nil {
		return usage.Settings{}, fmt.Errorf("read usage settings: %w", err)
	}
	return settings, nil
}

// requireConsent guards the methods that touch the original transcripts.
func (s *Service) requireConsent(ctx context.Context) error {
	settings, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Consent {
		return usage.ErrNoConsent
	}
	return nil
}

func (s *Service) handleStatus(ctx context.Context, params json.RawMessage) (any, error) {
	if err := noParams(params); err != nil {
		return nil, err
	}
	return s.status(ctx)
}

// status builds the watch state. Its coverage describes the whole collection
// window rather than the range of a page.
func (s *Service) status(ctx context.Context) (*usage.Status, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return nil, err
	}
	scan := s.progress()
	now := s.cfg.Now()
	window := usage.Query{Range: usage.Range{
		From:  windowStart(now, settings.WindowDays).Add(statusRangeSlack),
		Until: now,
	}}
	coverage, err := s.coverageWith(ctx, window, settings.Consent, scan)
	if err != nil {
		return nil, err
	}
	version, err := s.cfg.Store.DataVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("read data version: %w", err)
	}
	return &usage.Status{
		Consent:    settings.Consent,
		Root:       s.cfg.RootLabel,
		WindowDays: settings.WindowDays,
		// Nothing is being read while collection is off, whatever is pending.
		Scanning:    settings.Consent && scan.pending,
		FilesTotal:  scan.filesTotal,
		FilesDone:   scan.filesDone,
		Coverage:    coverage,
		Excluded:    slices.Sorted(slices.Values(settings.Excluded)),
		Extension:   s.endpoint(),
		DataVersion: version,
	}, nil
}

func (s *Service) endpoint() usage.ExtEndpoint {
	if s.cfg.Extension == nil {
		return usage.ExtEndpoint{}
	}
	return s.cfg.Extension.Endpoint()
}

func (s *Service) handleOverview(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeQuery(params)
	if err != nil {
		return nil, err
	}
	overview, err := s.overview(ctx, q)
	if err != nil {
		return nil, err
	}
	return &overview, nil
}

// overview returns the dashboard numbers with their coverage filled in.
func (s *Service) overview(ctx context.Context, q usage.Query) (usage.Overview, error) {
	coverage, err := s.coverage(ctx, q)
	if err != nil {
		return usage.Overview{}, err
	}
	overview, err := s.cfg.Store.Overview(ctx, q, s.cfg.Location)
	if err != nil {
		return usage.Overview{}, fmt.Errorf("read overview: %w", err)
	}
	overview.Coverage = coverage
	return overview, nil
}

func (s *Service) handleTools(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeQuery(params)
	if err != nil {
		return nil, err
	}
	coverage, err := s.coverage(ctx, q)
	if err != nil {
		return nil, err
	}
	tools, err := s.cfg.Store.Tools(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read tools: %w", err)
	}
	tools.Coverage = coverage
	return &tools, nil
}

func (s *Service) handleProjects(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeQuery(params)
	if err != nil {
		return nil, err
	}
	coverage, err := s.coverage(ctx, q)
	if err != nil {
		return nil, err
	}
	projects, err := s.cfg.Store.Projects(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}
	projects.Coverage = coverage
	return &projects, nil
}

func (s *Service) handleSessions(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeSessionQuery(params)
	if err != nil {
		return nil, err
	}
	coverage, err := s.coverage(ctx, usage.Query{Range: q.Range, Project: q.Project})
	if err != nil {
		return nil, err
	}
	page, err := s.cfg.Store.Sessions(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	page.Coverage = coverage
	return &page, nil
}

func (s *Service) handleSession(ctx context.Context, params json.RawMessage) (any, error) {
	ref, err := decode[usage.SessionRef](params)
	if err != nil {
		return nil, err
	}
	if err = checkName("sessionId", ref.SessionID, true); err != nil {
		return nil, err
	}
	if err = s.requireConsent(ctx); err != nil {
		return nil, err
	}
	detail, err := s.cfg.Store.Session(ctx, ref.SessionID)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	rel, stored, err := s.cfg.Store.StreamFile(ctx, ref.SessionID, "")
	if err != nil {
		return nil, fmt.Errorf("locate transcript: %w", err)
	}
	// A session known only through its sub-agents has no root transcript.
	detail.Source = usage.SourceMissing
	if stored {
		detail.Source = s.probeTranscript(rel)
	}
	return &detail, nil
}

func (s *Service) handleInsights(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeQuery(params)
	if err != nil {
		return nil, err
	}
	overview, err := s.overview(ctx, q)
	if err != nil {
		return nil, err
	}
	tools, err := s.cfg.Store.Tools(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read tools: %w", err)
	}
	tools.Coverage = overview.Coverage
	heaviest, err := s.cfg.Store.Sessions(ctx, usage.SessionQuery{
		Range:   q.Range,
		Project: q.Project,
		Sort:    usage.SortTokens,
		Limit:   usage.MaxInsightSessions,
	})
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	sources, rejected, err := s.cfg.Store.ExtSources(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read extension sources: %w", err)
	}
	report := s.cfg.Evaluate(usage.InsightInput{
		Locale:      q.Locale,
		Range:       q.Range,
		Overview:    overview,
		Tools:       tools,
		Sessions:    heaviest.Items,
		Ext:         sources,
		ExtRejected: rejected,
	}, s.cfg.Now())
	report.Coverage = overview.Coverage
	return &report, nil
}

func (s *Service) handleExtensions(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeQuery(params)
	if err != nil {
		return nil, err
	}
	sources, rejected, err := s.cfg.Store.ExtSources(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read extension sources: %w", err)
	}
	return &usage.ExtOverview{Endpoint: s.endpoint(), Sources: sources, Rejected: rejected}, nil
}

func (s *Service) handleExtEvents(ctx context.Context, params json.RawMessage) (any, error) {
	q, err := decodeExtQuery(params)
	if err != nil {
		return nil, err
	}
	page, err := s.cfg.Store.ExtEvents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read extension events: %w", err)
	}
	return &page, nil
}

// setExcludedRequest and setEnabledRequest mirror usage.SetExcluded and
// usage.SetEnabled with the flag as a pointer: a body that leaves the flag
// out must not be read as "include again" or "switch off".
type setExcludedRequest struct {
	Project  string `json:"project"`
	Excluded *bool  `json:"excluded"`
}

type setEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Service) handleSetExcluded(ctx context.Context, params json.RawMessage) (any, error) {
	request, err := decode[setExcludedRequest](params)
	if err != nil {
		return nil, err
	}
	if err = checkName("project", request.Project, true); err != nil {
		return nil, err
	}
	if request.Excluded == nil {
		return nil, badRequest("excluded is required")
	}
	if err = s.cfg.Store.SetExcluded(ctx, request.Project, *request.Excluded); err != nil {
		return nil, fmt.Errorf("store excluded project: %w", err)
	}
	if !*request.Excluded {
		// Excluding deleted the project's cursors; its files are read again
		// under the window rule, and nothing remembered about them holds.
		s.invalidate()
		s.signal()
	}
	return s.status(ctx)
}

func (s *Service) handleSetWindow(ctx context.Context, params json.RawMessage) (any, error) {
	request, err := decode[usage.SetWindow](params)
	if err != nil {
		return nil, err
	}
	if request.Days < 1 || request.Days > usage.MaxWindowDays {
		return nil, badRequest("days must be between 1 and %d", usage.MaxWindowDays)
	}
	if err = s.setWindowDays(ctx, request.Days); err != nil {
		return nil, err
	}
	return s.status(ctx)
}

// setWindowDays stores the window and asks for a full pass when it grew:
// files skipped as too old may now be inside it.
func (s *Service) setWindowDays(ctx context.Context, days int) error {
	s.windowMu.Lock()
	defer s.windowMu.Unlock()
	before, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if err = s.cfg.Store.SetWindowDays(ctx, days); err != nil {
		return fmt.Errorf("store window days: %w", err)
	}
	if days > before.WindowDays {
		s.invalidate()
		s.signal()
	}
	return nil
}

func (s *Service) handleRescan(ctx context.Context, params json.RawMessage) (any, error) {
	if err := noParams(params); err != nil {
		return nil, err
	}
	if err := s.cfg.Store.ResetCursors(ctx); err != nil {
		return nil, fmt.Errorf("reset file cursors: %w", err)
	}
	s.invalidate()
	s.signal()
	return s.status(ctx)
}

func (s *Service) handleClearData(ctx context.Context, params json.RawMessage) (any, error) {
	if err := noParams(params); err != nil {
		return nil, err
	}
	// Consent goes first: the store refuses every batch from then on, so a
	// running pass cannot bring back rows that Clear removes.
	if err := s.cfg.Store.SetConsent(ctx, false); err != nil {
		return nil, fmt.Errorf("withdraw consent: %w", err)
	}
	s.withdraw()
	// The endpoint goes off before the rows go: an event that arrived
	// between Clear and the switch would outlive the clearing. A failure to
	// switch it off does not keep the user's data from being deleted.
	var switchErr error
	if s.cfg.Extension != nil {
		switchErr = s.disableExtension(ctx)
	}
	if err := s.cfg.Store.Clear(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("clear usage data: %w", err), switchErr)
	}
	s.invalidate()
	if switchErr != nil {
		return nil, switchErr
	}
	return s.status(ctx)
}

func (s *Service) handleSetExtension(ctx context.Context, params json.RawMessage) (any, error) {
	request, err := decode[setEnabledRequest](params)
	if err != nil {
		return nil, err
	}
	if request.Enabled == nil {
		return nil, badRequest("enabled is required")
	}
	if s.cfg.Extension == nil {
		return nil, ErrExtensionUnavailable
	}
	if *request.Enabled {
		err = s.enableExtension(ctx)
	} else {
		err = s.disableExtension(ctx)
	}
	if err != nil {
		return nil, err
	}
	return s.status(ctx)
}

// enableExtension starts the endpoint and then records the port it bound.
// When the record fails the endpoint is stopped again, so that the stored
// setting and the live endpoint agree: the user would otherwise see it on
// now and find it off after the next launch.
func (s *Service) enableExtension(ctx context.Context) error {
	port, err := s.cfg.Extension.SetEnabled(ctx, true)
	if err != nil {
		return fmt.Errorf("switch extension endpoint on: %w", err)
	}
	if err = s.cfg.Store.SetExtension(ctx, true, port); err != nil {
		storeErr := fmt.Errorf("store extension setting: %w", err)
		if _, stopErr := s.cfg.Extension.SetEnabled(ctx, false); stopErr != nil {
			return errors.Join(storeErr, fmt.Errorf("switch extension endpoint back off: %w", stopErr))
		}
		return storeErr
	}
	return nil
}

// disableExtension records the endpoint as off and then stops it. It fails
// closed: the off state is stored first so that a later launch does not
// bring the endpoint back, and the endpoint is stopped even when storing
// fails, because the user asked for it to be off.
func (s *Service) disableExtension(ctx context.Context) error {
	storeErr := s.storeExtensionOff(ctx)
	if _, err := s.cfg.Extension.SetEnabled(ctx, false); err != nil {
		return errors.Join(storeErr, fmt.Errorf("switch extension endpoint off: %w", err))
	}
	return storeErr
}

// storeExtensionOff keeps the last bound port, which the endpoint reuses
// when it is switched on again.
func (s *Service) storeExtensionOff(ctx context.Context) error {
	settings, err := s.settings(ctx)
	if err != nil {
		return err
	}
	if err = s.cfg.Store.SetExtension(ctx, false, settings.ExtPort); err != nil {
		return fmt.Errorf("store extension setting: %w", err)
	}
	return nil
}
