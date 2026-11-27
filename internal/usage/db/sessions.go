package db

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// sessCursorVersion is bumped when the cursor layout changes, so that a
	// cursor issued by an older build is refused instead of misread.
	sessCursorVersion = 1
	// sessMaxCursorLen bounds the cursor text accepted from a request. A
	// cursor holds one sort position and one identifier.
	sessMaxCursorLen = 4096
	// sessAllFromMS and sessAllUntilMS select every stored row when a
	// range-bound query is reused for a whole session.
	sessAllFromMS  int64 = math.MinInt64
	sessAllUntilMS int64 = math.MaxInt64
	// sessDetailSessions is the session count of every group in a session
	// detail: the detail covers exactly one session.
	sessDetailSessions int64 = 1
	// sessSkillQualifier separates the plugin from the skill in the name of a
	// plugin-provided skill: <plugin>:<skill>.
	sessSkillQualifier = ":"
	sessP50            = 0.50
	sessP95            = 0.95
)

// Sort ranks. A session that lacks the value it is sorted by goes after
// every session that has one.
const (
	sessRankValued = iota
	sessRankMissing
)

// The four fragments that turn sessRowsTemplate into the list query (rows in
// a time range) or the detail query (rows of one session).
const (
	sessListCond     = "at_ms >= ?1 AND at_ms < ?2"
	sessListToolCond = "c.at_ms >= ?1 AND c.at_ms < ?2"
	// A root session is in range when a model call or a prompt of any of its
	// agents falls into the range; see db.go.
	sessListIDs    = "SELECT session_id FROM mc UNION SELECT session_id FROM pr"
	sessListFilter = `WHERE (?3 = '' OR project_key = ?3)
	  AND (?4 = '' OR title LIKE ?4 ESCAPE '\' OR project_path LIKE ?4 ESCAPE '\')`

	sessDetailCond     = "session_id = ?1"
	sessDetailToolCond = "c.session_id = ?1"
	sessDetailIDs      = "SELECT ?1 AS session_id"
	sessDetailFilter   = ""
)

// sessRowsTemplate computes one usage.SessionRow per session ID. Verb 1 is
// the row condition of model_call, prompt and compaction, verb 2 the same for
// tool_call aliased as c, verb 3 the session IDs to report and verb 4 the
// final filter. Labels come from the root stream only; a session without one
// keeps the project key of any of its streams.
const sessRowsTemplate = `
WITH mc AS (
	SELECT session_id, COUNT(*) AS calls,
	       SUM(input) AS input, SUM(output) AS output,
	       SUM(cache_read) AS cache_read, SUM(cache_create) AS cache_create,
	       COUNT(DISTINCT CASE WHEN agent_id <> '' THEN agent_id END) AS subagents,
	       MIN(project_key) AS project_key
	FROM model_call WHERE %[1]s GROUP BY session_id),
pr AS (
	SELECT session_id, COUNT(*) AS n, MIN(project_key) AS project_key
	FROM prompt WHERE %[1]s GROUP BY session_id),
tc AS (
	SELECT c.session_id, COUNT(*) AS n, COALESCE(SUM(r.is_error), 0) AS errors
	FROM tool_call c
	LEFT JOIN tool_result r ON r.session_id = c.session_id AND r.key = c.key
	WHERE %[2]s GROUP BY c.session_id),
cp AS (
	SELECT session_id, COUNT(*) AS n FROM compaction WHERE %[1]s GROUP BY session_id),
ids AS (%[3]s),
st AS (
	SELECT session_id,
	       COALESCE(MIN(CASE WHEN first_ms > 0 THEN first_ms END), 0) AS first_ms,
	       MAX(last_ms) AS last_ms, MIN(project_key) AS project_key
	FROM stream WHERE session_id IN (SELECT session_id FROM ids) GROUP BY session_id),
sess AS (
	SELECT ids.session_id AS id,
	       COALESCE(root.title, '') AS title,
	       COALESCE(root.project_key, st.project_key, mc.project_key, pr.project_key, '') AS project_key,
	       COALESCE(root.project_path, '') AS project_path,
	       COALESCE(root.entrypoint, '') AS entrypoint,
	       COALESCE(root.git_branch, '') AS git_branch,
	       COALESCE(st.first_ms, 0) AS first_ms, COALESCE(st.last_ms, 0) AS last_ms,
	       COALESCE(pr.n, 0) AS prompts, COALESCE(mc.calls, 0) AS model_calls,
	       COALESCE(tc.n, 0) AS tool_calls, COALESCE(tc.errors, 0) AS tool_errors,
	       COALESCE(mc.subagents, 0) AS subagents, COALESCE(cp.n, 0) AS compactions,
	       COALESCE(mc.input, 0) AS input, COALESCE(mc.output, 0) AS output,
	       COALESCE(mc.cache_read, 0) AS cache_read, COALESCE(mc.cache_create, 0) AS cache_create,
	       cs.total_cost_usd AS cost
	FROM ids
	LEFT JOIN stream root ON root.session_id = ids.session_id AND root.agent_id = ''
	LEFT JOIN st ON st.session_id = ids.session_id
	LEFT JOIN mc ON mc.session_id = ids.session_id
	LEFT JOIN pr ON pr.session_id = ids.session_id
	LEFT JOIN tc ON tc.session_id = ids.session_id
	LEFT JOIN cp ON cp.session_id = ids.session_id
	LEFT JOIN cost_state cs ON cs.session_id = ids.session_id)
SELECT id, title, project_key, project_path, entrypoint, git_branch, first_ms, last_ms,
       prompts, model_calls, tool_calls, tool_errors, subagents, compactions,
       input, output, cache_read, cache_create, cost
FROM sess %[4]s`

const sessModelNamesSQL = `
SELECT session_id, model FROM model_call
WHERE session_id IN (%s) AND at_ms >= ? AND at_ms < ? AND model <> ''
GROUP BY session_id, model
ORDER BY session_id, SUM(input + output + cache_read + cache_create) DESC, model`

const sessStreamsSQL = `SELECT agent_id, agent_type, first_ms, last_ms FROM stream WHERE session_id = ?`

const sessToolCallsSQL = `
SELECT c.agent_id, c.name, c.category, c.mcp_server, c.plugin, c.skill, c.message_key,
       r.key IS NOT NULL, COALESCE(r.is_error, 0), ` + toolDurationSQL + `
FROM tool_call c
LEFT JOIN tool_result r ON r.session_id = c.session_id AND r.key = c.key
WHERE c.session_id = ? ORDER BY c.at_ms, c.key`

const sessModelCallsSQL = `
SELECT agent_id, key, at_ms, model, input, output, cache_read, cache_create, skill, plugin
FROM model_call WHERE session_id = ? ORDER BY at_ms, key`

const sessHooksSQL = `
SELECT event, name, COUNT(*), SUM(CASE WHEN status = ?2 THEN 1 ELSE 0 END),
       AVG(duration_ms), MAX(duration_ms)
FROM hook_run WHERE session_id = ?1
GROUP BY event, name ORDER BY COUNT(*) DESC, event, name`

const sessExtSourcesSQL = `
SELECT source, COUNT(*), SUM(CASE WHEN status = ?2 THEN 1 ELSE 0 END), AVG(duration_ms)
FROM ext_event WHERE session_id = ?1
GROUP BY source ORDER BY COUNT(*) DESC, source`

const sessStreamFileSQL = `SELECT file_path FROM stream WHERE session_id = ? AND agent_id = ?`

// sessPos is the place of one session in a sorted list. Rank orders first,
// then Cost and N from high to low, then ID. A sort order fills the fields
// it ranks by and leaves the others zero.
type sessPos struct {
	Rank int
	Cost float64
	N    int64
	ID   string
}

// sessCursor is the JSON form of the position after which a page continues.
// Cost travels as IEEE 754 bits so that every stored value survives the trip.
type sessCursor struct {
	Version int               `json:"v"`
	Sort    usage.SessionSort `json:"s"`
	Rank    int               `json:"r,omitempty"`
	Cost    uint64            `json:"c,omitempty"`
	N       int64             `json:"n,omitempty"`
	ID      string            `json:"i"`
}

// sessRanked pairs a row with its position in the requested order.
type sessRanked struct {
	pos sessPos
	row usage.SessionRow
}

// sessSortKey returns the position function of a sort order. It is the one
// place that knows the orders: a new order is one more case here.
func sessSortKey(order usage.SessionSort) (func(usage.SessionRow) sessPos, bool) {
	switch order {
	case usage.SortLast:
		return func(r usage.SessionRow) sessPos { return sessPos{N: toMS(r.LastAt), ID: r.ID} }, true
	case usage.SortTokens:
		return func(r usage.SessionRow) sessPos {
			return sessPos{N: r.Tokens.Input + r.Tokens.Output + r.Tokens.CacheRead + r.Tokens.CacheCreate, ID: r.ID}
		}, true
	case usage.SortErrors:
		return func(r usage.SessionRow) sessPos { return sessPos{N: r.ToolErrors, ID: r.ID} }, true
	case usage.SortCost:
		return func(r usage.SessionRow) sessPos {
			if r.CostUSD == nil {
				return sessPos{Rank: sessRankMissing, ID: r.ID}
			}
			return sessPos{Cost: *r.CostUSD, ID: r.ID}
		}, true
	}
	return nil, false
}

// sessCompare orders two positions; a negative result puts a first.
func sessCompare(a, b sessPos) int {
	return cmp.Or(
		cmp.Compare(a.Rank, b.Rank),
		cmp.Compare(b.Cost, a.Cost),
		cmp.Compare(b.N, a.N),
		cmp.Compare(a.ID, b.ID),
	)
}

// sessPageLimit resolves the requested page size.
func sessPageLimit(limit int) (int, error) {
	switch {
	case limit < 0:
		return 0, fmt.Errorf("page limit %d: %w", limit, usage.ErrBadRequest)
	case limit == 0:
		return usage.DefaultPageSize, nil
	default:
		return min(limit, usage.MaxPageSize), nil
	}
}

// sessCursorEncode turns a cursor value into its opaque text form.
func sessCursorEncode(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// sessCursorDecode is the inverse of sessCursorEncode. Text that this package
// did not produce is a usage.ErrBadRequest.
func sessCursorDecode(cursor string, v any) error {
	if len(cursor) > sessMaxCursorLen {
		return fmt.Errorf("cursor too long: %w", usage.ErrBadRequest)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return fmt.Errorf("decode cursor: %w: %w", usage.ErrBadRequest, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(v); err != nil {
		return fmt.Errorf("decode cursor: %w: %w", usage.ErrBadRequest, err)
	}
	return nil
}

// sessCollect runs a read query directly on the pool and scans every row.
// The result is never nil.
func sessCollect[T any](ctx context.Context, conn *sql.DB, label string, scan func(*sql.Rows) (T, error), query string, args ...any) ([]T, error) {
	rows, err := conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		v, scanErr := scan(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("%s: %w", label, scanErr)
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return out, nil
}

// sessLikePattern builds the LIKE pattern of a substring search with the
// wildcards of the search text escaped. An empty result means no search.
// SQLite's LIKE ignores case for ASCII letters only.
func sessLikePattern(search string) string {
	if search == "" {
		return ""
	}
	escape := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + escape.Replace(search) + "%"
}

func sessScanRow(rows *sql.Rows) (usage.SessionRow, error) {
	var (
		r               usage.SessionRow
		firstMS, lastMS int64
		cost            sql.NullFloat64
	)
	err := rows.Scan(&r.ID, &r.Title, &r.ProjectKey, &r.ProjectPath, &r.Entrypoint, &r.GitBranch, &firstMS, &lastMS,
		&r.Prompts, &r.ModelCalls, &r.ToolCalls, &r.ToolErrors, &r.Subagents, &r.Compactions,
		&r.Tokens.Input, &r.Tokens.Output, &r.Tokens.CacheRead, &r.Tokens.CacheCreate, &cost)
	if err != nil {
		return usage.SessionRow{}, err
	}
	r.StartedAt, r.LastAt = fromMS(firstMS), fromMS(lastMS)
	if cost.Valid {
		r.CostUSD = &cost.Float64
	}
	r.Models = []string{}
	return r, nil
}

// sessModelNames lists, per session, the distinct models called in
// [fromMS, untilMS), heaviest by tokens first.
func (s *Store) sessModelNames(ctx context.Context, ids []string, fromMS, untilMS int64) (map[string][]string, error) {
	names := make(map[string][]string, len(ids))
	if len(ids) == 0 {
		return names, nil
	}
	args := make([]any, 0, len(ids)+2)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, fromMS, untilMS)
	query := fmt.Sprintf(sessModelNamesSQL, strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","))
	pairs, err := sessCollect(ctx, s.db, "read session models", func(rows *sql.Rows) ([2]string, error) {
		var pair [2]string
		return pair, rows.Scan(&pair[0], &pair[1])
	}, query, args...)
	if err != nil {
		return nil, err
	}
	for _, pair := range pairs {
		names[pair[0]] = append(names[pair[0]], pair[1])
	}
	return names, nil
}

// sessFillModels sets Models on every row from one query.
func (s *Store) sessFillModels(ctx context.Context, rows []usage.SessionRow, fromMS, untilMS int64) error {
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	names, err := s.sessModelNames(ctx, ids, fromMS, untilMS)
	if err != nil {
		return err
	}
	for i := range rows {
		if models := names[rows[i].ID]; models != nil {
			rows[i].Models = models
		}
	}
	return nil
}

// Sessions returns one page of root sessions with activity in the query
// range. Counts and tokens cover only that range and include sub-agents. The
// Coverage field is left zero for the service to fill.
func (s *Store) Sessions(ctx context.Context, q usage.SessionQuery) (usage.SessionPage, error) {
	order := q.Sort
	if order == "" {
		order = usage.SortLast
	}
	keyOf, ok := sessSortKey(order)
	if !ok {
		return usage.SessionPage{}, fmt.Errorf("session sort %q: %w", order, usage.ErrBadRequest)
	}
	limit, err := sessPageLimit(q.Limit)
	if err != nil {
		return usage.SessionPage{}, err
	}
	if !q.Range.Until.After(q.Range.From) {
		return usage.SessionPage{}, fmt.Errorf("session range is empty: %w", usage.ErrBadRequest)
	}
	var after *sessPos
	if q.Cursor != "" {
		var c sessCursor
		if err = sessCursorDecode(q.Cursor, &c); err != nil {
			return usage.SessionPage{}, err
		}
		if c.Version != sessCursorVersion || c.Sort != order || c.ID == "" {
			return usage.SessionPage{}, fmt.Errorf("cursor does not match the request: %w", usage.ErrBadRequest)
		}
		after = &sessPos{Rank: c.Rank, Cost: math.Float64frombits(c.Cost), N: c.N, ID: c.ID}
	}
	page := usage.SessionPage{Items: []usage.SessionRow{}}
	// Spaces typed around the search text are not part of it. Stored titles
	// and paths are cut at usage.MaxLabelLen, so a longer search cannot match
	// and must not reach the LIKE pattern length limit.
	search := strings.TrimSpace(q.Search)
	if utf8.RuneCountInString(search) > usage.MaxLabelLen {
		return page, nil
	}
	fromMS, untilMS := toMS(q.Range.From), toMS(q.Range.Until)
	// Sort keys are aggregates, so every matching session is computed either
	// way; ordering and paging happen here to keep one definition of the order.
	rows, err := sessCollect(ctx, s.db, "read sessions", sessScanRow,
		fmt.Sprintf(sessRowsTemplate, sessListCond, sessListToolCond, sessListIDs, sessListFilter),
		fromMS, untilMS, q.Project, sessLikePattern(search))
	if err != nil {
		return usage.SessionPage{}, err
	}
	ranked := make([]sessRanked, len(rows))
	for i, row := range rows {
		ranked[i] = sessRanked{pos: keyOf(row), row: row}
	}
	slices.SortFunc(ranked, func(a, b sessRanked) int { return sessCompare(a.pos, b.pos) })
	page.Total = len(ranked)
	start := 0
	if after != nil {
		start = sort.Search(len(ranked), func(i int) bool { return sessCompare(ranked[i].pos, *after) > 0 })
	}
	end := min(start+limit, len(ranked))
	for _, r := range ranked[start:end] {
		page.Items = append(page.Items, r.row)
	}
	if end < len(ranked) && end > start {
		last := ranked[end-1].pos
		page.NextCursor, err = sessCursorEncode(sessCursor{
			Version: sessCursorVersion, Sort: order,
			Rank: last.Rank, Cost: math.Float64bits(last.Cost), N: last.N, ID: last.ID,
		})
		if err != nil {
			return usage.SessionPage{}, err
		}
	}
	if err = s.sessFillModels(ctx, page.Items, fromMS, untilMS); err != nil {
		return usage.SessionPage{}, err
	}
	return page, nil
}

// Session returns the whole-session detail regardless of any range. Source is
// left empty for the service to fill. It returns usage.ErrNotFound for an
// unknown session.
func (s *Store) Session(ctx context.Context, sessionID string) (usage.SessionDetail, error) {
	return s.sessDetail(ctx, sessionID, usage.MaxTurnPoints)
}

// StreamFile returns the root-relative transcript path of one stream. ok is
// false when the stream is not stored.
func (s *Store) StreamFile(ctx context.Context, sessionID, agentID string) (path string, ok bool, err error) {
	paths, err := sessCollect(ctx, s.db, "read stream file", func(rows *sql.Rows) (string, error) {
		var p string
		return p, rows.Scan(&p)
	}, sessStreamFileSQL, sessionID, agentID)
	if err != nil || len(paths) == 0 {
		return "", false, err
	}
	return paths[0], true, nil
}

// sessStream is one stored transcript stream of a session.
type sessStream struct {
	agentID, agentType string
	firstMS, lastMS    int64
}

// sessToolRow is one tool call of a session joined to its result.
type sessToolRow struct {
	agentID, name, category  string
	mcpServer, plugin, skill string
	messageKey               string
	closed, isError          bool
	durationMs               sql.NullInt64
}

// sessModelRow is one model call of a session.
type sessModelRow struct {
	agentID, key, model string
	atMS                int64
	tokens              usage.TokenTotals
	skill, plugin       string
}

// sessAgentStat accumulates the activity of one agent.
type sessAgentStat struct {
	modelCalls, toolCalls, toolErrors int64
	tokens                            usage.TokenTotals
}

// sessToolStat accumulates one tool; durations holds its closed calls.
type sessToolStat struct {
	row       usage.ToolUsage
	durations []float64
}

// sessGroup accumulates one grouping label.
type sessGroup struct {
	calls, errors, closed int64
	durationSum           float64
	tokens                *usage.TokenTotals
}

// sessAcc folds the tool and model calls of one session into the detail
// lists. Tool calls must be added before model calls: a turn point needs the
// tool calls its model call proposed.
type sessAcc struct {
	maxTurns       int
	agents         map[string]*sessAgentStat
	tools          map[string]*sessToolStat
	models         map[string]*usage.ModelUsage
	mcpServers     map[string]*sessGroup
	skills         map[string]*sessGroup
	plugins        map[string]*sessGroup
	rootToolsByMsg map[string]int64
	turns          []usage.TurnPoint
	truncated      bool
}

func sessNewAcc(maxTurns int) *sessAcc {
	return &sessAcc{
		maxTurns:       maxTurns,
		agents:         map[string]*sessAgentStat{},
		tools:          map[string]*sessToolStat{},
		models:         map[string]*usage.ModelUsage{},
		mcpServers:     map[string]*sessGroup{},
		skills:         map[string]*sessGroup{},
		plugins:        map[string]*sessGroup{},
		rootToolsByMsg: map[string]int64{},
		turns:          []usage.TurnPoint{},
	}
}

func sessAddTokens(dst *usage.TokenTotals, t usage.TokenTotals) {
	dst.Input += t.Input
	dst.Output += t.Output
	dst.CacheRead += t.CacheRead
	dst.CacheCreate += t.CacheCreate
}

func sessTokenSum(t usage.TokenTotals) int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheCreate
}

func (a *sessAcc) agent(id string) *sessAgentStat {
	st := a.agents[id]
	if st == nil {
		st = &sessAgentStat{}
		a.agents[id] = st
	}
	return st
}

func sessGroupOf(groups map[string]*sessGroup, name string) *sessGroup {
	g := groups[name]
	if g == nil {
		g = &sessGroup{}
		groups[name] = g
	}
	return g
}

func (g *sessGroup) addCall(c sessToolRow) {
	g.calls++
	if c.isError {
		g.errors++
	}
	if c.closed && c.durationMs.Valid {
		g.closed++
		g.durationSum += float64(c.durationMs.Int64)
	}
}

func (g *sessGroup) addTokens(t usage.TokenTotals) {
	if g.tokens == nil {
		g.tokens = &usage.TokenTotals{}
	}
	sessAddTokens(g.tokens, t)
}

func (a *sessAcc) addTool(c sessToolRow) {
	agent := a.agent(c.agentID)
	agent.toolCalls++
	tool := a.tools[c.name]
	if tool == nil {
		tool = &sessToolStat{row: usage.ToolUsage{
			Name: c.name, Category: usage.ToolCategory(c.category), MCPServer: c.mcpServer, Sessions: sessDetailSessions,
		}}
		// A tool name fixes its plugin only for MCP tools. The Skill tool
		// serves the skills of many plugins, which the plugin groups report.
		if tool.row.Category == usage.ToolMCP {
			tool.row.Plugin = c.plugin
		}
		a.tools[c.name] = tool
	}
	tool.row.Calls++
	if c.isError {
		agent.toolErrors++
		tool.row.Errors++
	}
	switch {
	case !c.closed:
		tool.row.Pending++
	case c.durationMs.Valid:
		tool.durations = append(tool.durations, float64(c.durationMs.Int64))
	}
	if c.mcpServer != "" {
		sessGroupOf(a.mcpServers, c.mcpServer).addCall(c)
	}
	if c.skill != "" {
		sessGroupOf(a.skills, c.skill).addCall(c)
	}
	if c.plugin != "" {
		sessGroupOf(a.plugins, c.plugin).addCall(c)
	}
	if c.agentID == "" && c.messageKey != "" {
		a.rootToolsByMsg[c.messageKey]++
	}
}

// sessSkillName is the group a model call attributed to skill belongs to.
// A Skill tool call names a plugin-provided skill as <plugin>:<skill>, while
// the attribution carries the two parts separately; joining them keeps the
// calls and the tokens of one skill in one group.
func sessSkillName(skill, plugin string) string {
	if plugin == "" || strings.Contains(skill, sessSkillQualifier) {
		return skill
	}
	return plugin + sessSkillQualifier + skill
}

func (a *sessAcc) addModel(m sessModelRow) {
	agent := a.agent(m.agentID)
	agent.modelCalls++
	sessAddTokens(&agent.tokens, m.tokens)
	model := a.models[m.model]
	if model == nil {
		model = &usage.ModelUsage{Model: m.model, Sessions: sessDetailSessions}
		a.models[m.model] = model
	}
	model.Calls++
	sessAddTokens(&model.Tokens, m.tokens)
	if m.skill != "" {
		sessGroupOf(a.skills, sessSkillName(m.skill, m.plugin)).addTokens(m.tokens)
	}
	if m.plugin != "" {
		sessGroupOf(a.plugins, m.plugin).addTokens(m.tokens)
	}
	if m.agentID != "" {
		return
	}
	if len(a.turns) >= a.maxTurns {
		a.truncated = true
		return
	}
	a.turns = append(a.turns, usage.TurnPoint{
		At:        fromMS(m.atMS),
		Model:     m.model,
		Context:   m.tokens.Input + m.tokens.CacheRead + m.tokens.CacheCreate,
		Output:    m.tokens.Output,
		ToolCalls: a.rootToolsByMsg[m.key],
	})
}

func (a *sessAcc) toolRows() []usage.ToolUsage {
	out := make([]usage.ToolUsage, 0, len(a.tools))
	for _, tool := range a.tools {
		slices.Sort(tool.durations)
		row := tool.row
		row.P50Ms = percentile(tool.durations, sessP50)
		row.P95Ms = percentile(tool.durations, sessP95)
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y usage.ToolUsage) int {
		return cmp.Or(cmp.Compare(y.Calls, x.Calls), cmp.Compare(x.Name, y.Name))
	})
	return out
}

func (a *sessAcc) modelRows() []usage.ModelUsage {
	out := make([]usage.ModelUsage, 0, len(a.models))
	for _, model := range a.models {
		out = append(out, *model)
	}
	slices.SortFunc(out, func(x, y usage.ModelUsage) int {
		return cmp.Or(cmp.Compare(sessTokenSum(y.Tokens), sessTokenSum(x.Tokens)), cmp.Compare(x.Model, y.Model))
	})
	return out
}

// sessGroupRows lists groups by calls, then by name.
func sessGroupRows(groups map[string]*sessGroup) []usage.GroupUsage {
	out := make([]usage.GroupUsage, 0, len(groups))
	for name, g := range groups {
		row := usage.GroupUsage{Name: name, Calls: g.calls, Errors: g.errors, Sessions: sessDetailSessions, Tokens: g.tokens}
		if g.closed > 0 {
			avg := g.durationSum / float64(g.closed)
			row.AvgMs = &avg
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(x, y usage.GroupUsage) int {
		return cmp.Or(cmp.Compare(y.Calls, x.Calls), cmp.Compare(x.Name, y.Name))
	})
	return out
}

// agentRows lists the stored streams: the root agent first, then sub-agents
// by start time with those that never reported a time last.
func (a *sessAcc) agentRows(streams []sessStream) []usage.AgentRow {
	slices.SortFunc(streams, func(x, y sessStream) int {
		return cmp.Or(
			cmp.Compare(sessFlag(x.agentID != ""), sessFlag(y.agentID != "")),
			cmp.Compare(sessFlag(x.firstMS == 0), sessFlag(y.firstMS == 0)),
			cmp.Compare(x.firstMS, y.firstMS),
			cmp.Compare(x.agentID, y.agentID),
		)
	})
	out := make([]usage.AgentRow, 0, len(streams))
	for _, st := range streams {
		row := usage.AgentRow{AgentID: st.agentID, AgentType: st.agentType, StartedAt: fromMS(st.firstMS), LastAt: fromMS(st.lastMS)}
		if stat := a.agents[st.agentID]; stat != nil {
			row.ModelCalls, row.ToolCalls, row.ToolErrors, row.Tokens = stat.modelCalls, stat.toolCalls, stat.toolErrors, stat.tokens
		}
		out = append(out, row)
	}
	return out
}

// sessFlag orders false before true.
func sessFlag(v bool) int {
	if v {
		return 1
	}
	return 0
}

func sessScanStream(rows *sql.Rows) (sessStream, error) {
	var st sessStream
	return st, rows.Scan(&st.agentID, &st.agentType, &st.firstMS, &st.lastMS)
}

func sessScanTool(rows *sql.Rows) (sessToolRow, error) {
	var c sessToolRow
	return c, rows.Scan(&c.agentID, &c.name, &c.category, &c.mcpServer, &c.plugin, &c.skill, &c.messageKey,
		&c.closed, &c.isError, &c.durationMs)
}

func sessScanModel(rows *sql.Rows) (sessModelRow, error) {
	var m sessModelRow
	return m, rows.Scan(&m.agentID, &m.key, &m.atMS, &m.model,
		&m.tokens.Input, &m.tokens.Output, &m.tokens.CacheRead, &m.tokens.CacheCreate, &m.skill, &m.plugin)
}

func sessNullable(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func sessScanHook(rows *sql.Rows) (usage.HookUsage, error) {
	var (
		h        usage.HookUsage
		avg, top sql.NullFloat64
	)
	if err := rows.Scan(&h.Event, &h.Name, &h.Runs, &h.Failures, &avg, &top); err != nil {
		return usage.HookUsage{}, err
	}
	h.AvgMs, h.MaxMs = sessNullable(avg), sessNullable(top)
	return h, nil
}

func sessScanExtSource(rows *sql.Rows) (usage.GroupUsage, error) {
	g := usage.GroupUsage{Sessions: sessDetailSessions}
	var avg sql.NullFloat64
	if err := rows.Scan(&g.Name, &g.Calls, &g.Errors, &avg); err != nil {
		return usage.GroupUsage{}, err
	}
	g.AvgMs = sessNullable(avg)
	return g, nil
}

// sessDetail builds the detail of one session with at most maxTurns turn
// points. The cap is a parameter so that tests can exercise truncation.
func (s *Store) sessDetail(ctx context.Context, sessionID string, maxTurns int) (usage.SessionDetail, error) {
	streams, err := sessCollect(ctx, s.db, "read session streams", sessScanStream, sessStreamsSQL, sessionID)
	if err != nil {
		return usage.SessionDetail{}, err
	}
	// Extension events without a session carry an empty session ID, so the
	// empty ID must never select a session.
	if sessionID == "" || len(streams) == 0 {
		return usage.SessionDetail{}, fmt.Errorf("session detail: %w", usage.ErrNotFound)
	}
	rows, err := sessCollect(ctx, s.db, "read session", sessScanRow,
		fmt.Sprintf(sessRowsTemplate, sessDetailCond, sessDetailToolCond, sessDetailIDs, sessDetailFilter), sessionID)
	if err != nil {
		return usage.SessionDetail{}, err
	}
	if len(rows) != 1 {
		return usage.SessionDetail{}, fmt.Errorf("session detail: %w", usage.ErrNotFound)
	}
	if err = s.sessFillModels(ctx, rows, sessAllFromMS, sessAllUntilMS); err != nil {
		return usage.SessionDetail{}, err
	}
	tools, err := sessCollect(ctx, s.db, "read session tool calls", sessScanTool, sessToolCallsSQL, sessionID)
	if err != nil {
		return usage.SessionDetail{}, err
	}
	models, err := sessCollect(ctx, s.db, "read session model calls", sessScanModel, sessModelCallsSQL, sessionID)
	if err != nil {
		return usage.SessionDetail{}, err
	}
	hooks, err := sessCollect(ctx, s.db, "read session hooks", sessScanHook, sessHooksSQL, sessionID, string(usage.HookError))
	if err != nil {
		return usage.SessionDetail{}, err
	}
	extSources, err := sessCollect(ctx, s.db, "read session extension sources", sessScanExtSource, sessExtSourcesSQL, sessionID, string(usage.ExtError))
	if err != nil {
		return usage.SessionDetail{}, err
	}
	acc := sessNewAcc(maxTurns)
	for _, c := range tools {
		acc.addTool(c)
	}
	for _, m := range models {
		acc.addModel(m)
	}
	return usage.SessionDetail{
		Session:        rows[0],
		Models:         acc.modelRows(),
		Tools:          acc.toolRows(),
		MCPServers:     sessGroupRows(acc.mcpServers),
		Skills:         sessGroupRows(acc.skills),
		Plugins:        sessGroupRows(acc.plugins),
		Hooks:          hooks,
		Agents:         acc.agentRows(streams),
		Turns:          acc.turns,
		TurnsTruncated: acc.truncated,
		ExtSources:     extSources,
	}, nil
}
