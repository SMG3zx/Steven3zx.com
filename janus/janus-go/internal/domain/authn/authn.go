package authn

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonKeyLength   = 32
	argonSaltLength  = 16
)

// Authentication hashes and verifies passwords and issues and validates session
// tokens. It holds only configuration, so any number of callers may use it at
// once and no call waits for another.
type Authentication struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

// New creates an Authentication that signs tokens with secret, uses issuer as the JWT
// issuer claim, and gives tokens a lifetime of ttl.
func New(secret []byte, issuer string, ttl time.Duration) *Authentication {
	return &Authentication{secret: append([]byte(nil), secret...), issuer: issuer, ttl: ttl}
}

// Token is a signed session token and the Unix time at which it expires.
type SessionToken struct {
	Value     string
	ExpiresAt int64
}

// HashPassword returns the Argon2id encoding of password.
func (s *Authentication) HashPassword(ctx context.Context, password string) (string, error) {
	hash, err := hashPassword(password)
	return hash, err
}

// VerifyPassword reports whether password matches the encoded Argon2id hash.
func (s *Authentication) VerifyPassword(ctx context.Context, password, encoded string) (bool, error) {
	valid, err := verifyPassword(password, encoded)
	return valid, err
}

// IssueToken signs a session token for subject.
func (s *Authentication) IssueToken(ctx context.Context, subject string) (SessionToken, error) {
	token, err := s.issue(subject)
	return token, err
}

// ValidateToken returns the subject of a valid, unexpired token. valid is false
// for a token that is malformed, expired, or signed for another issuer.
func (s *Authentication) ValidateToken(ctx context.Context, token string) (subject string, valid bool) {
	claims := &sessionClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected JWT signing method %q", token.Method.Alg())
		}
		return s.secret, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithAudience("janus-go"), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	return claims.Subject, err == nil && parsed.Valid && claims.Subject != ""
}

type sessionClaims struct{ jwt.RegisteredClaims }

func (s *Authentication) issue(subject string) (SessionToken, error) {
	if strings.TrimSpace(subject) == "" {
		return SessionToken{}, errors.New("token subject is required")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(s.ttl)
	claims := sessionClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: s.issuer, Subject: subject, Audience: jwt.ClaimStrings{"janus-go"},
		ExpiresAt: jwt.NewNumericDate(expiresAt), IssuedAt: jwt.NewNumericDate(now),
		ID: uuid.NewString(),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	encoded, err := token.SignedString(s.secret)
	return SessionToken{Value: encoded, ExpiresAt: expiresAt.Unix()}, err
}

func hashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 1024 {
		return "", errors.New("password must be between 12 and 1024 bytes")
	}
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return false, errors.New("unsupported password hash format")
	}
	var memory, iterations, parallelism uint32
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil || memory < 8*1024 || memory > 128*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 4 {
		return false, errors.New("invalid password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errors.New("invalid password hash salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("invalid password hash digest")
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
