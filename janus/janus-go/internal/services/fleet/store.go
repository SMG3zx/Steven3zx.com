package fleet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	contractfleet "janus/internal/contract/fleet"
	"janus/internal/domain/deployments"
	"janus/internal/platform/tursodb"
)

// migrations is fleet.db's schema. Its tables have the columns janus.db's had,
// so `janus migrate split` copies rows as they are. An Owner, Project, Snapshot
// and Build are plain IDs here: they live in the other services. Times of Agents
// and runs are stored as Unix nanoseconds, so a comparison in SQL is a comparison
// of instants.
var migrations = []string{`
CREATE TABLE enrollment_codes (
	code_hash TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	used_at INTEGER
);
CREATE TABLE agents (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	name TEXT NOT NULL,
	tag TEXT NOT NULL,
	key_id TEXT NOT NULL UNIQUE,
	created_at INTEGER NOT NULL
);
CREATE INDEX agents_owner ON agents(owner_id, created_at, id);
CREATE TABLE run_usage (
	run_id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	deployment_id TEXT NOT NULL,
	fuel INTEGER NOT NULL,
	peak_memory_bytes INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	cpu_time_us INTEGER NOT NULL,
	network_bytes_in INTEGER NOT NULL,
	network_bytes_out INTEGER NOT NULL,
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	probe INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL
);
CREATE INDEX run_usage_owner ON run_usage(owner_id, started_at DESC, run_id DESC);
CREATE TABLE deployments (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	snapshot_id TEXT NOT NULL DEFAULT '',
	build_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	state TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	deploy_anyway INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX deployments_owner ON deployments(owner_id, created_at DESC);
CREATE INDEX deployments_project ON deployments(project_id, created_at DESC);
CREATE INDEX deployments_snapshot ON deployments(snapshot_id);
CREATE UNIQUE INDEX one_inflight_deployment_per_project ON deployments(project_id) WHERE state IN ('queued','starting');
`}

// inFlightError is what a new Deployment gets while another one of its Project
// is queued or starting.
var inFlightError = errors.New("a Deployment is already in flight for this Project")

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Service) insertCode(ctx context.Context, codeHash, owner string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO enrollment_codes (code_hash,owner_id,expires_at) VALUES (?,?,?)`,
		codeHash, owner, expires.UnixNano())
	return err
}

// useCode marks the code used and returns its owner, when the code
// exists, is unused and has not expired at now. Otherwise consumed is false.
func (s *Service) useCode(ctx context.Context, codeHash string, now time.Time) (owner string, consumed bool, err error) {
	err = tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE enrollment_codes SET used_at=? WHERE code_hash=? AND used_at IS NULL AND expires_at>?`,
			now.UnixNano(), codeHash, now.UnixNano())
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return nil
		}
		consumed = true
		return tx.QueryRowContext(ctx, `SELECT owner_id FROM enrollment_codes WHERE code_hash=?`, codeHash).Scan(&owner)
	})
	return owner, consumed, err
}

func (s *Service) insertAgent(ctx context.Context, agent contractfleet.AgentRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agents (id,owner_id,name,tag,key_id,created_at) VALUES (?,?,?,?,?,?)`,
		agent.ID, agent.OwnerID, agent.Name, agent.Tag, agent.KeyID, agent.CreatedAt.UnixNano())
	return err
}

const agentColumns = `id,owner_id,name,tag,key_id,created_at`

func scanAgent(row interface{ Scan(...any) error }) (contractfleet.AgentRecord, error) {
	var agent contractfleet.AgentRecord
	var created int64
	if err := row.Scan(&agent.ID, &agent.OwnerID, &agent.Name, &agent.Tag, &agent.KeyID, &created); err != nil {
		return contractfleet.AgentRecord{}, err
	}
	agent.CreatedAt = time.Unix(0, created).UTC()
	return agent, nil
}

// loadAgents returns the Owner's Agents, oldest first. An empty owner returns
// every Agent, oldest first.
func (s *Service) loadAgents(ctx context.Context, owner string) ([]contractfleet.AgentRecord, error) {
	query := `SELECT ` + agentColumns + ` FROM agents`
	args := []any{}
	if owner != "" {
		query += ` WHERE owner_id=?`
		args = append(args, owner)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at,id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := make([]contractfleet.AgentRecord, 0)
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

// loadAgent returns one of the Owner's Agents. An empty owner finds it whoever
// owns it.
func (s *Service) loadAgent(ctx context.Context, id, owner string) (contractfleet.AgentRecord, bool, error) {
	query := `SELECT ` + agentColumns + ` FROM agents WHERE id=?`
	args := []any{id}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	agent, err := scanAgent(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return contractfleet.AgentRecord{}, false, nil
	}
	return agent, err == nil, err
}

func (s *Service) removeAgentRow(ctx context.Context, id, owner string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE id=? AND owner_id=?`, id, owner)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

// Fuel and byte counts are uint64 in Go and INTEGER (int64) in SQLite. No run
// comes near 2^63, so the conversion is exact.

// insertRunUsage keeps one run an Agent reported. A run is kept once: recording
// a RunID again changes nothing.
func (s *Service) insertRunUsage(ctx context.Context, record contractfleet.RunUsageRecord) error {
	probe := 0
	if record.Probe {
		probe = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_usage (run_id,owner_id,agent_id,deployment_id,fuel,peak_memory_bytes,duration_ms,cpu_time_us,network_bytes_in,network_bytes_out,status,error,probe,started_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO NOTHING`,
		record.RunID, record.OwnerID, record.AgentID, record.DeploymentID, int64(record.Usage.Fuel), int64(record.Usage.PeakMemoryBytes),
		record.Usage.DurationMS, record.Usage.CPUTimeMicros, int64(record.NetworkBytesIn), int64(record.NetworkBytesOut),
		record.Status, record.Error, probe, record.StartedAt.UnixNano())
	return err
}

// loadRunUsage returns the Owner's runs that match query, newest first.
func (s *Service) loadRunUsage(ctx context.Context, query contractfleet.RunUsageQuery) ([]contractfleet.RunUsageRecord, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,owner_id,agent_id,deployment_id,fuel,peak_memory_bytes,duration_ms,cpu_time_us,network_bytes_in,network_bytes_out,status,error,probe,started_at
		FROM run_usage WHERE owner_id=? AND (?='' OR agent_id=?) AND (?='' OR deployment_id=?)
		ORDER BY started_at DESC, run_id DESC LIMIT ?`,
		query.OwnerID, query.AgentID, query.AgentID, query.DeploymentID, query.DeploymentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]contractfleet.RunUsageRecord, 0)
	for rows.Next() {
		var record contractfleet.RunUsageRecord
		var fuel, memory, in, out, started int64
		var probe int
		if err = rows.Scan(&record.RunID, &record.OwnerID, &record.AgentID, &record.DeploymentID, &fuel, &memory,
			&record.Usage.DurationMS, &record.Usage.CPUTimeMicros, &in, &out, &record.Status, &record.Error, &probe, &started); err != nil {
			return nil, err
		}
		record.Usage.Fuel, record.Usage.PeakMemoryBytes = uint64(fuel), uint64(memory)
		record.NetworkBytesIn, record.NetworkBytesOut = uint64(in), uint64(out)
		record.Probe = probe != 0
		record.StartedAt = time.Unix(0, started).UTC()
		records = append(records, record)
	}
	return records, rows.Err()
}

// deploymentRow is a Deployment as stored, with the Snapshot its Build was made
// from, which a snapshot's deletion finds it by.
type deploymentRow struct {
	contractfleet.Deployment
	OwnerID    string
	SnapshotID string
}

const deploymentColumns = `id,owner_id,project_id,snapshot_id,build_id,agent_id,state,reason,error,deploy_anyway,created_at,updated_at`

func scanDeployment(row interface{ Scan(...any) error }) (deploymentRow, error) {
	var d deploymentRow
	var anyway int
	if err := row.Scan(&d.ID, &d.OwnerID, &d.ProjectID, &d.SnapshotID, &d.BuildID, &d.AgentID, &d.State, &d.Reason, &d.Error, &anyway, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return deploymentRow{}, err
	}
	d.DeployAnyway = anyway != 0
	return d, nil
}

// insertDeployment stores a queued Deployment, unless another of its Project is
// still queued or starting.
func (s *Service) insertDeployment(ctx context.Context, owner string, d deploymentRow) error {
	return tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var inFlight int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE project_id=? AND state IN ('queued','starting')`,
			d.ProjectID).Scan(&inFlight); err != nil {
			return err
		}
		if inFlight > 0 {
			return inFlightError
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO deployments(id,owner_id,project_id,snapshot_id,build_id,agent_id,state,deploy_anyway)
			VALUES (?,?,?,?,?,?,'queued',?)`,
			d.ID, owner, d.ProjectID, d.SnapshotID, d.BuildID, d.AgentID, d.DeployAnyway)
		return err
	})
}

// moveDeployment moves a Deployment to a new state when the state machine
// allows it and the Deployment is still in the state the caller saw.
func (s *Service) moveDeployment(ctx context.Context, id, from, to, errText, reason string) error {
	if !deployments.CanMove(deployments.State(from), deployments.State(to)) {
		return fmt.Errorf("deployment cannot move from %s to %s", from, to)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE deployments SET state=?,error=?,reason=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND state=?`,
		to, errText, reason, id, from)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return nil
	}
	var exists int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE id=?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return errors.New("deployment not found")
	}
	return errors.New("deployment changed while it was being updated")
}

// loadDeployment returns one of the Owner's Deployments. An empty owner finds it
// whoever owns it.
func (s *Service) loadDeployment(ctx context.Context, id, owner string) (deploymentRow, bool, error) {
	query := `SELECT ` + deploymentColumns + ` FROM deployments WHERE id=?`
	args := []any{id}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	d, err := scanDeployment(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return deploymentRow{}, false, nil
	}
	return d, err == nil, err
}

// promoteDeployment makes a starting Deployment healthy and stops the Project's
// other healthy Deployments with the reason given, in one transaction, so the
// Project never has no live Deployment or two. It returns the Deployments it
// stopped, so their Agents can be told.
func (s *Service) promoteDeployment(ctx context.Context, id, owner, projectID, supersedes string) ([]deploymentRow, error) {
	var superseded []deploymentRow
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE owner_id=? AND project_id=? AND state=? AND id<>?`,
			owner, projectID, deployments.Healthy, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			other, err := scanDeployment(rows)
			if err != nil {
				rows.Close()
				return err
			}
			superseded = append(superseded, other)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		result, err := tx.ExecContext(ctx, `UPDATE deployments SET state=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=? AND project_id=? AND state=?`,
			deployments.Healthy, id, owner, projectID, deployments.Starting)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("deployment changed while it was being updated")
		}
		if len(superseded) == 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE deployments SET state=?,reason=?,updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND project_id=? AND state=? AND id<>?`,
			deployments.Stopped, supersedes, owner, projectID, deployments.Healthy, id)
		return err
	})
	return superseded, err
}

// listDeployments returns one Project's Deployments of the Owner, newest first.
func (s *Service) listDeployments(ctx context.Context, projectID, owner string) ([]deploymentRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE project_id=? AND owner_id=? ORDER BY created_at DESC, rowid DESC`, projectID, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var listed []deploymentRow
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		listed = append(listed, d)
	}
	return listed, rows.Err()
}

// deploymentsWhere returns the Deployments matching the column and value, the
// owner's Deployments when owner is set, newest first.
func (s *Service) deploymentsWhere(ctx context.Context, column, value, owner string) ([]deploymentRow, error) {
	query := `SELECT ` + deploymentColumns + ` FROM deployments WHERE ` + column + `=?`
	args := []any{value}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at DESC, rowid DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var listed []deploymentRow
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		listed = append(listed, d)
	}
	return listed, rows.Err()
}

// liveDeployments returns the Owner's Deployments that serve or are about to,
// so teardown can stop them.
func (s *Service) liveDeployments(ctx context.Context, owner string) ([]deploymentRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deploymentColumns+` FROM deployments
		WHERE owner_id=? AND state IN ('healthy','queued','starting') ORDER BY created_at, rowid`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var listed []deploymentRow
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		listed = append(listed, d)
	}
	return listed, rows.Err()
}

// deleteOwner removes everything the Owner has in fleet.db: its enrollment codes,
// Agents, runs and Deployments.
func (s *Service) deleteOwnerRows(ctx context.Context, owner string) error {
	return tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		for _, table := range []string{"enrollment_codes", "agents", "run_usage", "deployments"} {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE owner_id=?`, owner); err != nil {
				return fmt.Errorf("delete the %s of the Account: %w", table, err)
			}
		}
		return nil
	})
}

// Ensure the scanners satisfy the row interfaces they are used with.
var _ querier = (*sql.DB)(nil)

// deleteDeploymentsWhere deletes the Deployments whose column matches value, and
// returns how many it deleted.
func (s *Service) deleteDeploymentsWhere(ctx context.Context, column, value string) (int, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM deployments WHERE `+column+`=?`, value)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}
