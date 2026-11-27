package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Percentiles reported for closed tool calls.
const (
	aggP50 = 0.50
	aggP95 = 0.95
)

// aggArgNames carries the JSON array of tool names whose durations are read.
const aggArgNames = "names"

// aggResultJoin pairs tool_call c with its tool_result r; a call without a
// result row is pending.
const aggResultJoin = "r.session_id = c.session_id AND r.key = c.key"

// Tools aggregates tool, MCP server, skill, plugin, sub-agent, Bash program
// and hook usage of the query range.
// The Coverage field is left zero for the service to fill.
//
// A call belongs to the range by the time it was proposed, whenever its
// result arrived. Every list is ordered by count descending and then by name,
// and holds at most usage.MaxPageSize rows.
//
// Skills are grouped by bare name: a Skill call may name its skill as
// <plugin>:<skill> while the transcript attributes model calls to <skill>, so
// both are keyed by the part after the last colon. A skill or plugin row
// always carries Tokens; Sessions counts the sessions that called it or had a
// model call attributed to it. The duration of a hook is averaged over the
// runs that reported one, and is nil when none did.
//
// A range that does not start before it ends is usage.ErrBadRequest.
func (s *Store) Tools(ctx context.Context, q usage.Query) (usage.Tools, error) {
	sc, err := aggNewScope(q)
	if err != nil {
		return usage.Tools{}, err
	}
	out := usage.Tools{Range: q.Range}
	if out.Tools, err = s.aggToolRows(ctx, sc, aggMaxRows); err != nil {
		return usage.Tools{}, fmt.Errorf("query usage tools: %w", err)
	}
	// One entry per group list: a new grouping is added here and nowhere else.
	groups := []struct {
		dst  *[]usage.GroupUsage
		spec aggGroupSpec
	}{
		{&out.MCPServers, aggGroupSpec{label: "c.mcp_server", filter: "c.mcp_server <> ''"}},
		{&out.Skills, aggGroupSpec{
			label:  aggBareName("c.skill"),
			filter: "c.category = '" + string(usage.ToolSkill) + "'",
			tokens: aggBareName("m.skill"),
		}},
		{&out.Plugins, aggGroupSpec{label: "c.plugin", filter: "c.plugin <> ''", tokens: "m.plugin"}},
		{&out.Subagents, aggGroupSpec{label: "c.subagent_type", filter: "c.category = '" + string(usage.ToolAgent) + "'"}},
		{&out.Programs, aggGroupSpec{label: "c.target", filter: "c.name = '" + usage.ToolNameBash + "' AND c.target <> ''"}},
	}
	for _, group := range groups {
		if *group.dst, err = s.aggGroupRows(ctx, sc, group.spec); err != nil {
			return usage.Tools{}, fmt.Errorf("query usage tool groups: %w", err)
		}
	}
	if out.Hooks, err = s.aggHookRows(ctx, sc); err != nil {
		return usage.Tools{}, fmt.Errorf("query usage hooks: %w", err)
	}
	return out, nil
}

// aggToolRows returns the limit most-called tools of the scope, one row per
// tool name, ordered by calls descending and then by name. MCPServer and
// Plugin are reported only when every call of the tool agrees on them: the
// Skill tool, for one, is called for skills of different plugins.
func (s *Store) aggToolRows(ctx context.Context, sc aggScope, limit int) ([]usage.ToolUsage, error) {
	query := `SELECT c.name, MIN(c.category),
	       CASE WHEN MIN(c.mcp_server) = MAX(c.mcp_server) THEN MIN(c.mcp_server) ELSE '' END,
	       CASE WHEN MIN(c.plugin) = MAX(c.plugin) THEN MIN(c.plugin) ELSE '' END,
	       COUNT(*), COALESCE(SUM(r.is_error), 0), SUM(r.key IS NULL), COUNT(DISTINCT c.session_id)
	FROM tool_call c LEFT JOIN tool_result r ON ` + aggResultJoin + `
	WHERE ` + aggInRange("c") + `
	GROUP BY c.name ORDER BY COUNT(*) DESC, c.name ASC LIMIT :` + aggArgLimit
	tools := []usage.ToolUsage{}
	err := s.aggQuery(ctx, query, sc.args(sql.Named(aggArgLimit, limit)), func(rows *sql.Rows) error {
		var tool usage.ToolUsage
		var category string
		if err := rows.Scan(&tool.Name, &category, &tool.MCPServer, &tool.Plugin,
			&tool.Calls, &tool.Errors, &tool.Pending, &tool.Sessions); err != nil {
			return err
		}
		tool.Category = usage.ToolCategory(category)
		tools = append(tools, tool)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = s.aggToolPercentiles(ctx, sc, tools); err != nil {
		return nil, err
	}
	return tools, nil
}

// aggToolPercentiles fills P50Ms and P95Ms of tools from the durations of
// their closed calls. A tool without a closed call keeps nil.
func (s *Store) aggToolPercentiles(ctx context.Context, sc aggScope, tools []usage.ToolUsage) error {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, len(tools))
	index := make(map[string]int, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
		index[tool.Name] = i
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("encode tool names: %w", err)
	}
	query := `SELECT c.name, ` + toolDurationSQL + `
	FROM tool_call c JOIN tool_result r ON ` + aggResultJoin + `
	WHERE ` + aggInRange("c") + ` AND c.name IN (SELECT value FROM json_each(:` + aggArgNames + `))`
	durations := make([][]float64, len(tools))
	err = s.aggQuery(ctx, query, sc.args(sql.Named(aggArgNames, string(encoded))), func(rows *sql.Rows) error {
		var name string
		var ms float64
		if err := rows.Scan(&name, &ms); err != nil {
			return err
		}
		if i, ok := index[name]; ok {
			durations[i] = append(durations[i], ms)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := range tools {
		slices.Sort(durations[i])
		tools[i].P50Ms = percentile(durations[i], aggP50)
		tools[i].P95Ms = percentile(durations[i], aggP95)
	}
	return nil
}

// aggGroupSpec describes one grouping of tool calls. label is the SQL
// expression over tool_call c that names the group and filter the condition
// that selects its calls. tokens, when set, is the SQL expression over
// model_call m that names the group a model call is attributed to.
type aggGroupSpec struct {
	label  string
	filter string
	tokens string
}

// aggBareName is the SQL expression for the part of column after its last
// colon, or the whole value when it has none. rtrim strips the trailing
// characters that are not colons, which leaves the prefix up to and including
// the last colon; the name starts right after it.
func aggBareName(column string) string {
	return "substr(" + column + ", length(rtrim(" + column + ", replace(" + column + ", ':', ''))) + 1)"
}

// query builds the grouping statement. Tool calls and attributed model calls
// are stacked into one relation first, so that Sessions counts a session once
// even when it appears on both sides.
func (g aggGroupSpec) query() string {
	query := `SELECT name, SUM(calls), SUM(errors), COUNT(DISTINCT session_id), SUM(closed), COALESCE(SUM(duration), 0),
	       SUM(input), SUM(output), SUM(cache_read), SUM(cache_create)
	FROM (
		SELECT ` + g.label + ` AS name, c.session_id AS session_id, 1 AS calls, COALESCE(r.is_error, 0) AS errors,
		       r.key IS NOT NULL AS closed, ` + toolDurationSQL + ` AS duration,
		       0 AS input, 0 AS output, 0 AS cache_read, 0 AS cache_create
		FROM tool_call c LEFT JOIN tool_result r ON ` + aggResultJoin + `
		WHERE ` + aggInRange("c") + ` AND ` + g.filter
	if g.tokens != "" {
		query += `
		UNION ALL
		SELECT ` + g.tokens + `, m.session_id, 0, 0, 0, NULL, m.input, m.output, m.cache_read, m.cache_create
		FROM model_call m WHERE ` + aggInRange("m") + ` AND ` + g.tokens + ` <> ''`
	}
	return query + `
	) GROUP BY name ORDER BY SUM(calls) DESC, name ASC LIMIT :` + aggArgLimit
}

// aggGroupRows returns the groups of spec in the scope, ordered by calls
// descending and then by name. AvgMs covers closed calls only.
func (s *Store) aggGroupRows(ctx context.Context, sc aggScope, spec aggGroupSpec) ([]usage.GroupUsage, error) {
	groups := []usage.GroupUsage{}
	err := s.aggQuery(ctx, spec.query(), sc.args(sql.Named(aggArgLimit, aggMaxRows)), func(rows *sql.Rows) error {
		var group usage.GroupUsage
		var tokens usage.TokenTotals
		var closed, durationMS int64
		if err := rows.Scan(&group.Name, &group.Calls, &group.Errors, &group.Sessions, &closed, &durationMS,
			&tokens.Input, &tokens.Output, &tokens.CacheRead, &tokens.CacheCreate); err != nil {
			return err
		}
		if closed > 0 {
			avg := float64(durationMS) / float64(closed)
			group.AvgMs = &avg
		}
		if spec.tokens != "" {
			group.Tokens = &tokens
		}
		groups = append(groups, group)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return groups, nil
}

// aggHookRows returns the hooks of the scope by event and name, ordered by
// runs descending, then event, then name. A run fails when its status is
// error or blocked. A stored duration of zero means the transcript reported
// none, so such runs do not pull the average toward zero.
func (s *Store) aggHookRows(ctx context.Context, sc aggScope) ([]usage.HookUsage, error) {
	const argError, argBlocked = "hook_error", "hook_blocked"
	query := `SELECT h.event, h.name, COUNT(*), SUM(h.status IN (:` + argError + `, :` + argBlocked + `)),
	       AVG(CASE WHEN h.duration_ms > 0 THEN h.duration_ms END),
	       MAX(CASE WHEN h.duration_ms > 0 THEN h.duration_ms END)
	FROM hook_run h WHERE ` + aggInRange("h") + `
	GROUP BY h.event, h.name ORDER BY COUNT(*) DESC, h.event ASC, h.name ASC LIMIT :` + aggArgLimit
	args := sc.args(
		sql.Named(argError, string(usage.HookError)),
		sql.Named(argBlocked, string(usage.HookBlocked)),
		sql.Named(aggArgLimit, aggMaxRows),
	)
	hooks := []usage.HookUsage{}
	err := s.aggQuery(ctx, query, args, func(rows *sql.Rows) error {
		var hook usage.HookUsage
		var avg, peak sql.NullFloat64
		if err := rows.Scan(&hook.Event, &hook.Name, &hook.Runs, &hook.Failures, &avg, &peak); err != nil {
			return err
		}
		if avg.Valid {
			hook.AvgMs = &avg.Float64
		}
		if peak.Valid {
			hook.MaxMs = &peak.Float64
		}
		hooks = append(hooks, hook)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return hooks, nil
}
