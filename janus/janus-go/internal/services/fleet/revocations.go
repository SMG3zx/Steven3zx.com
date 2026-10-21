package fleet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"janus/internal/platform/tursodb"
)

// revokeLeaf records that the leaf user userPub is revoked and revokes it in the
// AGENTS account, then has the hub apply the account. The first revocation of a
// user is kept, so a retry does not move its time.
func (s *Service) revokeLeaf(ctx context.Context, userPub string) error {
	now := s.cfg.Now()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO leaf_revocations(user_pub,revoked_at) VALUES(?,?) ON CONFLICT(user_pub) DO NOTHING`,
		userPub, now.UnixNano()); err != nil {
		return fmt.Errorf("record the leaf revocation: %w", err)
	}
	if s.cfg.Agents == nil {
		return nil
	}
	if err := s.cfg.Agents.Revoke(userPub, now); err != nil {
		return fmt.Errorf("revoke the leaf credentials: %w", err)
	}
	if s.cfg.OnRevoke != nil {
		if err := s.cfg.OnRevoke(s.cfg.Agents); err != nil {
			return fmt.Errorf("apply the leaf revocation: %w", err)
		}
	}
	return nil
}

// LeafRevocations returns the leaf users fleet.db has revoked, with when each was
// revoked. The control plane reads them before its NATS server starts, so a
// revoked Agent stays revoked across restarts. A missing fleet.db has none, and
// the file is not created: the split refuses to run over an existing one.
func LeafRevocations(ctx context.Context, path string) (map[string]time.Time, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return map[string]time.Time{}, nil
	} else if err != nil {
		return nil, err
	}
	db, err := tursodb.Open(ctx, path, migrations)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return readRevocations(ctx, db)
}

func readRevocations(ctx context.Context, db *sql.DB) (map[string]time.Time, error) {
	rows, err := db.QueryContext(ctx, `SELECT user_pub,revoked_at FROM leaf_revocations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revoked := map[string]time.Time{}
	for rows.Next() {
		var pub string
		var at int64
		if err := rows.Scan(&pub, &at); err != nil {
			return nil, err
		}
		revoked[pub] = time.Unix(0, at).UTC()
	}
	return revoked, rows.Err()
}
