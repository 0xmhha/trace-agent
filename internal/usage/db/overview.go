package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Series bucket rules.
const (
	// aggMaxBins is the most buckets one series holds; a wider range gets
	// wider buckets.
	aggMaxBins = 400
	// aggHourBin and aggDayBin are the widths chosen when the query leaves
	// the width open.
	aggHourBin = 1
	aggDayBin  = 24
	// aggHourlyRangeMax is the longest range that still gets hourly buckets.
	aggHourlyRangeMax = 2 * aggDayBin * time.Hour
	// aggMaxBinHours is the widest bucket a query may ask for: one bucket for
	// the longest history window.
	aggMaxBinHours = aggDayBin * usage.MaxWindowDays
	// aggWidestBinHours is the widest bucket whose length still fits a
	// time.Duration.
	aggWidestBinHours = int(math.MaxInt64 / int64(time.Hour))
)

// Sizes of the fixed overview lists.
const (
	aggTopProjects = 10
	aggTopTools    = 10
	aggWeekdays    = 7
	aggDayHours    = 24
)

// aggActiveSlotMS is the slot length used to find active days. It only has to
// be shorter than a local day; see aggActiveDays.
const aggActiveSlotMS = int64(time.Hour / time.Millisecond)

// Named parameters of the overview queries.
const (
	aggArgBins = "bins"
	aggArgSlot = "slot"
)

// aggBinsCTE turns the JSON array of [lo, hi) millisecond pairs bound to
// :bins into a relation with the bucket index i.
const aggBinsCTE = `WITH bins(i, lo, hi) AS (
	SELECT key, CAST(json_extract(value, '$[0]') AS INTEGER), CAST(json_extract(value, '$[1]') AS INTEGER)
	FROM json_each(:` + aggArgBins + `)
) `

// aggInBin is the join condition that puts the rows of the table aliased as
// alias into bucket b. The buckets are already clipped to the query range.
func aggInBin(alias string) string {
	return alias + ".at_ms >= b.lo AND " + alias + ".at_ms < b.hi AND " + aggInProject(alias)
}

// Overview aggregates the dashboard numbers of the query range. loc decides
// day boundaries, active days and the weekday-hour heatmap.
// The Coverage field is left zero for the service to fill.
//
// A nil loc means UTC. The series holds every bucket of the range, also the
// empty ones. Buckets are q.BinHours wide; widths that are a multiple of 24
// follow local calendar days and other widths step from the start of the
// local day of q.Range.From, so the first bucket may start before the range.
// It still counts only rows of the range. A width that would need more than
// 400 buckets is doubled until the series fits. Bin.From and every other time
// is reported in UTC.
//
// A range that does not start before it ends, a negative q.BinHours or one
// above a year, and a range too wide for 400 buckets are usage.ErrBadRequest.
func (s *Store) Overview(ctx context.Context, q usage.Query, loc *time.Location) (usage.Overview, error) {
	sc, err := aggNewScope(q)
	if err != nil {
		return usage.Overview{}, err
	}
	if loc == nil {
		loc = time.UTC
	}
	starts, err := aggSeriesStarts(q.Range, q.BinHours, loc)
	if err != nil {
		return usage.Overview{}, err
	}
	out := usage.Overview{Range: q.Range}
	steps := []func() error{
		func() error { return s.aggTotals(ctx, sc, &out) },
		func() error { return s.aggCost(ctx, sc, &out) },
		func() error { return s.aggSeries(ctx, sc, starts, &out) },
		func() error { return s.aggActiveDays(ctx, sc, loc, &out) },
		func() error { return s.aggHeatmap(ctx, sc, loc, &out) },
		func() error { return s.aggModels(ctx, sc, &out) },
		func() error { return s.aggTopLists(ctx, sc, &out) },
	}
	for _, step := range steps {
		if err = step(); err != nil {
			return usage.Overview{}, fmt.Errorf("query usage overview: %w", err)
		}
	}
	return out, nil
}

// aggBinWidth is the bucket width in hours: the requested one, or hourly for
// ranges up to two days and daily beyond.
func aggBinWidth(r usage.Range, requested int) (int, error) {
	switch {
	case requested < 0 || requested > aggMaxBinHours:
		return 0, fmt.Errorf("bin width of %d hours: %w", requested, usage.ErrBadRequest)
	case requested > 0:
		return requested, nil
	case r.Until.Sub(r.From) <= aggHourlyRangeMax:
		return aggHourBin, nil
	default:
		return aggDayBin, nil
	}
}

// aggSeriesStarts returns the start of every series bucket of r, doubling the
// width until at most aggMaxBins buckets cover the range.
func aggSeriesStarts(r usage.Range, requested int, loc *time.Location) ([]time.Time, error) {
	width, err := aggBinWidth(r, requested)
	if err != nil {
		return nil, err
	}
	for {
		if starts, ok := aggBinStarts(r, width, loc, aggMaxBins); ok {
			return starts, nil
		}
		if width > aggWidestBinHours/2 {
			return nil, fmt.Errorf("range too wide for a series: %w", usage.ErrBadRequest)
		}
		width *= 2
	}
}

// aggDayStart is the first instant of a local calendar day in loc. Days past
// the end of the month roll over as in time.Date.
func aggDayStart(year int, month time.Month, day int, loc *time.Location) time.Time {
	start := time.Date(year, month, day, 0, 0, 0, 0, loc)
	// A zone that moves its clocks forward at midnight has no 00:00 on that
	// day, and time.Date then answers with an instant of the evening before.
	// The day begins at the transition instead.
	noon := time.Date(year, month, day, 12, 0, 0, 0, loc)
	if start.Day() != noon.Day() {
		if _, end := start.ZoneBounds(); !end.IsZero() && !end.After(noon) {
			start = end
		}
	}
	return start
}

// aggBinStarts returns the starts of the buckets of width hours that cover r,
// beginning with the bucket that contains r.From. ok is false when more than
// limit buckets would be needed. width must be positive and at most
// aggWidestBinHours.
//
// A width that is a multiple of 24 steps over local calendar days, so a
// bucket is 23 or 25 hours long across a clock change and always starts with
// its local day. Any other width steps in elapsed time from the start of the
// local day of r.From.
func aggBinStarts(r usage.Range, width int, loc *time.Location, limit int) (starts []time.Time, ok bool) {
	year, month, day := r.From.In(loc).Date()
	// next returns the start of bucket k, given the start of bucket k-1.
	var start time.Time
	var next func(prev time.Time, k int) time.Time
	if width%aggDayBin == 0 {
		days := width / aggDayBin
		// Every start is derived from the calendar, not from the previous
		// start, so an irregular day cannot shift the later ones.
		next = func(_ time.Time, k int) time.Time { return aggDayStart(year, month, day+k*days, loc) }
		start = next(time.Time{}, 0)
	} else {
		step := time.Duration(width) * time.Hour
		// Adding one step at a time keeps every operand within a Duration,
		// which a multiple of the widest step would not be.
		next = func(prev time.Time, _ int) time.Time { return prev.Add(step) }
		anchor := aggDayStart(year, month, day, loc)
		start = anchor.Add(r.From.Sub(anchor) / step * step)
	}
	for k := 1; start.Before(r.Until); k++ {
		// A zone that skips a whole calendar day yields the same start twice.
		if len(starts) == 0 || start.After(starts[len(starts)-1]) {
			if len(starts) == limit {
				return nil, false
			}
			starts = append(starts, start)
		}
		start = next(start, k)
	}
	return starts, true
}

// aggTotals fills the scalar counters, the token sums and the cache ratio.
func (s *Store) aggTotals(ctx context.Context, sc aggScope, out *usage.Overview) error {
	counts := `SELECT
		(SELECT COUNT(*) FROM (` + aggActiveSessions() + `)),
		(SELECT COUNT(*) FROM prompt p WHERE ` + aggInRange("p") + `),
		(SELECT COUNT(*) FROM (SELECT DISTINCT m.session_id, m.agent_id FROM model_call m WHERE ` + aggInRange("m") + ` AND m.agent_id <> '')),
		(SELECT COUNT(*) FROM compaction k WHERE ` + aggInRange("k") + `)`
	if err := s.db.QueryRowContext(ctx, counts, sc.args()...).Scan(&out.Sessions, &out.Prompts, &out.Subagents, &out.Compactions); err != nil {
		return err
	}
	calls := `SELECT COUNT(*), COALESCE(SUM(m.input), 0), COALESCE(SUM(m.output), 0), COALESCE(SUM(m.cache_read), 0), COALESCE(SUM(m.cache_create), 0)
		FROM model_call m WHERE ` + aggInRange("m")
	tokens := &out.Tokens
	if err := s.db.QueryRowContext(ctx, calls, sc.args()...).Scan(&out.ModelCalls, &tokens.Input, &tokens.Output, &tokens.CacheRead, &tokens.CacheCreate); err != nil {
		return err
	}
	if prompted := tokens.Input + tokens.CacheRead + tokens.CacheCreate; prompted > 0 {
		ratio := float64(tokens.CacheRead) / float64(prompted)
		out.CacheHitRatio = &ratio
	}
	tools := `SELECT COUNT(*), COALESCE(SUM(r.is_error), 0)
		FROM tool_call c LEFT JOIN tool_result r ON ` + aggResultJoin + ` WHERE ` + aggInRange("c")
	return s.db.QueryRowContext(ctx, tools, sc.args()...).Scan(&out.ToolCalls, &out.ToolErrors)
}

// aggCost sums the client-reported cost of the sessions in range. The cost
// stays nil when none of them reported one: unknown is not zero.
func (s *Store) aggCost(ctx context.Context, sc aggScope, out *usage.Overview) error {
	query := `SELECT COUNT(*), SUM(t.total_cost_usd) FROM cost_state t WHERE t.session_id IN (` + aggActiveSessions() + `)`
	var total sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, query, sc.args()...).Scan(&out.CostSessions, &total); err != nil {
		return err
	}
	if out.CostSessions > 0 && total.Valid {
		out.CostUSD = &total.Float64
	}
	return nil
}

// aggSeries fills one zero-filled bucket per start. Each bucket is clipped to
// the query range before it is handed to SQLite, so the series adds up to the
// totals even when the first bucket starts before the range.
func (s *Store) aggSeries(ctx context.Context, sc aggScope, starts []time.Time, out *usage.Overview) error {
	bins := make([]usage.Bin, len(starts))
	spans := make([][2]int64, len(starts))
	for i, start := range starts {
		// The first bucket opens with the range and the last one closes with
		// it, whatever their aligned starts are, so the buckets partition the
		// range exactly.
		fromMS, untilMS := sc.fromMS, sc.untilMS
		if i > 0 {
			fromMS = start.UnixMilli()
		}
		if i+1 < len(starts) {
			untilMS = starts[i+1].UnixMilli()
		}
		spans[i] = [2]int64{fromMS, untilMS}
		bins[i].From = start.UTC()
	}
	out.Series = bins
	encoded, err := json.Marshal(spans)
	if err != nil {
		return fmt.Errorf("encode series buckets: %w", err)
	}
	args := sc.args(sql.Named(aggArgBins, string(encoded)))
	// CROSS JOIN keeps the buckets as the outer loop, so every bucket is one
	// index range scan instead of one pass over the table per bucket.
	// Each query fills its own fields of a scratch bucket; the rest stay zero
	// and the scratch bucket is added to the one the row names.
	fills := []struct {
		query string
		scan  func(rows *sql.Rows, i *int, v *usage.Bin) error
	}{
		{
			`SELECT b.i, SUM(m.input), SUM(m.output), SUM(m.cache_read), SUM(m.cache_create)
			 FROM bins b CROSS JOIN model_call m ON ` + aggInBin("m") + ` GROUP BY b.i`,
			func(rows *sql.Rows, i *int, v *usage.Bin) error {
				return rows.Scan(i, &v.Tokens.Input, &v.Tokens.Output, &v.Tokens.CacheRead, &v.Tokens.CacheCreate)
			},
		},
		{
			`SELECT b.i, COUNT(*) FROM bins b CROSS JOIN prompt p ON ` + aggInBin("p") + ` GROUP BY b.i`,
			func(rows *sql.Rows, i *int, v *usage.Bin) error { return rows.Scan(i, &v.Prompts) },
		},
		{
			`SELECT b.i, COUNT(*), COALESCE(SUM(r.is_error), 0)
			 FROM bins b CROSS JOIN tool_call c ON ` + aggInBin("c") + `
			 LEFT JOIN tool_result r ON ` + aggResultJoin + ` GROUP BY b.i`,
			func(rows *sql.Rows, i *int, v *usage.Bin) error { return rows.Scan(i, &v.ToolCalls, &v.ToolErrors) },
		},
		{
			`SELECT i, COUNT(*) FROM (
				SELECT b.i AS i, m.session_id FROM bins b CROSS JOIN model_call m ON ` + aggInBin("m") + `
				UNION
				SELECT b.i, p.session_id FROM bins b CROSS JOIN prompt p ON ` + aggInBin("p") + `
			 ) GROUP BY i`,
			func(rows *sql.Rows, i *int, v *usage.Bin) error { return rows.Scan(i, &v.Sessions) },
		},
	}
	for _, fill := range fills {
		err = s.aggQuery(ctx, aggBinsCTE+fill.query, args, func(rows *sql.Rows) error {
			var i int
			var v usage.Bin
			if scanErr := fill.scan(rows, &i, &v); scanErr != nil {
				return scanErr
			}
			// The index is the one json_each assigned: the position in spans.
			if i < 0 || i >= len(bins) {
				return fmt.Errorf("series bucket %d out of range", i)
			}
			aggAddBin(&bins[i], v)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// aggAddBin adds the counters of v to dst and leaves dst.From alone.
func aggAddBin(dst *usage.Bin, v usage.Bin) {
	dst.Tokens.Input += v.Tokens.Input
	dst.Tokens.Output += v.Tokens.Output
	dst.Tokens.CacheRead += v.Tokens.CacheRead
	dst.Tokens.CacheCreate += v.Tokens.CacheCreate
	dst.Sessions += v.Sessions
	dst.Prompts += v.Prompts
	dst.ToolCalls += v.ToolCalls
	dst.ToolErrors += v.ToolErrors
}

// aggActiveDays counts the local dates in loc with a prompt or a model call.
// SQLite knows no time zones, so it only reports the first and last row of
// every slot that has one. Dates grow with time and a slot is shorter than a
// day, so a slot touches at most the dates of those two rows.
func (s *Store) aggActiveDays(ctx context.Context, sc aggScope, loc *time.Location, out *usage.Overview) error {
	query := `SELECT MIN(at_ms), MAX(at_ms) FROM (
		SELECT m.at_ms AS at_ms FROM model_call m WHERE ` + aggInRange("m") + `
		UNION ALL
		SELECT p.at_ms FROM prompt p WHERE ` + aggInRange("p") + `
	) GROUP BY at_ms / :` + aggArgSlot
	type date struct {
		year  int
		month time.Month
		day   int
	}
	days := map[date]struct{}{}
	err := s.aggQuery(ctx, query, sc.args(sql.Named(aggArgSlot, aggActiveSlotMS)), func(rows *sql.Rows) error {
		var firstMS, lastMS int64
		if err := rows.Scan(&firstMS, &lastMS); err != nil {
			return err
		}
		for _, ms := range [...]int64{firstMS, lastMS} {
			var d date
			d.year, d.month, d.day = time.UnixMilli(ms).In(loc).Date()
			days[d] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}
	out.ActiveDays = int64(len(days))
	return nil
}

// aggHeatmap fills all 168 weekday-hour cells, Sunday first, with the prompts
// of the range by local time in loc.
func (s *Store) aggHeatmap(ctx context.Context, sc aggScope, loc *time.Location, out *usage.Overview) error {
	cells := make([]usage.HeatCell, aggWeekdays*aggDayHours)
	for i := range cells {
		cells[i].Weekday, cells[i].Hour = i/aggDayHours, i%aggDayHours
	}
	out.Heatmap = cells
	query := `SELECT p.at_ms FROM prompt p WHERE ` + aggInRange("p")
	return s.aggQuery(ctx, query, sc.args(), func(rows *sql.Rows) error {
		var ms int64
		if err := rows.Scan(&ms); err != nil {
			return err
		}
		at := time.UnixMilli(ms).In(loc)
		cells[int(at.Weekday())*aggDayHours+at.Hour()].Prompts++
		return nil
	})
}

// aggModels fills the per-model rows, ordered by total tokens descending and
// then by model ID.
func (s *Store) aggModels(ctx context.Context, sc aggScope, out *usage.Overview) error {
	query := `SELECT m.model, COUNT(*), COUNT(DISTINCT m.session_id),
	       SUM(m.input), SUM(m.output), SUM(m.cache_read), SUM(m.cache_create)
	FROM model_call m WHERE ` + aggInRange("m") + `
	GROUP BY m.model
	ORDER BY SUM(m.input) + SUM(m.output) + SUM(m.cache_read) + SUM(m.cache_create) DESC, m.model ASC
	LIMIT :` + aggArgLimit
	out.Models = []usage.ModelUsage{}
	return s.aggQuery(ctx, query, sc.args(sql.Named(aggArgLimit, aggMaxRows)), func(rows *sql.Rows) error {
		var model usage.ModelUsage
		if err := rows.Scan(&model.Model, &model.Calls, &model.Sessions,
			&model.Tokens.Input, &model.Tokens.Output, &model.Tokens.CacheRead, &model.Tokens.CacheCreate); err != nil {
			return err
		}
		out.Models = append(out.Models, model)
		return nil
	})
}

// aggTopLists fills the busiest projects and tools. A project is listed only
// when it has a session in range, so excluded and idle projects stay out.
func (s *Store) aggTopLists(ctx context.Context, sc aggScope, out *usage.Overview) error {
	projects, err := s.aggProjectRows(ctx, sc)
	if err != nil {
		return err
	}
	out.Projects = []usage.ProjectUsage{}
	for _, project := range projects {
		if project.Sessions > 0 && len(out.Projects) < aggTopProjects {
			out.Projects = append(out.Projects, project)
		}
	}
	out.Tools, err = s.aggToolRows(ctx, sc, aggTopTools)
	return err
}
