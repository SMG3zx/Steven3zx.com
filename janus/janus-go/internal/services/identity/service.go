// Package identity is the identity service. It owns identity.db: Users, their
// local credentials and Provider Identities, and their GitHub Connection, with
// the token encrypted. Its contract is internal/contract/identity.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	contractid "janus/internal/contract/identity"
	"janus/internal/platform/natsx"
	"janus/internal/platform/tursodb"
)

// operationTimeout bounds the database work of one request.
const operationTimeout = 30 * time.Second

// Config configures the service.
type Config struct {
	// DatabasePath is identity.db.
	DatabasePath string
	// TokenSecret derives the key that encrypts GitHub tokens. It is
	// JANUS_JWT_SECRET, so tokens copied from janus.db still decrypt.
	TokenSecret string
}

// Service answers the identity subjects from identity.db.
type Service struct {
	cfg     Config
	key     []byte
	db      *sql.DB
	service micro.Service
}

// New returns a stopped service.
func New(cfg Config) *Service {
	return &Service{cfg: cfg, key: deriveTokenKey(cfg.TokenSecret)}
}

// Start opens identity.db, bringing its schema up to date, and starts
// answering on conn. A failure leaves nothing running.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) error {
	if len(s.cfg.TokenSecret) == 0 {
		return errors.New("the identity service needs a token secret")
	}
	db, err := tursodb.Open(ctx, s.cfg.DatabasePath, migrations)
	if err != nil {
		return err
	}
	service, err := micro.AddService(conn, micro.Config{Name: contractid.ServiceName, Version: "1.0.0", Description: "Users, sign-in methods and GitHub Connections"})
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("register the identity service: %w", err)
	}
	s.db, s.service = db, service
	if err := s.addEndpoints(); err != nil {
		_ = s.Stop(ctx)
		return err
	}
	return nil
}

func (s *Service) addEndpoints() error {
	return errors.Join(
		natsx.AddEndpoint(s.service, "create-local-user", contractid.SubjectCreateLocalUser, timed(s.createLocalUser)),
		natsx.AddEndpoint(s.service, "find-local-user", contractid.SubjectFindLocalUser, timed(s.findLocalUser)),
		natsx.AddEndpoint(s.service, "find-user", contractid.SubjectFindUser, timed(s.findUser)),
		natsx.AddEndpoint(s.service, "delete-user", contractid.SubjectDeleteUser, timed(s.deleteUser)),
		natsx.AddEndpoint(s.service, "resolve-google", contractid.SubjectResolveGoogle, timed(s.resolveGoogle)),
		natsx.AddEndpoint(s.service, "resolve-github-login", contractid.SubjectResolveGitHubLogin, timed(s.resolveGitHubLogin)),
		natsx.AddEndpoint(s.service, "connect-github", contractid.SubjectConnectGitHub, timed(s.connectGitHub)),
		natsx.AddEndpoint(s.service, "github-token", contractid.SubjectGitHubToken, timed(s.gitHubToken)),
		natsx.AddEndpoint(s.service, "disconnect-github", contractid.SubjectDisconnectGitHub, timed(s.disconnectGitHub)),
	)
}

// timed bounds a handler by operationTimeout.
func timed[Req, Rep any](handle func(context.Context, Req) (Rep, error)) func(context.Context, Req) (Rep, error) {
	return func(ctx context.Context, request Req) (Rep, error) {
		ctx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()
		return handle(ctx, request)
	}
}

// Stop stops answering, waits for the requests being answered, and closes
// identity.db.
func (s *Service) Stop(context.Context) error {
	var errs []error
	if s.service != nil {
		errs = append(errs, s.service.Stop())
	}
	if s.db != nil {
		errs = append(errs, s.db.Close())
	}
	return errors.Join(errs...)
}

func invalid(format string, args ...any) error {
	return contract.Errorf(contract.StatusInvalid, format, args...)
}

func conflict(format string, args ...any) error {
	return contract.Errorf(contract.StatusConflict, format, args...)
}
