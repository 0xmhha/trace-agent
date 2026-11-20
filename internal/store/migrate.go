package store

import (
	"context"
	"database/sql"
	"strings"

	contract "github.com/0xmhha/trace-agent/projects/trace-agent"
)

// Take table definitions directly from the pinned contract to avoid schema drift.
func definitions(first, last string) string {
	ddl := contract.Schema
	return ddl[strings.Index(ddl, first):strings.Index(ddl, last)]
}

func migrate(ctx context.Context, tx *sql.Tx, version int) error {
	if version == 11 {
		if _, err := tx.ExecContext(ctx, definitions("CREATE TABLE agent_identity", "-- 작업 귀속 전에도")); err != nil {
			return err
		}
		fact := definitions("CREATE TABLE fact_version", "CREATE TABLE support")
		const cols = "task_id,fact_id,rev,occurred_ms,clock_key,kind,grade,subject_role,edge_code,value_code,numeric_value,agent_id,related_agent_id,attempt_id,related_attempt_id,plan_key,step_key,step_ordinal,related_module_id,module_id,reason_code,withdrawn"
		ddl := strings.Replace(fact, "CREATE TABLE fact_version", "CREATE TABLE migrated_fact_version", 1)
		// An old assignment lacks verified identity. Preserve its key, references and revision as historical evidence.
		if _, err := tx.ExecContext(ctx, ddl+"INSERT INTO migrated_fact_version ("+cols+") SELECT "+strings.Replace(cols, ",kind,", ",CASE kind WHEN 'assignment' THEN 'legacy_assignment' ELSE kind END,", 1)+" FROM fact_version; DROP TABLE fact_version; ALTER TABLE migrated_fact_version RENAME TO fact_version;"+definitions("CREATE INDEX fact_by_task_time", "-- 같은 리비전에")); err != nil {
			return err
		}
	}
	// #nosec G202 -- Only compile-time clauses and the embedded, pinned schema form this DDL.
	_, err := tx.ExecContext(ctx, `INSERT INTO revision(rev,committed_ms) VALUES(0,0) ON CONFLICT(rev) DO NOTHING;
 ALTER TABLE evidence ADD COLUMN observer_code TEXT CHECK(observer_code IN ('agent','model','tool','gateway','client','collector','unknown'));
 ALTER TABLE evidence ADD COLUMN adapter_version TEXT;`+definitions("CREATE TABLE source_model_observation", "CREATE TABLE body")+"PRAGMA user_version=13;")
	return err
}
