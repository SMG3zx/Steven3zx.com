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
