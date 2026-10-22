package builds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	contractbuilds "janus/internal/contract/builds"
	"janus/internal/domain/jobs"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// migrations is builds.db's schema. Its tables have the columns janus.db's had,
// . An Owner, Project and
// Snapshot are plain IDs here: they live in the other services.
var migrations = []string{`
CREATE TABLE project_wasm_builds (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	project_id TEXT NOT NULL,
	snapshot_id TEXT NOT NULL,
	target TEXT NOT NULL,
	status TEXT NOT NULL,
	progress INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	artifact_name TEXT NOT NULL DEFAULT '',
	artifact BLOB,
	build_log TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX wasm_builds_owner ON project_wasm_builds(owner_id, created_at DESC);
CREATE INDEX wasm_builds_project ON project_wasm_builds(project_id);
CREATE INDEX wasm_builds_snapshot ON project_wasm_builds(snapshot_id);
CREATE UNIQUE INDEX one_active_wasm_build_per_project ON project_wasm_builds(project_id) WHERE status IN ('queued','building');
CREATE TABLE project_checks (
	build_id TEXT PRIMARY KEY REFERENCES project_wasm_builds(id) ON DELETE CASCADE,
	owner_id TEXT NOT NULL,
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	check_log TEXT NOT NULL DEFAULT '',
	no_tests INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
` + outbox.Schema}

// Check statuses. A Check is running until its commands end, then passed or failed.
const (
	checkRunning = "running"
	checkPassed  = "passed"
	checkFailed  = "failed"
)

const buildColumns = `id,owner_id,project_id,snapshot_id,target,status,progress,error,artifact_name,build_log,created_at,updated_at`

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func scanBuild(row interface{ Scan(...any) error }) (contractbuilds.Build, error) {
	var b contractbuilds.Build
	err := row.Scan(&b.ID, &b.OwnerID, &b.ProjectID, &b.SnapshotID, &b.Target, &b.Status, &b.Progress, &b.Error, &b.ArtifactName, &b.Log, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

// getBuild returns a Build. When owner is empty the Build is found whoever owns it.
func getBuild(ctx context.Context, q querier, id, owner string) (contractbuilds.Build, bool, error) {
	query := `SELECT ` + buildColumns + ` FROM project_wasm_builds WHERE id=?`
	args := []any{id}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	build, err := scanBuild(q.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return contractbuilds.Build{}, false, nil
	}
	return build, err == nil, err
}

// insertBuild stores a queued Build, with its job and its event, in tx.
func insertBuild(ctx context.Context, tx *sql.Tx, request contractbuilds.RequestBuild, snapshotID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO project_wasm_builds(id,owner_id,project_id,snapshot_id,target,status) VALUES(?,?,?,?,?,?)`,
		request.ID, request.OwnerID, request.ProjectID, snapshotID, request.Target, contractbuilds.StatusQueued)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return conflict("two WASM builds are already running; retry when one finishes")
		}
		return err
	}
	if err := addEvent(ctx, tx, contractbuilds.SubjectBuildJob, "build:"+request.ID, contractbuilds.BuildJobMessage{JobID: request.ID}); err != nil {
		return err
	}
	return buildChanged(ctx, tx, request.ID)
}

// transitionBuild moves a Build from the status the caller saw to another, when
// domain/jobs allows it and nobody changed it meanwhile. A move within the same
// status only reports progress, so it publishes nothing.
func transitionBuild(ctx context.Context, db *sql.DB, id, from, to string, progress int, reason, log string) error {
	if !jobs.WASM.CanMove(from, to) {
		return fmt.Errorf("WASM build cannot move from %s to %s", from, to)
	}
	return tursodb.InTx(ctx, db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE project_wasm_builds SET status=?,progress=?,error=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`,
			to, progress, reason, log, id, from)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("WASM build changed while it was being updated")
		}
		if from == to {
			return nil
		}
		return buildChanged(ctx, tx, id)
	})
}

// completeBuild stores a finished Build's Artifact and log, and marks it complete.
func completeBuild(ctx context.Context, db *sql.DB, id, name string, artifact []byte, log string) error {
	return tursodb.InTx(ctx, db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE project_wasm_builds SET status=?,progress=100,error='',artifact_name=?,artifact=?,build_log=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`,
			contractbuilds.StatusComplete, name, artifact, log, id, contractbuilds.StatusBuilding)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("WASM build changed while it was being completed")
		}
		return buildChanged(ctx, tx, id)
	})
}

// startCheck records that the checks of a complete Build have started.
func startCheck(ctx context.Context, db *sql.DB, buildID string) error {
	return tursodb.InTx(ctx, db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `INSERT INTO project_checks(build_id,owner_id,status)
			SELECT id,owner_id,? FROM project_wasm_builds WHERE id=? AND status=?`, checkRunning, buildID, contractbuilds.StatusComplete)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("WASM build not found")
		}
		return checkChanged(ctx, tx, buildID)
	})
}

// finishCheck records how the running checks of a Build ended.
func finishCheck(ctx context.Context, db *sql.DB, buildID, status, errText, log string, noTests bool) error {
	return tursodb.InTx(ctx, db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE project_checks SET status=?,error=?,check_log=?,no_tests=?,updated_at=CURRENT_TIMESTAMP WHERE build_id=? AND status=?`,
			status, errText, log, noTests, buildID, checkRunning)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return errors.New("check not found")
		}
		return checkChanged(ctx, tx, buildID)
	})
}

// getCheck returns the Check of a Build. When owner is empty the Check is found
// whoever owns it.
func getCheck(ctx context.Context, q querier, buildID, owner string) (contractbuilds.Check, bool, error) {
	query := `SELECT build_id,status,error,check_log,no_tests FROM project_checks WHERE build_id=?`
	args := []any{buildID}
	if owner != "" {
		query += ` AND owner_id=?`
		args = append(args, owner)
	}
	var check contractbuilds.Check
	var noTests int
	err := q.QueryRowContext(ctx, query, args...).Scan(&check.BuildID, &check.Status, &check.Error, &check.Log, &noTests)
	if errors.Is(err, sql.ErrNoRows) {
		return contractbuilds.Check{}, false, nil
	}
	check.NoTests = noTests != 0
	return check, err == nil, err
}

// buildChanged adds the Build's event to the outbox in tx. The event carries the
// Build's status and its Project and Snapshot, so a consumer can store a
// reference from it alone.
func buildChanged(ctx context.Context, tx *sql.Tx, id string) error {
	var event contractbuilds.BuildChanged
	err := tx.QueryRowContext(ctx, `SELECT id,owner_id,project_id,snapshot_id,target,status,error FROM project_wasm_builds WHERE id=?`, id).
		Scan(&event.BuildID, &event.OwnerID, &event.ProjectID, &event.SnapshotID, &event.Target, &event.Status, &event.Error)
	if err != nil {
		return err
	}
	return addEvent(ctx, tx, contractbuilds.SubjectBuildChanged, fmt.Sprintf("build:%s:%s:%s", id, event.Status, uuid.NewString()), event)
}

// checkChanged adds the Check's event to the outbox in tx.
func checkChanged(ctx context.Context, tx *sql.Tx, buildID string) error {
	check, found, err := getCheck(ctx, tx, buildID, "")
	if err != nil || !found {
		return err
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT owner_id FROM project_checks WHERE build_id=?`, buildID).Scan(&owner); err != nil {
		return err
	}
	event := contractbuilds.CheckChanged{BuildID: buildID, OwnerID: owner, Status: check.Status, Error: check.Error, Log: check.Log, NoTests: check.NoTests}
	return addEvent(ctx, tx, contractbuilds.SubjectCheckChanged, fmt.Sprintf("check:%s:%s:%s", buildID, check.Status, uuid.NewString()), event)
}

// addEvent adds the JSON of value to the outbox in tx.
func addEvent(ctx context.Context, tx *sql.Tx, subject, msgID string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, tx, subject, msgID, data)
}

// deleteBuilds deletes the Builds where selects, with their Checks, and returns
// their IDs so their Artifacts can go too. Nothing is published: the owners of
// these records already know they are going.
func deleteBuilds(ctx context.Context, db *sql.DB, where string, args ...any) ([]string, error) {
	var ids []string
	err := tursodb.InTx(ctx, db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM project_wasm_builds WHERE `+where, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `DELETE FROM project_checks WHERE build_id=?`, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM project_wasm_builds WHERE id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}
