// Package identity is the identity service. It owns identity.db: Users, their
// local credentials and Provider Identities, and their GitHub Connection, with
// the token encrypted. It also runs the deletion of an Account: it announces the
// deletion and deletes the User once every service has confirmed. Its contract
// is internal/contract/identity.
package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/internal/contract"
	"janus/internal/contract/account"
	contractid "janus/internal/contract/identity"
	"janus/internal/platform/natsx"
	"janus/internal/platform/outbox"
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
	conn    *nats.Conn
	js      jetstream.JetStream
	relay   *outbox.Relay
	service micro.Service
	// cleared consumes the services' confirmations of an Account's deletion.
	cleared jetstream.ConsumeContext
}

// New returns a stopped service.
func New(cfg Config) *Service {
	return &Service{cfg: cfg, key: deriveTokenKey(cfg.TokenSecret)}
}

// Start opens identity.db, bringing its schema up to date, and starts answering on
// conn, publishing the deletions of Accounts and consuming the confirmations. A
// failure leaves nothing running.
func (s *Service) Start(ctx context.Context, conn *nats.Conn) error {
	if len(s.cfg.TokenSecret) == 0 {
		return errors.New("the identity service needs a token secret")
	}
	db, err := tursodb.Open(ctx, s.cfg.DatabasePath, migrations)
	if err != nil {
		return err
	}
	s.db, s.conn = db, conn
	if err := s.startEvents(ctx); err != nil {
		_ = s.Stop(ctx)
		return err
	}
	service, err := micro.AddService(conn, micro.Config{Name: contractid.ServiceName, Version: "1.0.0", Description: "Users, sign-in methods and GitHub Connections"})
	if err != nil {
		_ = s.Stop(ctx)
		return fmt.Errorf("register the identity service: %w", err)
	}
	s.service = service
	if err := s.addEndpoints(); err != nil {
		_ = s.Stop(ctx)
		return err
	}
	return nil
}

// startEvents declares the EVENTS stream, starts publishing the outbox, and
// consumes the services' confirmations of Account deletions.
func (s *Service) startEvents(ctx context.Context) error {
	js, err := jetstream.New(s.conn)
	if err != nil {
		return fmt.Errorf("open JetStream: %w", err)
	}
	s.js = js
	if _, err := natsx.DeclareEvents(ctx, js); err != nil {
		return err
	}
	s.relay = outbox.NewRelay(s.db, js)
	s.relay.Start()
	stream, err := js.Stream(ctx, natsx.EventsStream)
	if err != nil {
		return fmt.Errorf("open the %s stream: %w", natsx.EventsStream, err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       clearedConsumer,
		FilterSubject: account.ClearedFilter,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		return fmt.Errorf("declare the account confirmations consumer: %w", err)
	}
	if s.cleared, err = consumer.Consume(s.handleCleared); err != nil {
		return fmt.Errorf("consume account confirmations: %w", err)
	}
	return nil
}

func (s *Service) addEndpoints() error {
	return errors.Join(
		natsx.AddEndpoint(s.service, "create-local-user", contractid.SubjectCreateLocalUser, timed(s.createLocalUser)),
		natsx.AddEndpoint(s.service, "find-local-user", contractid.SubjectFindLocalUser, timed(s.findLocalUser)),
		natsx.AddEndpoint(s.service, "find-user", contractid.SubjectFindUser, timed(s.findUser)),
		natsx.AddEndpoint(s.service, "delete-user", contractid.SubjectDeleteUser, timed(s.deleteUser)),
		natsx.AddEndpoint(s.service, "delete-account", contractid.SubjectDeleteAccount, timed(s.deleteAccount)),
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

// Stop stops answering, stops consuming confirmations and publishing the outbox,
// and closes identity.db.
func (s *Service) Stop(ctx context.Context) error {
	var errs []error
	if s.service != nil {
		errs = append(errs, s.service.Stop())
	}
	if s.cleared != nil {
		s.cleared.Drain()
	}
	if s.relay != nil {
		errs = append(errs, s.relay.Stop(ctx))
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
