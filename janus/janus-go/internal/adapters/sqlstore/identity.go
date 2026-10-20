package sqlstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"
	"strings"

	"janus/internal/app"
)

// CreateLocalUser creates a user and its email and password credential.
func (s *Store) CreateLocalUser(ctx context.Context, user app.CreateLocalUser) error {
	ctx, done := s.start(ctx, "CreateLocalUser")
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		email := strings.TrimSpace(user.Email)
		if _, err := tx.ExecContext(ctx, `INSERT INTO users(id,email) VALUES(?,?)`, user.ID, email); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO local_credentials(user_id,email,password_hash) VALUES(?,?,?)`, user.ID, email, user.PasswordHash)
		return err
	}))
}

// FindLocalUser finds a user by the email of its credential, ignoring case.
func (s *Store) FindLocalUser(ctx context.Context, email string) (app.LocalUserFound, error) {
	ctx, done := s.start(ctx, "FindLocalUser")
	var result app.LocalUserFound
	err := s.db.QueryRowContext(ctx, `SELECT user_id,email,password_hash FROM local_credentials WHERE email=? COLLATE NOCASE`, strings.TrimSpace(email)).Scan(&result.ID, &result.Email, &result.PasswordHash)
	switch {
	case err == nil:
		result.Found = true
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	}
	return result, done(err)
}

// FindUser finds a user by ID.
func (s *Store) FindUser(ctx context.Context, id string) (app.UserFound, error) {
	ctx, done := s.start(ctx, "FindUser")
	var result app.UserFound
	err := s.db.QueryRowContext(ctx, `SELECT id,email,display_name FROM users WHERE id=?`, id).Scan(&result.ID, &result.Email, &result.DisplayName)
	switch {
	case err == nil:
		result.Found = true
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	}
	return result, done(err)
}

// ResolveGoogleIdentity returns the user that owns a Google identity, and
// creates one when the identity is new. It matches by subject, never by email.
func (s *Store) ResolveGoogleIdentity(ctx context.Context, identity app.ResolveGoogleIdentity) (app.GoogleIdentityResolved, error) {
	ctx, done := s.start(ctx, "ResolveGoogleIdentity")
	result := app.GoogleIdentityResolved{Email: identity.Email, DisplayName: identity.DisplayName}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name FROM oauth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='google' AND i.subject=?`, identity.Subject).Scan(&result.ID, &result.Email, &result.DisplayName)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		userID := newID()
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name) VALUES(?,?,?)`, userID, identity.Email, identity.DisplayName); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('google',?,?)`, identity.Subject, userID); err != nil {
			return err
		}
		result.ID = userID
		return nil
	})
	return result, done(err)
}

// ResolveGitHubLogin returns the user that owns a GitHub identity, and creates
// one when the identity is new. It matches by subject, never by email: a new
// identity whose email belongs to another user is reported as EmailInUse.
func (s *Store) ResolveGitHubLogin(ctx context.Context, login app.ResolveGitHubLogin) (app.GitHubLoginResolved, error) {
	ctx, done := s.start(ctx, "ResolveGitHubLogin")
	var result app.GitHubLoginResolved
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name FROM oauth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='github' AND i.subject=?`, login.Subject).Scan(&result.ID, &result.Email, &result.DisplayName)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		email := strings.TrimSpace(login.Email)
		if email == "" {
			return errors.New("GitHub did not return a verified primary email")
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email<>'' AND email=? COLLATE NOCASE`, email).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			result.EmailInUse = true
			return nil
		}
		result.ID = newID()
		result.Email, result.DisplayName = email, strings.TrimSpace(login.DisplayName)
		if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name) VALUES(?,?,?)`, result.ID, result.Email, result.DisplayName); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('github',?,?)`, login.Subject, result.ID)
		return err
	})
	return result, done(err)
}

// ConnectGitHub stores the encrypted token of a user's GitHub Connection and
// links the GitHub identity to the user. It refuses an identity that belongs to
// another user and a user that already has a different GitHub account.
func (s *Store) ConnectGitHub(ctx context.Context, connection app.ConnectGitHub) error {
	ctx, done := s.start(ctx, "ConnectGitHub")
	if connection.UserID == "" || connection.Subject == "" || connection.Token == "" {
		return done(errors.New("incomplete GitHub connection"))
	}
	ciphertext, err := s.encryptToken(connection.Token)
	if err != nil {
		return done(err)
	}
	return done(s.inTx(ctx, func(tx *sql.Tx) error {
		var linkedUser string
		err := tx.QueryRowContext(ctx, `SELECT user_id FROM oauth_identities WHERE provider='github' AND subject=?`, connection.Subject).Scan(&linkedUser)
		if err == nil && linkedUser != connection.UserID {
			return errors.New("GitHub account is connected to another Janus account")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existingSubject string
		err = tx.QueryRowContext(ctx, `SELECT subject FROM github_connections WHERE user_id=?`, connection.UserID).Scan(&existingSubject)
		if err == nil && existingSubject != connection.Subject {
			return errors.New("a different GitHub account is already connected")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('github',?,?) ON CONFLICT(provider,subject) DO NOTHING`, connection.Subject, connection.UserID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_connections(user_id,subject,token_ciphertext) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET subject=excluded.subject,token_ciphertext=excluded.token_ciphertext,connected_at=CURRENT_TIMESTAMP`, connection.UserID, connection.Subject, ciphertext)
		return err
	}))
}

// FindGitHubToken returns the decrypted token of a user's GitHub Connection.
func (s *Store) FindGitHubToken(ctx context.Context, userID string) (token string, connected bool, err error) {
	ctx, done := s.start(ctx, "FindGitHubToken")
	var ciphertext []byte
	err = s.db.QueryRowContext(ctx, `SELECT token_ciphertext FROM github_connections WHERE user_id=?`, userID).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, done(nil)
	}
	if err != nil {
		return "", false, done(err)
	}
	token, err = s.decryptToken(ciphertext)
	return token, err == nil, done(err)
}

// DisconnectGitHub removes a user's GitHub Connection. Its sign-in identities stay.
func (s *Store) DisconnectGitHub(ctx context.Context, userID string) error {
	ctx, done := s.start(ctx, "DisconnectGitHub")
	_, err := s.db.ExecContext(ctx, `DELETE FROM github_connections WHERE user_id=?`, userID)
	return done(err)
}

func deriveTokenKey(secret string) []byte {
	sum := sha256.Sum256([]byte("janus/github-oauth-token/v1\x00" + secret))
	return sum[:]
}

func (s *Store) encryptToken(token string) ([]byte, error) {
	if len(s.key) != 32 {
		return nil, errors.New("GitHub token encryption is unavailable")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(token), nil), nil
}

func (s *Store) decryptToken(value []byte) (string, error) {
	if len(s.key) != 32 {
		return "", errors.New("GitHub token encryption is unavailable")
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(value) < gcm.NonceSize() {
		return "", errors.New("stored GitHub token is invalid")
	}
	nonce, ciphertext := value[:gcm.NonceSize()], value[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("cannot decrypt stored GitHub token; reconnect GitHub")
	}
	return string(plain), nil
}
