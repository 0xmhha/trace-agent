package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Process exit codes.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

const (
	// tempDirPattern names the private directory of the temporary database.
	// os.MkdirTemp creates it readable by the owner only.
	tempDirPattern = program + "-*"
	tempDBName     = "usage.sqlite"
	// maxScanPasses bounds the scan loop. One complete pass ends the scanning
	// state, so more than a few passes means the service never reports the
	// end; the command then fails instead of spinning until the timeout.
	maxScanPasses = 20
	// progressInterval is the pause between progress lines during one pass.
	progressInterval = 2 * time.Second
	prettyIndent     = "  "
	// emptyParams is the request body of a method without a request type.
	emptyParams = "{}"
)

// Keys of the printed object.
const (
	keyStatus   = "status"
	keyOverview = "overview"
	keyTools    = "tools"
	keyInsights = "insights"
	keySessions = "sessions"
)

// Failures of run that do not come from a dependency.
var (
	errScanUnfinished  = errors.New("scan still unfinished")
	errInvalidResponse = errors.New("response is not valid JSON")
)

// usageService is the part of the usage service the command drives.
// *service.Service implements it.
type usageService interface {
	SetConsent(ctx context.Context, granted bool) error
	ScanOnce(ctx context.Context) error
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

// openParams tells the constructor what to build the pipeline over.
type openParams struct {
	root      string
	rootLabel string
	dbPath    string
	days      int
	now       func() time.Time
	location  *time.Location
}

// openFunc opens the store at p.dbPath, sets the collection window to p.days
// and returns the service together with the function that releases the store.
type openFunc func(ctx context.Context, p openParams) (usageService, func() error, error)

// deps is everything run takes from outside. ticks returns a channel that
// fires every interval and the function that stops it. tempBase is the parent
// of the temporary database directory; empty selects the system default.
type deps struct {
	open     openFunc
	now      func() time.Time
	location *time.Location
	ticks    func(interval time.Duration) (<-chan time.Time, func())
	tempBase string
	stdout   io.Writer
	stderr   io.Writer
}

// logger writes progress and error lines to standard error.
type logger struct{ w io.Writer }

// printf writes one line. A failing standard error has nowhere to be
// reported, so the write error is dropped.
func (l logger) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(l.w, program+": "+format+"\n", args...)
}

// run scans the root, then prints the report to d.stdout. Standard output
// receives either the complete JSON object or nothing.
func run(ctx context.Context, cfg config, d deps) int {
	log := logger{w: d.stderr}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()
	out, err := collect(ctx, cfg, d, log)
	if err != nil {
		switch ctxErr := ctx.Err(); {
		case errors.Is(ctxErr, context.DeadlineExceeded):
			log.printf("timed out after %s: %v", cfg.timeout, err)
		case ctxErr != nil:
			log.printf("interrupted: %v", err)
		default:
			log.printf("%v", err)
		}
		return exitFailure
	}
	if _, err = d.stdout.Write(out); err != nil {
		log.printf("write report: %v", err)
		return exitFailure
	}
	return exitOK
}

// collect opens the pipeline, scans and returns the encoded report. The
// store is closed and a temporary database removed on every path; a failure
// of either discards the report, because a database that could not be removed
// would stay behind unnoticed.
func collect(ctx context.Context, cfg config, d deps, log logger) (out []byte, err error) {
	dbPath := cfg.dbPath
	if dbPath == "" {
		dir, mkErr := os.MkdirTemp(d.tempBase, tempDirPattern)
		if mkErr != nil {
			return nil, fmt.Errorf("create temporary database directory: %w", mkErr)
		}
		defer func() {
			if rmErr := os.RemoveAll(dir); rmErr != nil {
				out, err = nil, errors.Join(err, fmt.Errorf("remove temporary database: %w", rmErr))
			}
		}()
		dbPath = filepath.Join(dir, tempDBName)
	}
	svc, closeStore, err := d.open(ctx, openParams{
		root:      cfg.root,
		rootLabel: cfg.rootLabel,
		dbPath:    dbPath,
		days:      cfg.days,
		now:       d.now,
		location:  d.location,
	})
	if err != nil {
		return nil, fmt.Errorf("open usage pipeline: %w", err)
	}
	defer func() {
		if closeErr := closeStore(); closeErr != nil {
			out, err = nil, errors.Join(err, fmt.Errorf("close usage database: %w", closeErr))
		}
	}()
	// Consent goes through the service, which records it in this command's
	// own store and schedules the full pass.
	if err = svc.SetConsent(ctx, true); err != nil {
		return nil, fmt.Errorf("record consent: %w", err)
	}
	log.printf("scanning %s, last %d days", cfg.rootLabel, cfg.days)
	if err = scanUntilDone(ctx, svc, d, log); err != nil {
		return nil, err
	}
	return buildReport(ctx, svc, cfg, d)
}

// scanUntilDone repeats scan passes until the service reports that the full
// pass is complete.
func scanUntilDone(ctx context.Context, svc usageService, d deps, log logger) error {
	for pass := 1; pass <= maxScanPasses; pass++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("scan transcripts: %w", err)
		}
		if err := scanPass(ctx, svc, d, log); err != nil {
			return fmt.Errorf("scan transcripts: %w", err)
		}
		status, err := readStatus(ctx, svc)
		if err != nil {
			return err
		}
		log.printf("pass %d: %d of %d files read", pass, status.FilesDone, status.FilesTotal)
		if !status.Scanning {
			return nil
		}
	}
	return fmt.Errorf("%w after %d passes", errScanUnfinished, maxScanPasses)
}

// scanPass runs one ScanOnce and prints progress while it is running. The
// reporter stops when done is closed, and scanPass waits for it, so nothing
// else writes to standard error at the same time.
func scanPass(ctx context.Context, svc usageService, d deps, log logger) error {
	ticks, stop := d.ticks(progressInterval)
	defer stop()
	done := make(chan struct{})
	var reporter sync.WaitGroup
	reporter.Go(func() { reportProgress(ctx, svc, ticks, done, log) })
	err := svc.ScanOnce(ctx)
	close(done)
	reporter.Wait()
	return err
}

// reportProgress prints the file counters on every tick on which they
// moved, until done is closed.
func reportProgress(ctx context.Context, svc usageService, ticks <-chan time.Time, done <-chan struct{}, log logger) {
	lastDone, lastTotal := -1, -1
	for {
		select {
		case <-done:
			return
		case <-ticks:
		}
		// Progress is a courtesy: a status that cannot be read now is read
		// again, and reported, by the scan loop after the pass.
		status, err := readStatus(ctx, svc)
		if err != nil || (status.FilesDone == lastDone && status.FilesTotal == lastTotal) {
			continue
		}
		lastDone, lastTotal = status.FilesDone, status.FilesTotal
		log.printf("%d of %d files read", lastDone, lastTotal)
	}
}

// readStatus asks the service for its watch state.
func readStatus(ctx context.Context, svc usageService) (usage.Status, error) {
	raw, err := svc.Handle(ctx, usage.MethodStatus, json.RawMessage(emptyParams))
	if err != nil {
		return usage.Status{}, fmt.Errorf("request %s: %w", usage.MethodStatus, err)
	}
	var status usage.Status
	if err = json.Unmarshal(raw, &status); err != nil {
		return usage.Status{}, fmt.Errorf("decode %s: %w", usage.MethodStatus, err)
	}
	return status, nil
}

// reportQuery holds the request bodies shared by the report sections.
type reportQuery struct {
	aggregate usage.Query
	sessions  usage.SessionQuery
}

// section is one key of the printed object: the method that produces it and
// its request body. A nil params means the method takes none.
type section struct {
	key    string
	method string
	params func(q reportQuery) any
}

// reportSections lists the printed keys in output order. A new key is added
// here and nowhere else.
func reportSections() []section {
	aggregate := func(q reportQuery) any { return q.aggregate }
	return []section{
		{key: keyStatus, method: usage.MethodStatus},
		{key: keyOverview, method: usage.MethodOverview, params: aggregate},
		{key: keyTools, method: usage.MethodTools, params: aggregate},
		{key: keyInsights, method: usage.MethodInsights, params: aggregate},
		{key: keySessions, method: usage.MethodSessions, params: func(q reportQuery) any { return q.sessions }},
	}
}

// reportKeys returns the printed keys in output order.
func reportKeys() []string {
	sections := reportSections()
	keys := make([]string, 0, len(sections))
	for _, s := range sections {
		keys = append(keys, s.key)
	}
	return keys
}

// reportRange is the last days local calendar days, today included, as whole
// days. Whole days give the series complete buckets. They also keep the range
// inside the collection window: the store measures the window back from the
// instant of each query, so a range of exactly days*24 hours taken a moment
// earlier would start before it and be reported as reaching outside.
func reportRange(now time.Time, loc *time.Location, days int) usage.Range {
	year, month, day := now.In(loc).Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, loc)
	return usage.Range{From: today.AddDate(0, 0, 1-days), Until: today.AddDate(0, 0, 1)}
}

// buildReport requests every section and encodes them as one JSON object
// with the keys in section order.
func buildReport(ctx context.Context, svc usageService, cfg config, d deps) ([]byte, error) {
	span := reportRange(d.now(), d.location, cfg.days)
	query := reportQuery{
		aggregate: usage.Query{Range: span, Project: cfg.project},
		sessions:  usage.SessionQuery{Range: span, Project: cfg.project, Sort: usage.SortLast, Limit: cfg.sessions},
	}
	var object bytes.Buffer
	object.WriteByte('{')
	for i, s := range reportSections() {
		params := json.RawMessage(emptyParams)
		if s.params != nil {
			encoded, err := json.Marshal(s.params(query))
			if err != nil {
				return nil, fmt.Errorf("encode %s request: %w", s.method, err)
			}
			params = encoded
		}
		raw, err := svc.Handle(ctx, s.method, params)
		if err != nil {
			return nil, fmt.Errorf("request %s: %w", s.method, err)
		}
		// Each response must be exactly one JSON value, or it could change
		// the shape of the object it is spliced into.
		if !json.Valid(raw) {
			return nil, fmt.Errorf("request %s: %w", s.method, errInvalidResponse)
		}
		key, err := json.Marshal(s.key)
		if err != nil {
			return nil, fmt.Errorf("encode report: %w", err)
		}
		if i > 0 {
			object.WriteByte(',')
		}
		object.Write(key)
		object.WriteByte(':')
		object.Write(raw)
	}
	object.WriteByte('}')

	var out bytes.Buffer
	var err error
	if cfg.pretty {
		err = json.Indent(&out, object.Bytes(), "", prettyIndent)
	} else {
		err = json.Compact(&out, object.Bytes())
	}
	if err != nil {
		return nil, fmt.Errorf("encode report: %w", err)
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
