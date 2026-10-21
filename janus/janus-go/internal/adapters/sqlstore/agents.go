package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"janus/internal/app"
)

// Times are stored as integer Unix nanoseconds, so a comparison in SQL is a
// comparison of instants.

func (s *Store) CreateEnrollmentCode(ctx context.Context, code app.CreateEnrollmentCode) error {
	ctx, done := s.start(ctx, "CreateEnrollmentCode")
	_, err := s.db.ExecContext(ctx, `INSERT INTO enrollment_codes (code_hash,owner_id,expires_at) VALUES (?,?,?)`,
		code.CodeHash, code.OwnerID, code.ExpiresAt.UnixNano())
	return done(err)
}

func (s *Store) ConsumeEnrollmentCode(ctx context.Context, codeHash string, now time.Time) (ownerID string, consumed bool, err error) {
	ctx, done := s.start(ctx, "ConsumeEnrollmentCode")
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE enrollment_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`,
			now.UnixNano(), codeHash, now.UnixNano())
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return nil
		}
		consumed = true
		return tx.QueryRowContext(ctx, `SELECT owner_id FROM enrollment_codes WHERE code_hash=?`, codeHash).Scan(&ownerID)
	})
	if err != nil {
		return "", false, done(err)
	}
	return ownerID, consumed, done(nil)
}

func (s *Store) CreateAgent(ctx context.Context, agent app.CreateAgent) error {
	ctx, done := s.start(ctx, "CreateAgent")
	_, err := s.db.ExecContext(ctx, `INSERT INTO agents (id,owner_id,name,tag,key_id,created_at) VALUES (?,?,?,?,?,?)`,
		agent.ID, agent.OwnerID, agent.Name, agent.Tag, agent.KeyID, agent.CreatedAt.UnixNano())
	return done(err)
}

const agentColumns = `id,owner_id,name,tag,key_id,created_at`

func scanAgent(row interface{ Scan(...any) error }) (app.AgentRecord, error) {
	var agent app.AgentRecord
	var created int64
	if err := row.Scan(&agent.ID, &agent.OwnerID, &agent.Name, &agent.Tag, &agent.KeyID, &created); err != nil {
		return app.AgentRecord{}, err
	}
	agent.CreatedAt = time.Unix(0, created).UTC()
	return agent, nil
}

func (s *Store) ListAgents(ctx context.Context, ownerID string) ([]app.AgentRecord, error) {
	ctx, done := s.start(ctx, "ListAgents")
	agents := make([]app.AgentRecord, 0)
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents WHERE owner_id=? ORDER BY created_at,id`, ownerID)
	if err != nil {
		return nil, done(err)
	}
	defer rows.Close()
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, done(err)
		}
		agents = append(agents, agent)
	}
	if err = rows.Err(); err != nil {
		return nil, done(err)
	}
	return agents, done(nil)
}

func (s *Store) FindAgent(ctx context.Context, id, ownerID string) (app.AgentRecord, bool, error) {
	ctx, done := s.start(ctx, "FindAgent")
	agent, err := scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agents WHERE id=? AND owner_id=?`, id, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return app.AgentRecord{}, false, done(nil)
	}
	if err != nil {
		return app.AgentRecord{}, false, done(err)
	}
	return agent, true, done(nil)
}

func (s *Store) DeleteAgent(ctx context.Context, id, ownerID string) (bool, error) {
	ctx, done := s.start(ctx, "DeleteAgent")
	result, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id=? AND owner_id=?`, id, ownerID)
	if err != nil {
		return false, done(err)
	}
	count, _ := result.RowsAffected()
	return count == 1, done(nil)
}

func (s *Store) ListAllAgents(ctx context.Context) ([]app.AgentRecord, error) {
	ctx, done := s.start(ctx, "ListAllAgents")
	agents := make([]app.AgentRecord, 0)
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents ORDER BY created_at,id`)
	if err != nil {
		return nil, done(err)
	}
	defer rows.Close()
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, done(err)
		}
		agents = append(agents, agent)
	}
	if err = rows.Err(); err != nil {
		return nil, done(err)
	}
	return agents, done(nil)
}

// Fuel and byte counts are uint64 in Go and INTEGER (int64) in SQLite. No run
// comes near 2^63, so the conversion is exact.

func (s *Store) RecordRunUsage(ctx context.Context, record app.RunUsageRecord) error {
	ctx, done := s.start(ctx, "RecordRunUsage")
	probe := 0
	if record.Probe {
		probe = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_usage (run_id,owner_id,agent_id,deployment_id,fuel,peak_memory_bytes,duration_ms,cpu_time_us,network_bytes_in,network_bytes_out,status,error,probe,started_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`,
		record.RunID, record.OwnerID, record.AgentID, record.DeploymentID, int64(record.Usage.Fuel), int64(record.Usage.PeakMemoryBytes),
		record.Usage.DurationMS, record.Usage.CPUTimeMicros, int64(record.NetworkBytesIn), int64(record.NetworkBytesOut),
		record.Status, record.Error, probe, record.StartedAt.UnixNano())
	return done(err)
}

func (s *Store) ListRunUsage(ctx context.Context, query app.RunUsageQuery) ([]app.RunUsageRecord, error) {
	ctx, done := s.start(ctx, "ListRunUsage")
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,owner_id,agent_id,deployment_id,fuel,peak_memory_bytes,duration_ms,cpu_time_us,network_bytes_in,network_bytes_out,status,error,probe,started_at
		FROM run_usage WHERE owner_id=? AND (?='' OR agent_id=?) AND (?='' OR deployment_id=?)
		ORDER BY started_at DESC, run_id DESC LIMIT ?`,
		query.OwnerID, query.AgentID, query.AgentID, query.DeploymentID, query.DeploymentID, limit)
	if err != nil {
		return nil, done(err)
	}
	defer rows.Close()
	records := make([]app.RunUsageRecord, 0)
	for rows.Next() {
		var record app.RunUsageRecord
		var fuel, memory, in, out, started int64
		var probe int
		if err = rows.Scan(&record.RunID, &record.OwnerID, &record.AgentID, &record.DeploymentID, &fuel, &memory,
			&record.Usage.DurationMS, &record.Usage.CPUTimeMicros, &in, &out, &record.Status, &record.Error, &probe, &started); err != nil {
			return nil, done(err)
		}
		record.Usage.Fuel, record.Usage.PeakMemoryBytes = uint64(fuel), uint64(memory)
		record.NetworkBytesIn, record.NetworkBytesOut = uint64(in), uint64(out)
		record.Probe = probe != 0
		record.StartedAt = time.Unix(0, started).UTC()
		records = append(records, record)
	}
	if err = rows.Err(); err != nil {
		return nil, done(err)
	}
	return records, done(nil)
}
