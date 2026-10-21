package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	contractid "janus/internal/contract/identity"
	"janus/internal/platform/tursodb"
)

// migrations is identity.db's schema. Its first entry matches the identity
// tables janus.db had, so `janus migrate split` copies rows as they are.
var migrations = []string{`
CREATE TABLE users (
	id TEXT PRIMARY KEY,
	email TEXT NOT NULL DEFAULT '',
	display_name TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE local_credentials (
	user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL
);
CREATE TABLE oauth_identities (
	provider TEXT NOT NULL,
	subject TEXT NOT NULL,
	user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (provider, subject)
);
CREATE TABLE github_connections (
	user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	subject TEXT NOT NULL UNIQUE,
	token_ciphertext BLOB NOT NULL,
	connected_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`}

func isUnique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func (s *Service) createLocalUser(ctx context.Context, user contractid.CreateLocalUser) (contractid.Empty, error) {
	email := strings.TrimSpace(user.Email)
	if user.ID == "" || email == "" || user.PasswordHash == "" {
		return contractid.Empty{}, invalid("a local user needs an ID, an email and a password hash")
	}
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,email) VALUES(?,?)`, user.ID, email); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO local_credentials(user_id,email,password_hash) VALUES(?,?,?)`, user.ID, email, user.PasswordHash)
		return err
	})
	if isUnique(err) {
		return contractid.Empty{}, conflict("account already exists")
	}
	return contractid.Empty{}, err
}

func (s *Service) findLocalUser(ctx context.Context, request contractid.FindLocalUser) (contractid.LocalUser, error) {
	var found contractid.LocalUser
	err := s.db.QueryRowContext(ctx, `SELECT user_id,email,password_hash FROM local_credentials WHERE email=? COLLATE NOCASE`, strings.TrimSpace(request.Email)).Scan(&found.ID, &found.Email, &found.PasswordHash)
	switch {
	case err == nil:
		found.Found = true
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	}
	return found, err
}

func (s *Service) findUser(ctx context.Context, request contractid.FindUser) (contractid.User, error) {
	var found contractid.User
	err := s.db.QueryRowContext(ctx, `SELECT id,email,display_name FROM users WHERE id=?`, request.ID).Scan(&found.ID, &found.Email, &found.DisplayName)
	switch {
	case err == nil:
		found.Found = true
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	}
	return found, err
}

// deleteUser removes the User; its credentials, identities and GitHub
// Connection go with it through the foreign keys.
func (s *Service) deleteUser(ctx context.Context, request contractid.DeleteUser) (contractid.UserDeleted, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, request.ID)
	if err != nil {
		return contractid.UserDeleted{}, err
	}
	n, err := result.RowsAffected()
	return contractid.UserDeleted{Deleted: n > 0}, err
}

// resolveGoogle returns the User that owns a Google identity, creating one
// when the identity is new. It matches by subject, never by email.
func (s *Service) resolveGoogle(ctx context.Context, identity contractid.ResolveIdentity) (contractid.Resolved, error) {
	if identity.Subject == "" {
		return contractid.Resolved{}, invalid("a Google identity needs a subject")
	}
	result := contractid.Resolved{Email: identity.Email, DisplayName: identity.DisplayName}
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name FROM oauth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='google' AND i.subject=?`, identity.Subject).Scan(&result.ID, &result.Email, &result.DisplayName)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result.ID = uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name) VALUES(?,?,?)`, result.ID, identity.Email, identity.DisplayName); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('google',?,?)`, identity.Subject, result.ID)
		return err
	})
	if err != nil {
		return contractid.Resolved{}, err
	}
	return result, nil
}

// resolveGitHubLogin returns the User that owns a GitHub identity, creating one
// when the identity is new. It matches by subject, never by email: a new
// identity whose email belongs to another User is reported as EmailInUse.
func (s *Service) resolveGitHubLogin(ctx context.Context, login contractid.ResolveIdentity) (contractid.Resolved, error) {
	if login.Subject == "" {
		return contractid.Resolved{}, invalid("a GitHub login needs a subject")
	}
	var result contractid.Resolved
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name FROM oauth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='github' AND i.subject=?`, login.Subject).Scan(&result.ID, &result.Email, &result.DisplayName)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		email := strings.TrimSpace(login.Email)
		if email == "" {
			return invalid("GitHub did not return a verified primary email")
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email<>'' AND email=? COLLATE NOCASE`, email).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			result.EmailInUse = true
			return nil
		}
		result.ID = uuid.NewString()
		result.Email, result.DisplayName = email, strings.TrimSpace(login.DisplayName)
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name) VALUES(?,?,?)`, result.ID, result.Email, result.DisplayName); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('github',?,?)`, login.Subject, result.ID)
		return err
	})
	if err != nil {
		return contractid.Resolved{}, err
	}
	return result, nil
}
