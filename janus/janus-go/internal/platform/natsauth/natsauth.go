// Package natsauth issues the decentralised NATS credentials of section 7 of
// docs/architecture.md: an operator that signs account JWTs, the JANUS and
// AGENTS accounts, and one user per Agent that may touch only that Agent's
// subjects. It holds keys and claims; it does not run a server or store
// anything.
package natsauth

import (
	"fmt"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
)

// Operator signs the account JWTs. Its key stays with the hub's configuration.
type Operator struct {
	key    nkeys.KeyPair
	Claims *jwt.OperatorClaims
	JWT    string
}

// NewOperator creates an operator named name and signs its own JWT.
func NewOperator(name string) (*Operator, error) {
	key, err := nkeys.CreateOperator()
	if err != nil {
		return nil, fmt.Errorf("create operator key: %w", err)
	}
	pub, err := key.PublicKey()
	if err != nil {
		return nil, err
	}
	claims := jwt.NewOperatorClaims(pub)
	claims.Name = name
	token, err := claims.Encode(key)
	if err != nil {
		return nil, fmt.Errorf("sign operator JWT: %w", err)
	}
	return &Operator{key: key, Claims: claims, JWT: token}, nil
}

// SetSystemAccount names the account the server runs its own traffic and
// JetStream control under. It is re-signed into the operator's JWT.
func (o *Operator) SetSystemAccount(sys *Account) error {
	o.Claims.SystemAccount = sys.Pub
	token, err := o.Claims.Encode(o.key)
	if err != nil {
		return fmt.Errorf("sign operator JWT: %w", err)
	}
	o.JWT = token
	return nil
}

// Account is an account the operator has signed. Its key signs the user JWTs
// of its members, so fleet holds the AGENTS key and JANUS the JANUS key.
type Account struct {
	operator *Operator
	key      nkeys.KeyPair
	Pub      string
	Claims   *jwt.AccountClaims
	JWT      string
}

// NewAccount creates an account named name, signed by op.
func NewAccount(op *Operator, name string) (*Account, error) {
	key, err := nkeys.CreateAccount()
	if err != nil {
		return nil, fmt.Errorf("create account key: %w", err)
	}
	pub, err := key.PublicKey()
	if err != nil {
		return nil, err
	}
	a := &Account{operator: op, key: key, Pub: pub, Claims: jwt.NewAccountClaims(pub)}
	a.Claims.Name = name
	if err := a.sign(); err != nil {
		return nil, err
	}
	return a, nil
}

// sign re-encodes the account JWT from Claims. Changing Claims needs a sign
// before the server sees the change.
func (a *Account) sign() error {
	token, err := a.Claims.Encode(a.operator.key)
	if err != nil {
		return fmt.Errorf("sign account JWT: %w", err)
	}
	a.JWT = token
	return nil
}

// EnableJetStream gives the account unlimited JetStream, so its users can
// create buckets and streams on the server they connect to.
func (a *Account) EnableJetStream() error {
	a.Claims.Limits.JetStreamLimits = jwt.JetStreamLimits{
		MemoryStorage: -1, DiskStorage: -1, Streams: -1, Consumer: -1, MaxAckPending: -1,
	}
	return a.sign()
}

// Export makes subject available to another account as a service, so JANUS
// can request janus.agent.> from AGENTS.
func (a *Account) Export(name, subject string) error {
	a.Claims.Exports.Add(&jwt.Export{Name: name, Subject: jwt.Subject(subject), Type: jwt.Service})
	return a.sign()
}

// Import brings subject from the exporting account into this one.
func (a *Account) Import(from *Account, name, subject string) error {
	a.Claims.Imports.Add(&jwt.Import{Name: name, Subject: jwt.Subject(subject), Account: from.Pub, Type: jwt.Service})
	return a.sign()
}

// Credentials is one user's JWT and seed, the pair a NATS client connects with.
type Credentials struct {
	UserPub string
	JWT     string
	seed    []byte
}

// Options returns the client options that sign the server's nonce with the
// user's key.
func (c Credentials) Options() []nats.Option {
	return []nats.Option{nats.UserJWT(
		func() (string, error) { return c.JWT, nil },
		func(nonce []byte) ([]byte, error) {
			kp, err := nkeys.FromSeed(c.seed)
			if err != nil {
				return nil, err
			}
			return kp.Sign(nonce)
		},
	)}
}

// AgentSubjects are the only subjects one Agent may use, from section 7:
// its commands and its state key, its own reply inbox, and read access to
// its Owner's modules. Reading an object is a direct get of its metadata
// and an ordered consumer on the bucket's stream, so both JetStream API
// families are granted for that one stream only.
func AgentSubjects(agentID, ownerID string) (pub, sub []string) {
	modules := "OBJ_modules-" + ownerID
	pub = []string{
		"janus.agent." + agentID + ".>",
		"$KV.agent_state." + agentID,
		"$JS.API.DIRECT.GET." + modules,
		"$JS.API.DIRECT.GET." + modules + ".>",
		"$JS.API.STREAM.INFO." + modules,
		"$JS.API.STREAM.MSG.GET." + modules,
		"$JS.API.CONSUMER.*." + modules,
		"$JS.API.CONSUMER.*." + modules + ".>",
	}
	sub = []string{
		"janus.agent." + agentID + ".>",
		"_INBOX." + agentID + ".>",
	}
	return pub, sub
}

// IssueAgentUser makes a user for one Agent in this account. Its permissions
// are AgentSubjects, and nothing else. It may answer requests that reach it
// through an import, so the server's reply subjects are allowed too.
func (a *Account) IssueAgentUser(agentID, ownerID string) (Credentials, error) {
	pubAllow, subAllow := AgentSubjects(agentID, ownerID)
	return a.IssueUser("agent-"+agentID, pubAllow, subAllow, true)
}

// IssueUser makes a user in this account that may publish to pub and
// subscribe to sub. With respond, it may also answer requests.
func (a *Account) IssueUser(name string, pub, sub []string, respond bool) (Credentials, error) {
	key, err := nkeys.CreateUser()
	if err != nil {
		return Credentials{}, fmt.Errorf("create user key: %w", err)
	}
	userPub, err := key.PublicKey()
	if err != nil {
		return Credentials{}, err
	}
	seed, err := key.Seed()
	if err != nil {
		return Credentials{}, err
	}
	uc := jwt.NewUserClaims(userPub)
	uc.Name = name
	uc.Pub.Allow = pub
	uc.Sub.Allow = sub
	if respond {
		uc.Resp = &jwt.ResponsePermission{MaxMsgs: 1, Expires: time.Minute}
	}
	token, err := uc.Encode(a.key)
	if err != nil {
		return Credentials{}, fmt.Errorf("sign user JWT: %w", err)
	}
	return Credentials{UserPub: userPub, JWT: token, seed: seed}, nil
}

// Revoke adds userPub to this account's revocation list and re-signs the
// account JWT. The caller hands the result to the server with
// Server.UpdateAccountClaims, which drops the user's connections.
func (a *Account) Revoke(userPub string, at time.Time) error {
	if a.Claims.Revocations == nil {
		a.Claims.Revocations = jwt.RevocationList{}
	}
	a.Claims.Revocations.Revoke(userPub, at)
	return a.sign()
}
