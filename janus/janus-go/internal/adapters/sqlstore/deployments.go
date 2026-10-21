package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"janus/internal/app"
	"janus/internal/domain/deployments"
)

// inFlightError is what CreateDeployment gives while another Deployment of the
// Project is queued or starting. The memory store gives the same words.
var inFlightError = errors.New("a Deployment is already in flight for this Project")

// StartCheck records that the checks of a completed WASM Build have started.
func (s *Store) StartCheck(ctx context.Context, check app.StartCheck) error {
	ctx, done := s.start(ctx, "StartCheck")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO project_checks(build_id,owner_id,status)
			SELECT id,owner_id,? FROM project_wasm_builds WHERE id=? AND status='complete'`, app.CheckRunning, check.BuildID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return errors.New("WASM build not found")
		}
		return nil
	}))
}

// FinishCheck records how the running checks of a WASM Build ended.
func (s *Store) FinishCheck(ctx context.Context, check app.FinishCheck) error {
	ctx, done := s.start(ctx, "FinishCheck")
	if check.Status != app.CheckPassed && check.Status != app.CheckFailed {
		return done(fmt.Errorf("check cannot finish as %s", check.Status))
	}
	result, err := s.db.ExecContext(ctx, `UPDATE project_checks SET status=?,error=?,check_log=?,no_tests=?,updated_at=CURRENT_TIMESTAMP
		WHERE build_id=? AND status=?`, check.Status, check.Error, check.Log, check.NoTests, check.BuildID, app.CheckRunning)
	if err == nil {
		if rows, _ := result.RowsAffected(); rows != 1 {
			err = errors.New("check not found")
		}
	}
	return done(err)
}

// FindCheck returns the Check of one of an Account's WASM Builds.
func (s *Store) FindCheck(ctx context.Context, buildID, ownerID string) (check app.Check, found bool, err error) {
	ctx, done := s.start(ctx, "FindCheck")
	var noTests int
	err = s.db.QueryRowContext(ctx, `SELECT build_id,status,error,check_log,no_tests FROM project_checks WHERE build_id=? AND owner_id=?`,
		buildID, ownerID).Scan(&check.BuildID, &check.Status, &check.Error, &check.Log, &noTests)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Check{}, false, done(nil)
	}
	check.NoTests = noTests != 0
	return check, err == nil, done(err)
}

// CreateDeployment queues a Deployment of a WASM Build on one of the Owner's
// Agents, unless another Deployment of the Project is still in flight.
func (s *Store) CreateDeployment(ctx context.Context, deployment app.CreateDeployment) error {
	ctx, done := s.start(ctx, "CreateDeployment")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		var inFlight int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE project_id=? AND state IN ('queued','starting')`,
			deployment.ProjectID).Scan(&inFlight); err != nil {
			return err
		}
		if inFlight > 0 {
			return inFlightError
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO deployments(id,owner_id,project_id,build_id,agent_id,state,deploy_anyway)
			SELECT ?,b.owner_id,b.project_id,b.id,a.id,'queued',? FROM project_wasm_builds b JOIN agents a ON a.owner_id=b.owner_id
			WHERE b.id=? AND b.owner_id=? AND b.project_id=? AND a.id=?`,
			deployment.ID, deployment.DeployAnyway, deployment.BuildID, deployment.OwnerID, deployment.ProjectID, deployment.AgentID)
		if err != nil {
			return err
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return errors.New("WASM build or Agent not found")
		}
		return nil
	}))
}

// TransitionDeployment moves a Deployment to a new state when the state
// machine allows it and the Deployment is still in the state the caller saw.
func (s *Store) TransitionDeployment(ctx context.Context, t app.DeploymentTransition) error {
	ctx, done := s.start(ctx, "TransitionDeployment")
	if !deployments.CanMove(deployments.State(t.From), deployments.State(t.To)) {
		return done(fmt.Errorf("deployment cannot move from %s to %s", t.From, t.To))
	}
	result, err := s.db.ExecContext(ctx, `UPDATE deployments SET state=?,error=?,reason=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND state=?`,
		t.To, t.Error, t.Reason, t.ID, t.From)
	if err != nil {
		return done(err)
	}
	if rows, _ := result.RowsAffected(); rows == 1 {
		return done(nil)
	}
	var exists int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE id=?`, t.ID).Scan(&exists); err != nil {
		return done(err)
	}
	if exists == 0 {
		return done(errors.New("deployment not found"))
	}
	return done(errors.New("deployment changed while it was being updated"))
}

// FindDeployment returns one of an Account's Deployments.
func (s *Store) FindDeployment(ctx context.Context, id, ownerID string) (deployment app.Deployment, found bool, err error) {
	ctx, done := s.start(ctx, "FindDeployment")
	var anyway int
	err = s.db.QueryRowContext(ctx, `SELECT id,project_id,build_id,agent_id,state,reason,error,deploy_anyway,created_at,updated_at FROM deployments WHERE id=? AND owner_id=?`,
		id, ownerID).Scan(&deployment.ID, &deployment.ProjectID, &deployment.BuildID, &deployment.AgentID, &deployment.State, &deployment.Reason, &deployment.Error, &anyway, &deployment.CreatedAt, &deployment.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return app.Deployment{}, false, done(nil)
	}
	deployment.DeployAnyway = anyway != 0
	return deployment, err == nil, done(err)
}

// PromoteDeployment makes a starting Deployment healthy and stops the Project's
// other healthy Deployments with the reason given, in one transaction, so the
// Project never has no live Deployment or two.
func (s *Store) PromoteDeployment(ctx context.Context, promotion app.PromoteDeployment) ([]app.Deployment, error) {
	ctx, done := s.start(ctx, "PromoteDeployment")
	var superseded []app.Deployment
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,project_id,agent_id,state FROM deployments WHERE owner_id=? AND project_id=? AND state=? AND id<>?`,
			promotion.OwnerID, promotion.ProjectID, deployments.Healthy, promotion.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var other app.Deployment
			if err = rows.Scan(&other.ID, &other.ProjectID, &other.AgentID, &other.State); err != nil {
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
			deployments.Healthy, promotion.ID, promotion.OwnerID, promotion.ProjectID, deployments.Starting)
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
			deployments.Stopped, promotion.Supersedes, promotion.OwnerID, promotion.ProjectID, deployments.Healthy, promotion.ID)
		return err
	})
	return superseded, done(err)
}

// ListDeployments returns one Project's Deployments of an Account, newest first.
func (s *Store) ListDeployments(ctx context.Context, projectID, ownerID string) ([]app.Deployment, error) {
	ctx, done := s.start(ctx, "ListDeployments")
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_id,build_id,agent_id,state,reason,error,deploy_anyway,created_at,updated_at FROM deployments
		WHERE project_id=? AND owner_id=? ORDER BY created_at DESC, rowid DESC`, projectID, ownerID)
	if err != nil {
		return nil, done(err)
	}
	defer rows.Close()
	var listed []app.Deployment
	for rows.Next() {
		var deployment app.Deployment
		var anyway int
		if err = rows.Scan(&deployment.ID, &deployment.ProjectID, &deployment.BuildID, &deployment.AgentID, &deployment.State, &deployment.Reason, &deployment.Error, &anyway, &deployment.CreatedAt, &deployment.UpdatedAt); err != nil {
			return nil, done(err)
		}
		deployment.DeployAnyway = anyway != 0
		listed = append(listed, deployment)
	}
	return listed, done(rows.Err())
}
