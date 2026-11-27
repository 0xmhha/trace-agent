package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// ingestTable names one table whose rows carry project_key and the column
// Prune compares with its cut-off.
type ingestTable struct {
	name string
	// pruneCol is empty for a table that is never pruned by time.
	pruneCol string
}

// ingestTables lists every table that holds rows derived from transcripts.
// Clear, Prune and project exclusion all walk this list, so a new usage
// table is cleaned up everywhere once it is added here. File cursors are not
// pruned by time: without them a pruned range would be read again. The names
// are fixed here and never come from input, which is what makes it safe to
// build statements from them.
func ingestTables() []ingestTable {
	return []ingestTable{
		{name: "file"},
		{name: "stream", pruneCol: "last_ms"},
		{name: "model_call", pruneCol: "at_ms"},
		{name: "tool_call", pruneCol: "at_ms"},
		{name: "tool_result", pruneCol: "at_ms"},
		{name: "prompt", pruneCol: "at_ms"},
		{name: "hook_run", pruneCol: "at_ms"},
		{name: "turn_end", pruneCol: "at_ms"},
		{name: "compaction", pruneCol: "at_ms"},
		{name: "cost_state", pruneCol: "at_ms"},
	}
}

// Statements of Apply. They mirror seedBatch in testutil_test.go, which is
// the reference for the rows a batch produces.
const (
	// A batch read from offset zero restarts the file, so its skipped count
	// replaces the stored one instead of adding to it.
	ingestFileSQL = `INSERT INTO file(path,project_key,offset,size,mod_ms,skipped,unreadable,scanned_ms) VALUES(?1,?2,?3,?4,?5,?6,0,?7)
		ON CONFLICT(path) DO UPDATE SET offset=excluded.offset,size=excluded.size,mod_ms=excluded.mod_ms,
		 skipped=CASE WHEN ?8=0 THEN excluded.skipped ELSE file.skipped+excluded.skipped END,unreadable=0,scanned_ms=excluded.scanned_ms`
	ingestProjectSQL = `INSERT INTO project(project_key,path) VALUES(?,?)
		ON CONFLICT(project_key) DO UPDATE SET path=CASE WHEN excluded.path<>'' THEN excluded.path ELSE project.path END`
	// Empty attributes and zero times of a later batch never erase what an
	// earlier batch stored.
	ingestStreamSQL = `INSERT INTO stream(session_id,agent_id,project_key,file_path,agent_type,project_path,title,entrypoint,client_version,git_branch,first_ms,last_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id,agent_id) DO UPDATE SET
		 agent_type=CASE WHEN excluded.agent_type<>'' THEN excluded.agent_type ELSE stream.agent_type END,
		 project_path=CASE WHEN excluded.project_path<>'' THEN excluded.project_path ELSE stream.project_path END,
		 title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE stream.title END,
		 entrypoint=CASE WHEN excluded.entrypoint<>'' THEN excluded.entrypoint ELSE stream.entrypoint END,
		 client_version=CASE WHEN excluded.client_version<>'' THEN excluded.client_version ELSE stream.client_version END,
		 git_branch=CASE WHEN excluded.git_branch<>'' THEN excluded.git_branch ELSE stream.git_branch END,
		 first_ms=CASE WHEN excluded.first_ms=0 THEN stream.first_ms WHEN stream.first_ms=0 THEN excluded.first_ms ELSE MIN(stream.first_ms,excluded.first_ms) END,
		 last_ms=MAX(stream.last_ms,excluded.last_ms)`
	ingestModelCallSQL  = `INSERT OR REPLACE INTO model_call(session_id,agent_id,key,project_key,at_ms,model,input,output,cache_read,cache_create,thinking,stop_reason,skill,plugin) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	ingestToolCallSQL   = `INSERT OR REPLACE INTO tool_call(session_id,key,agent_id,project_key,at_ms,name,category,mcp_server,mcp_tool,plugin,skill,subagent_type,target,message_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	ingestToolResultSQL = `INSERT OR REPLACE INTO tool_result(session_id,key,project_key,at_ms,is_error,interrupted,duration_ms,sandbox_violations) VALUES(?,?,?,?,?,?,?,?)`
	ingestPromptSQL     = `INSERT OR REPLACE INTO prompt(session_id,key,agent_id,project_key,at_ms,source,chars) VALUES(?,?,?,?,?,?,?)`
	ingestHookSQL       = `INSERT OR REPLACE INTO hook_run(session_id,key,agent_id,project_key,at_ms,event,name,tool_use_key,status,exit_code,duration_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?)`
	ingestTurnSQL       = `INSERT OR REPLACE INTO turn_end(session_id,key,project_key,at_ms,duration_ms,messages) VALUES(?,?,?,?,?,?)`
	ingestCompactionSQL = `INSERT OR REPLACE INTO compaction(session_id,key,agent_id,project_key,at_ms,trigger_code,pre_tokens) VALUES(?,?,?,?,?,?,?)`
	// The running cost is cumulative, so only a state at least as new as the
	// stored one may replace it; a re-read of an older range must not. A cost
	// line carries no time of its own: when it is the first line of a batch
	// its time is zero, and it is then the latest line of the file, so it
	// replaces the stored state and keeps the stored time.
	ingestCostSQL = `INSERT INTO cost_state(session_id,project_key,at_ms,total_cost_usd,api_duration_ms,tool_duration_ms,lines_added,lines_removed) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET at_ms=CASE WHEN excluded.at_ms=0 THEN cost_state.at_ms ELSE excluded.at_ms END,total_cost_usd=excluded.total_cost_usd,api_duration_ms=excluded.api_duration_ms,tool_duration_ms=excluded.tool_duration_ms,lines_added=excluded.lines_added,lines_removed=excluded.lines_removed
		WHERE excluded.at_ms = 0 OR excluded.at_ms >= cost_state.at_ms`
)

// Statements of the cursor and clean-up methods.
const (
	ingestCursorSQL = `SELECT offset,size,mod_ms,skipped FROM file WHERE path = ?`
	ingestOffsetSQL = `SELECT offset FROM file WHERE path = ?`
	// The size and modification time of the last successful read are kept on
	// an existing row and left zero on a new one, so the file still looks
	// changed to the scanner and is tried again.
	ingestMarkSQL = `INSERT INTO file(path,project_key,offset,size,mod_ms,skipped,unreadable,scanned_ms) VALUES(?,?,0,0,0,0,1,?)
		ON CONFLICT(path) DO UPDATE SET unreadable=1,scanned_ms=excluded.scanned_ms WHERE file.unreadable=0`
	// Forgetting the last seen size and modification time as well makes every
	// tracked file look changed, whichever of the three the scanner compares.
	ingestResetSQL         = `UPDATE file SET offset=0,size=0,mod_ms=0 WHERE offset<>0 OR size<>0 OR mod_ms<>0`
	ingestExcludedSQL      = `SELECT excluded FROM project WHERE project_key = ?`
	ingestClearProjectsSQL = `DELETE FROM project WHERE excluded = 0`
	ingestClearExtSQL      = `DELETE FROM ext_event`
	ingestClearRejectSQL   = `DELETE FROM ext_reject`
	ingestPruneExtSQL      = `DELETE FROM ext_event WHERE received_ms < ?`
	ingestCountExtSQL      = `SELECT count(*) FROM ext_event`
	ingestCapExtSQL        = `DELETE FROM ext_event WHERE id IN (
		SELECT id FROM ext_event ORDER BY received_ms DESC, at_ms DESC, id DESC LIMIT -1 OFFSET ?)`
)

// Operation names used as the prefix of returned errors.
const (
	ingestOpCursor = "read file cursor"
	ingestOpApply  = "apply usage batch"
	ingestOpMark   = "mark file unreadable"
	ingestOpClear  = "clear usage data"
	ingestOpPrune  = "prune usage data"
	ingestOpReset  = "reset file cursors"
)

// ingestInTx runs fn in one write transaction. It commits when fn returns
// nil and rolls back otherwise, so a failed or refused write leaves nothing
// behind.
func (s *Store) ingestInTx(ctx context.Context, op string, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// ingestExec runs one statement and returns the number of rows it changed.
func ingestExec(ctx context.Context, tx *sql.Tx, query string, args ...any) (int64, error) {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ingestEach runs query once per row with a single prepared statement.
func ingestEach[T any](ctx context.Context, tx *sql.Tx, query string, rows []T, args func(T) []any) error {
	if len(rows) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, row := range rows {
		if _, err = stmt.ExecContext(ctx, args(row)...); err != nil {
			return err
		}
	}
	return nil
}

// ingestFlag stores a boolean in the 0/1 form of the schema.
func ingestFlag(v bool) int {
	if v {
		return 1
	}
	return 0
}

// ingestCollecting reports whether rows of project may be written: consent
// is granted and the project is not excluded. It runs inside the write
// transaction, so an exclusion or a withdrawal cannot slip in between the
// check and the write.
func ingestCollecting(ctx context.Context, tx *sql.Tx, project string) (bool, error) {
	consent, _, err := readSetting(ctx, tx, settingConsent)
	if err != nil {
		return false, err
	}
	if consent != settingOn {
		return false, nil
	}
	var excluded int
	err = tx.QueryRowContext(ctx, ingestExcludedSQL, project).Scan(&excluded)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read project exclusion: %w", err)
	}
	return excluded == 0, nil
}

// ingestStale reports whether a batch read from base no longer matches the
// stored state of its file.
func ingestStale(ctx context.Context, tx *sql.Tx, file usage.FileRef, base int64) (bool, error) {
	collecting, err := ingestCollecting(ctx, tx, file.ProjectKey)
	if err != nil || !collecting {
		return true, err
	}
	if base == 0 {
		return false, nil
	}
	var offset int64
	err = tx.QueryRowContext(ctx, ingestOffsetSQL, file.Path).Scan(&offset)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return true, fmt.Errorf("read file cursor: %w", err)
	}
	return base != offset, nil
}

// ingestRootPath is the project path a batch contributes: only the root
// transcript names the project directory.
func ingestRootPath(sess usage.SessionPatch) string {
	if sess.AgentID != "" {
		return ""
	}
	return sess.ProjectPath
}

// ingestRootCost returns the cost state a batch may store. A sub-agent
// transcript never carries the session total.
func ingestRootCost(batch usage.Batch) *usage.CostState {
	if batch.Session.AgentID != "" {
		return nil
	}
	return batch.Cost
}

// ingestHasData reports whether batch holds at least one record or session
// attribute. A batch without either only moves the cursor, and readers have
// nothing to refetch for it.
func ingestHasData(batch usage.Batch) bool {
	records := len(batch.ModelCalls) + len(batch.ToolCalls) + len(batch.ToolResults) + len(batch.Prompts) +
		len(batch.Hooks) + len(batch.Turns) + len(batch.Compactions)
	if records > 0 || ingestRootCost(batch) != nil {
		return true
	}
	sess := batch.Session
	labels := sess.AgentType + sess.ProjectPath + sess.Title + sess.Entrypoint + sess.ClientVersion + sess.GitBranch
	return labels != "" || !sess.FirstAt.IsZero() || !sess.LastAt.IsZero()
}

// ingestValidate refuses a batch that cannot describe a range of a file.
func ingestValidate(file usage.FileRef, batch usage.Batch) error {
	if file.Path == "" || file.ProjectKey == "" {
		return fmt.Errorf("file path and project key are required: %w", usage.ErrBadRequest)
	}
	if batch.BaseOffset < 0 || batch.NextOffset < batch.BaseOffset || batch.Skipped < 0 {
		return fmt.Errorf("batch offsets out of order: %w", usage.ErrBadRequest)
	}
	return nil
}

// Cursor returns the stored read position of a transcript file. ok is false
// when the file has not been scanned yet.
func (s *Store) Cursor(ctx context.Context, path string) (cursor usage.FileCursor, ok bool, err error) {
	var modMS int64
	err = s.db.QueryRowContext(ctx, ingestCursorSQL, path).Scan(&cursor.Offset, &cursor.Size, &modMS, &cursor.Skipped)
	if errors.Is(err, sql.ErrNoRows) {
		return usage.FileCursor{}, false, nil
	}
	if err != nil {
		return usage.FileCursor{}, false, fmt.Errorf("%s: %w", ingestOpCursor, err)
	}
	cursor.ModTime = fromMS(modMS)
	return cursor, true, nil
}

// Apply stores one batch and advances the file cursor to batch.NextOffset in
// a single transaction. It returns usage.ErrStale and stores nothing when
// consent is not granted, when the file's project is excluded, or when
// batch.BaseOffset is neither zero nor
// the stored cursor offset (zero when the file has no cursor). A batch with
// BaseOffset zero restarts the file: its Skipped replaces the stored count;
// otherwise Skipped is added. Zero session times never overwrite stored
// ones, batch.Cost is stored only for the root transcript, and data_version
// advances only when the batch holds at least one record or session
// attribute. The rows written match seedBatch in testutil_test.go.
func (s *Store) Apply(ctx context.Context, file usage.FileRef, batch usage.Batch) error {
	if err := ingestValidate(file, batch); err != nil {
		return fmt.Errorf("%s: %w", ingestOpApply, err)
	}
	return s.ingestInTx(ctx, ingestOpApply, func(tx *sql.Tx) error {
		stale, err := ingestStale(ctx, tx, file, batch.BaseOffset)
		if err != nil {
			return err
		}
		if stale {
			return usage.ErrStale
		}
		if err = s.ingestWrite(ctx, tx, file, batch); err != nil {
			return err
		}
		if !ingestHasData(batch) {
			return nil
		}
		return bump(ctx, tx)
	})
}

// ingestWrite writes the cursor, the project, the stream and every record of
// batch.
func (s *Store) ingestWrite(ctx context.Context, tx *sql.Tx, file usage.FileRef, batch usage.Batch) error {
	project := file.ProjectKey
	sess := batch.Session
	if _, err := tx.ExecContext(ctx, ingestFileSQL, file.Path, project, batch.NextOffset, file.Size, toMS(file.ModTime),
		batch.Skipped, toMS(s.now()), batch.BaseOffset); err != nil {
		return fmt.Errorf("write file cursor: %w", err)
	}
	if _, err := tx.ExecContext(ctx, ingestProjectSQL, project, ingestRootPath(sess)); err != nil {
		return fmt.Errorf("write project: %w", err)
	}
	if sess.SessionID != "" {
		if _, err := tx.ExecContext(ctx, ingestStreamSQL, sess.SessionID, sess.AgentID, project, file.Path, sess.AgentType,
			sess.ProjectPath, sess.Title, sess.Entrypoint, sess.ClientVersion, sess.GitBranch, toMS(sess.FirstAt), toMS(sess.LastAt)); err != nil {
			return fmt.Errorf("write stream: %w", err)
		}
	}
	if err := ingestRecords(ctx, tx, project, batch); err != nil {
		return err
	}
	cost := ingestRootCost(batch)
	if cost == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, ingestCostSQL, cost.SessionID, project, toMS(cost.At), cost.TotalCostUSD,
		cost.APIDurationMs, cost.ToolDurationMs, cost.LinesAdded, cost.LinesRemoved); err != nil {
		return fmt.Errorf("write cost state: %w", err)
	}
	return nil
}

// ingestRecords writes the keyed records of batch. Each one replaces the row
// with the same natural key, which makes re-reading a range idempotent.
func ingestRecords(ctx context.Context, tx *sql.Tx, project string, batch usage.Batch) error {
	if err := ingestEach(ctx, tx, ingestModelCallSQL, batch.ModelCalls, func(m usage.ModelCall) []any {
		return []any{m.SessionID, m.AgentID, m.Key, project, toMS(m.At), m.Model, m.Input, m.Output, m.CacheRead,
			m.CacheCreate, m.Thinking, m.StopReason, m.Skill, m.Plugin}
	}); err != nil {
		return fmt.Errorf("write model calls: %w", err)
	}
	if err := ingestEach(ctx, tx, ingestToolCallSQL, batch.ToolCalls, func(c usage.ToolCall) []any {
		return []any{c.SessionID, c.Key, c.AgentID, project, toMS(c.At), c.Name, string(c.Category), c.MCPServer,
			c.MCPTool, c.Plugin, c.Skill, c.SubagentType, c.Target, c.MessageKey}
	}); err != nil {
		return fmt.Errorf("write tool calls: %w", err)
	}
	if err := ingestEach(ctx, tx, ingestToolResultSQL, batch.ToolResults, func(r usage.ToolResult) []any {
		return []any{r.SessionID, r.Key, project, toMS(r.At), ingestFlag(r.IsError), ingestFlag(r.Interrupted),
			r.DurationMs, r.SandboxViolations}
	}); err != nil {
		return fmt.Errorf("write tool results: %w", err)
	}
	if err := ingestEach(ctx, tx, ingestPromptSQL, batch.Prompts, func(p usage.Prompt) []any {
		return []any{p.SessionID, p.Key, p.AgentID, project, toMS(p.At), p.Source, p.Chars}
	}); err != nil {
		return fmt.Errorf("write prompts: %w", err)
	}
	if err := ingestEach(ctx, tx, ingestHookSQL, batch.Hooks, func(h usage.HookRun) []any {
		return []any{h.SessionID, h.Key, h.AgentID, project, toMS(h.At), h.Event, h.Name, h.ToolUseKey,
			string(h.Status), h.ExitCode, h.DurationMs}
	}); err != nil {
		return fmt.Errorf("write hook runs: %w", err)
	}
	if err := ingestEach(ctx, tx, ingestTurnSQL, batch.Turns, func(e usage.TurnEnd) []any {
		return []any{e.SessionID, e.Key, project, toMS(e.At), e.DurationMs, e.Messages}
	}); err != nil {
		return fmt.Errorf("write turn ends: %w", err)
	}
	if err := ingestEach(ctx, tx, ingestCompactionSQL, batch.Compactions, func(c usage.Compaction) []any {
		return []any{c.SessionID, c.Key, c.AgentID, project, toMS(c.At), c.Trigger, c.PreTokens}
	}); err != nil {
		return fmt.Errorf("write compactions: %w", err)
	}
	return nil
}

// MarkUnreadable records that the last scan could not read the file, so that
// coverage reports it instead of silently showing less data. The flag is
// cleared by the next successful Apply. data_version advances only when the
// flag changes.
func (s *Store) MarkUnreadable(ctx context.Context, file usage.FileRef) error {
	if file.Path == "" || file.ProjectKey == "" {
		return fmt.Errorf("%s: file path and project key are required: %w", ingestOpMark, usage.ErrBadRequest)
	}
	return s.ingestInTx(ctx, ingestOpMark, func(tx *sql.Tx) error {
		// A scan that was overtaken by an exclusion or a withdrawal must not
		// bring back a cursor that was just deleted; nothing would clear it.
		collecting, err := ingestCollecting(ctx, tx, file.ProjectKey)
		if err != nil || !collecting {
			return err
		}
		changed, err := ingestExec(ctx, tx, ingestMarkSQL, file.Path, file.ProjectKey, toMS(s.now()))
		if err != nil || changed == 0 {
			return err
		}
		return bump(ctx, tx)
	})
}

// Clear deletes every usage row, extension event, rejection counter and file
// cursor. Settings and the excluded-project list are kept. Extension events
// cannot be rebuilt afterwards.
func (s *Store) Clear(ctx context.Context) error {
	return s.ingestInTx(ctx, ingestOpClear, func(tx *sql.Tx) error {
		var deleted int64
		for _, table := range ingestTables() {
			n, err := ingestExec(ctx, tx, "DELETE FROM "+table.name)
			if err != nil {
				return err
			}
			deleted += n
		}
		for _, query := range []string{ingestClearExtSQL, ingestClearRejectSQL, ingestClearProjectsSQL} {
			n, err := ingestExec(ctx, tx, query)
			if err != nil {
				return err
			}
			deleted += n
		}
		if deleted == 0 {
			return nil
		}
		return bump(ctx, tx)
	})
}

// Prune deletes usage rows older than usageBefore and extension events older
// than extBefore, then drops the oldest extension events beyond
// usage.MaxExtEvents. A zero time skips that part.
func (s *Store) Prune(ctx context.Context, usageBefore, extBefore time.Time) error {
	return s.ingestPrune(ctx, usageBefore, extBefore, usage.MaxExtEvents)
}

// ingestPrune is Prune with the extension event cap as a parameter, so that
// a test can exercise the cap without storing usage.MaxExtEvents rows.
func (s *Store) ingestPrune(ctx context.Context, usageBefore, extBefore time.Time, extCap int64) error {
	return s.ingestInTx(ctx, ingestOpPrune, func(tx *sql.Tx) error {
		var deleted int64
		if !usageBefore.IsZero() {
			for _, table := range ingestTables() {
				if table.pruneCol == "" {
					continue
				}
				n, err := ingestExec(ctx, tx, "DELETE FROM "+table.name+" WHERE "+table.pruneCol+" < ?", toMS(usageBefore))
				if err != nil {
					return err
				}
				deleted += n
			}
		}
		if !extBefore.IsZero() {
			n, err := ingestExec(ctx, tx, ingestPruneExtSQL, toMS(extBefore))
			if err != nil {
				return err
			}
			deleted += n
		}
		n, err := ingestCapExt(ctx, tx, extCap)
		if err != nil {
			return err
		}
		if deleted += n; deleted == 0 {
			return nil
		}
		return bump(ctx, tx)
	})
}

// ingestCapExt drops the oldest extension events beyond extCap. It counts
// first because the common case is a table under the cap, which then costs
// no sort.
func ingestCapExt(ctx context.Context, tx *sql.Tx, extCap int64) (int64, error) {
	var stored int64
	if err := tx.QueryRowContext(ctx, ingestCountExtSQL).Scan(&stored); err != nil {
		return 0, fmt.Errorf("count extension events: %w", err)
	}
	if stored <= max(extCap, 0) {
		return 0, nil
	}
	return ingestExec(ctx, tx, ingestCapExtSQL, max(extCap, 0))
}

// ResetCursors sets every file cursor back to offset zero so that the next
// scan re-reads all tracked files. Stored rows stay; re-reading is idempotent.
func (s *Store) ResetCursors(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, ingestResetSQL); err != nil {
		return fmt.Errorf("%s: %w", ingestOpReset, err)
	}
	return nil
}
