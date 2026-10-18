package authentication

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/crypto/argon2"

	actortrace "janus/actors/telemetry"
)

const (
	argonMemory      = 19 * 1024
	argonIterations  = 2
	argonParallelism = 1
	argonKeyLength   = 32
	argonSaltLength  = 16
)

// Actor hashes and verifies passwords and issues and validates session tokens.
type Actor struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

var _ goakt.Actor = (*Actor)(nil)

// New creates an authentication actor using secret to sign tokens, issuer as
// the JWT issuer claim, and ttl as the token lifetime.
func New(secret []byte, issuer string, ttl time.Duration) *Actor {
	return &Actor{secret: append([]byte(nil), secret...), issuer: issuer, ttl: ttl}
}

func (*Actor) PreStart(*goakt.Context) error { return nil }

func (a *Actor) Receive(rctx *goakt.ReceiveContext) {
	_, span := actortrace.StartSpan(rctx.Context(), "actor.authentication.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("actor.type", "authentication"))
	switch msg := rctx.Message().(type) {
	case *HashPassword:
		hash, err := hashPassword(msg.Password)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&PasswordHashed{Encoded: hash, Err: err})
	case *VerifyPassword:
		valid, err := verifyPassword(msg.Password, msg.Encoded)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&PasswordVerified{Valid: valid, Err: err})
	case *IssueToken:
		issued, err := a.issue(msg.Subject)
		if err != nil {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&TokenIssued{Token: issued.Token, ExpiresAt: issued.ExpiresAt, Err: err})
	case *ValidateToken:
		claims := &sessionClaims{}
		parsed, err := jwt.ParseWithClaims(msg.Token, claims, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected JWT signing method %q", token.Method.Alg())
			}
			return a.secret, nil
		}, jwt.WithIssuer(a.issuer), jwt.WithAudience("janus-go"), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
		valid := err == nil && parsed.Valid && claims.Subject != ""
		if err != nil && !errors.Is(err, jwt.ErrTokenExpired) {
			actortrace.RecordError(span, err)
		}
		rctx.Response(&TokenValidated{Subject: claims.Subject, Valid: valid})
	default:
		rctx.Unhandled()
	}
}

type sessionClaims struct{ jwt.RegisteredClaims }

func (a *Actor) issue(subject string) (TokenIssued, error) {
	if strings.TrimSpace(subject) == "" {
		return TokenIssued{}, errors.New("token subject is required")
	}
	now := time.Now().UTC()
	expiresAt := now.Add(a.ttl)
	claims := sessionClaims{RegisteredClaims: jwt.RegisteredClaims{
		Issuer: a.issuer, Subject: subject, Audience: jwt.ClaimStrings{"janus-go"},
		ExpiresAt: jwt.NewNumericDate(expiresAt), IssuedAt: jwt.NewNumericDate(now),
		ID: uuid.NewString(),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	encoded, err := token.SignedString(a.secret)
	return TokenIssued{Token: encoded, ExpiresAt: expiresAt.Unix()}, err
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

func (*Actor) PostStop(*goakt.Context) error { return nil }
