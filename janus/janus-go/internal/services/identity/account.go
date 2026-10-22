package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/contract/account"
	contractbuilds "janus/internal/contract/builds"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/natsx"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// clearedConsumer is the durable consumer of the services' confirmations.
const clearedConsumer = "identity-account-cleared"

// retryDelay is how long a confirmation that could not be recorded waits before
// it is delivered again.
const retryDelay = 5 * time.Second

// deleteAccount starts deleting the User's Account, or reports how far a deletion
// has got. An Account with unfinished imports or builds is refused unless the
// request cancels them. Otherwise the deletion is announced through the outbox,
// and the User goes once every service in account.Services has confirmed.
func (s *Service) deleteAccount(ctx context.Context, request contractid.DeleteAccount) (contractid.AccountDeletion, error) {
	if request.UserID == "" {
		return contractid.AccountDeletion{}, invalid("a User is required")
	}
	var deleting int
	err := s.db.QueryRowContext(ctx, `SELECT deleting FROM users WHERE id=?`, request.UserID).Scan(&deleting)
	if errors.Is(err, sql.ErrNoRows) {
		return contractid.AccountDeletion{Status: contractid.AccountMissing}, nil
	}
	if err != nil {
		return contractid.AccountDeletion{}, err
	}
	if deleting == 0 {
		active, err := s.activeJobs(ctx, request.UserID)
		if err != nil {
			return contractid.AccountDeletion{}, err
		}
		if len(active) > 0 && !request.CancelActiveJobs {
			return contractid.AccountDeletion{Status: contractid.AccountRefused, ActiveJobs: active}, nil
		}
		if err := s.startDeleting(ctx, request.UserID); err != nil {
			return contractid.AccountDeletion{}, err
		}
	}
	return contractid.AccountDeletion{Status: contractid.AccountDeleting}, nil
}

// activeJobs lists the User's imports and WASM builds that have not finished.
func (s *Service) activeJobs(ctx context.Context, userID string) ([]contractid.ActiveJob, error) {
	var imports contractprojects.ActiveImports
	if err := natsx.Call(ctx, s.conn, contractprojects.SubjectActiveImports, contractprojects.Owner{OwnerID: userID}, &imports); err != nil {
		return nil, fmt.Errorf("list the Account's imports: %w", err)
	}
	var builds contractbuilds.ActiveBuilds
	if err := natsx.Call(ctx, s.conn, contractbuilds.SubjectActiveBuilds, contractbuilds.Owner{OwnerID: userID}, &builds); err != nil {
		return nil, fmt.Errorf("list the Account's WASM builds: %w", err)
	}
	active := []contractid.ActiveJob{}
	for _, job := range imports.Imports {
		active = append(active, contractid.ActiveJob{Kind: "import", ID: job.ID, ProjectID: job.ProjectID, Status: job.Status})
	}
	for _, build := range builds.Builds {
		active = append(active, contractid.ActiveJob{Kind: "build", ID: build.ID, ProjectID: build.ProjectID, Status: build.Status})
	}
	return active, nil
}

// startDeleting marks the User deleting, which stops its sign-in, and queues the
// announcement in the same transaction. A User already deleting is left alone, so
// asking again resumes the deletion.
func (s *Service) startDeleting(ctx context.Context, userID string) error {
	data, err := json.Marshal(account.Deleting{UserID: userID})
	if err != nil {
		return err
	}
	err = tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE users SET deleting=1 WHERE id=? AND deleting=0`, userID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		return outbox.Add(ctx, tx, account.DeletingSubject, "account_deleting:"+userID, data)
	})
	if err == nil {
		s.relay.Kick()
	}
	return err
}

// handleCleared records one service's confirmation. A confirmation that cannot
// be recorded is delivered again; one that can never be applied is dropped.
func (s *Service) handleCleared(msg jetstream.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), operationTimeout)
	defer cancel()
	var cleared account.Cleared
	if err := json.Unmarshal(msg.Data(), &cleared); err != nil || !slices.Contains(account.Services, cleared.Service) {
		_ = msg.Term()
		return
	}
	if err := s.confirmCleared(ctx, cleared); err != nil {
		_ = msg.NakWithDelay(retryDelay)
		return
	}
	_ = msg.Ack()
}

// confirmCleared records that a service has no records left for the User. When
// every service has confirmed, the User goes, with its credentials, identities and
// GitHub Connection, in the same transaction.
func (s *Service) confirmCleared(ctx context.Context, cleared account.Cleared) error {
	return tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var deleting int
		err := tx.QueryRowContext(ctx, `SELECT deleting FROM users WHERE id=?`, cleared.OwnerID).Scan(&deleting)
		if errors.Is(err, sql.ErrNoRows) || err == nil && deleting == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_clearances(user_id,service) VALUES(?,?) ON CONFLICT DO NOTHING`, cleared.OwnerID, cleared.Service); err != nil {
			return err
		}
		var confirmed int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_clearances WHERE user_id=?`, cleared.OwnerID).Scan(&confirmed); err != nil {
			return err
		}
		if confirmed < len(account.Services) {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM account_clearances WHERE user_id=?`, cleared.OwnerID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, cleared.OwnerID)
		return err
	})
}
