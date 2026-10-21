package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io"

	contractid "janus/internal/contract/identity"
	"janus/internal/platform/tursodb"
)

// connectGitHub stores the encrypted token of a User's GitHub Connection and
// links the GitHub identity to the User. It refuses an identity that belongs
// to another User and a User that already has a different GitHub account.
func (s *Service) connectGitHub(ctx context.Context, connection contractid.ConnectGitHub) (contractid.Empty, error) {
	if connection.UserID == "" || connection.Subject == "" || connection.Token == "" {
		return contractid.Empty{}, invalid("incomplete GitHub connection")
	}
	ciphertext, err := s.encryptToken(connection.Token)
	if err != nil {
		return contractid.Empty{}, err
	}
	return contractid.Empty{}, tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=?`, connection.UserID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return invalid("no such User")
		}
		var linkedUser string
		err := tx.QueryRowContext(ctx, `SELECT user_id FROM oauth_identities WHERE provider='github' AND subject=?`, connection.Subject).Scan(&linkedUser)
		if err == nil && linkedUser != connection.UserID {
			return conflict("GitHub account is connected to another Janus account")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var existingSubject string
		err = tx.QueryRowContext(ctx, `SELECT subject FROM github_connections WHERE user_id=?`, connection.UserID).Scan(&existingSubject)
		if err == nil && existingSubject != connection.Subject {
			return conflict("a different GitHub account is already connected")
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('github',?,?) ON CONFLICT(provider,subject) DO NOTHING`, connection.Subject, connection.UserID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO github_connections(user_id,subject,token_ciphertext) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET subject=excluded.subject,token_ciphertext=excluded.token_ciphertext,connected_at=CURRENT_TIMESTAMP`, connection.UserID, connection.Subject, ciphertext)
		return err
	})
}

// gitHubToken returns the decrypted token of a User's GitHub Connection.
func (s *Service) gitHubToken(ctx context.Context, request contractid.GitHubToken) (contractid.GitHubTokenFound, error) {
	var ciphertext []byte
	err := s.db.QueryRowContext(ctx, `SELECT token_ciphertext FROM github_connections WHERE user_id=?`, request.UserID).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return contractid.GitHubTokenFound{}, nil
	}
	if err != nil {
		return contractid.GitHubTokenFound{}, err
	}
	token, err := s.decryptToken(ciphertext)
	if err != nil {
		return contractid.GitHubTokenFound{}, err
	}
	return contractid.GitHubTokenFound{Connected: true, Token: token}, nil
}

// disconnectGitHub removes a User's GitHub Connection. Its sign-in identities
// stay.
func (s *Service) disconnectGitHub(ctx context.Context, request contractid.DisconnectGitHub) (contractid.Empty, error) {
	_, err := s.db.ExecContext(ctx, `DELETE FROM github_connections WHERE user_id=?`, request.UserID)
	return contractid.Empty{}, err
}

// deriveTokenKey is the key janus.db's sqlstore used, so copied tokens decrypt.
func deriveTokenKey(secret string) []byte {
	sum := sha256.Sum256([]byte("janus/github-oauth-token/v1\x00" + secret))
	return sum[:]
}

func (s *Service) encryptToken(token string) ([]byte, error) {
	gcm, err := s.cipher()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(token), nil), nil
}

func (s *Service) decryptToken(value []byte) (string, error) {
	gcm, err := s.cipher()
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

func (s *Service) cipher() (cipher.AEAD, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
