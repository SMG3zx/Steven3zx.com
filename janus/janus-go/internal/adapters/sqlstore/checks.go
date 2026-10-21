package sqlstore

import (
	"context"
	"database/sql"
	"errors"

	"janus/internal/app"
)

// ApplyCheck stores the Check of a WASM Build the builds service published. A
// Check of a Build this Store does not reference is not kept.
func (s *Store) ApplyCheck(ctx context.Context, check app.CheckChange) error {
	ctx, done := s.start(ctx, "ApplyCheck")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_checks(build_id,owner_id,status,error,check_log,no_tests)
			SELECT id,owner_id,?,?,?,? FROM project_wasm_builds WHERE id=? AND owner_id=?
			ON CONFLICT(build_id) DO UPDATE SET status=excluded.status,error=excluded.error,check_log=excluded.check_log,no_tests=excluded.no_tests,updated_at=CURRENT_TIMESTAMP`,
			check.Status, check.Error, check.Log, check.NoTests, check.BuildID, check.OwnerID)
		return err
	}))
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
