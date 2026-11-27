package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Projects aggregates activity by project for the query range. Excluded
// projects are listed with zero counts so the UI can re-include them.
// The Coverage field is left zero for the service to fill.
//
// Every stored project is listed, also one without activity in the range,
// ordered by total tokens descending and then by key. A range that does not
// start before it ends is usage.ErrBadRequest.
func (s *Store) Projects(ctx context.Context, q usage.Query) (usage.Projects, error) {
	sc, err := aggNewScope(q)
	if err != nil {
		return usage.Projects{}, err
	}
	items, err := s.aggProjectRows(ctx, sc)
	if err != nil {
		return usage.Projects{}, fmt.Errorf("query usage projects: %w", err)
	}
	return usage.Projects{Items: items}, nil
}

// aggProjectRows returns one row per stored project of the scope, ordered by
// total tokens descending and then by key. Counts cover the scope's range
// only; LastAt is the latest model call or prompt in it. An excluded project
// owns no rows (excluding deletes them), which is what makes its counts zero.
func (s *Store) aggProjectRows(ctx context.Context, sc aggScope) ([]usage.ProjectUsage, error) {
	query := `WITH calls AS (
		SELECT m.project_key AS k, SUM(m.input) AS input, SUM(m.output) AS output,
		       SUM(m.cache_read) AS cache_read, SUM(m.cache_create) AS cache_create, MAX(m.at_ms) AS last_ms
		FROM model_call m WHERE ` + aggInRange("m") + ` GROUP BY m.project_key
	), asks AS (
		SELECT p.project_key AS k, COUNT(*) AS n, MAX(p.at_ms) AS last_ms
		FROM prompt p WHERE ` + aggInRange("p") + ` GROUP BY p.project_key
	), active AS (
		SELECT k, COUNT(*) AS n FROM (
			SELECT m.project_key AS k, m.session_id FROM model_call m WHERE ` + aggInRange("m") + `
			UNION
			SELECT p.project_key, p.session_id FROM prompt p WHERE ` + aggInRange("p") + `
		) GROUP BY k
	)
	SELECT j.project_key, j.path, j.excluded, COALESCE(active.n, 0), COALESCE(asks.n, 0),
	       COALESCE(calls.input, 0), COALESCE(calls.output, 0), COALESCE(calls.cache_read, 0), COALESCE(calls.cache_create, 0),
	       MAX(COALESCE(calls.last_ms, 0), COALESCE(asks.last_ms, 0))
	FROM project j
	LEFT JOIN calls ON calls.k = j.project_key
	LEFT JOIN asks ON asks.k = j.project_key
	LEFT JOIN active ON active.k = j.project_key
	WHERE ` + aggInProject("j") + `
	ORDER BY COALESCE(calls.input, 0) + COALESCE(calls.output, 0) + COALESCE(calls.cache_read, 0) + COALESCE(calls.cache_create, 0) DESC,
	         j.project_key ASC`
	items := []usage.ProjectUsage{}
	err := s.aggQuery(ctx, query, sc.args(), func(rows *sql.Rows) error {
		var item usage.ProjectUsage
		var lastMS int64
		if err := rows.Scan(&item.Key, &item.Path, &item.Excluded, &item.Sessions, &item.Prompts,
			&item.Tokens.Input, &item.Tokens.Output, &item.Tokens.CacheRead, &item.Tokens.CacheCreate, &lastMS); err != nil {
			return err
		}
		item.LastAt = fromMS(lastMS)
		items = append(items, item)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}
