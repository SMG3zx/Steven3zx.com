// Package natstrust makes the decentralised NATS trust of the control plane: the
// operator and the SYS, JANUS and AGENTS accounts, restored from a keystore so
// they keep their identity across restarts (section 7 of docs/architecture.md).
// It holds no server and no connection; cmd/janus and the tests start them.
package natstrust

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"

	"janus/internal/platform/keystore"
	"janus/internal/platform/natsauth"
	"janus/internal/platform/natsx"
)

// Trust is the trust of one control plane.
type Trust struct {
	// Server is what the embedded server trusts.
	Server *natsx.Trust
	// Client are the options of the control plane's own connection, which is the
	// one user of JANUS.
	Client []nats.Option
	// Fleet are the options of the connection the fleet service reaches the
	// Agents with. It is a user of AGENTS, so the Agents' state, modules and Usage
	// streams, which live in AGENTS, are reached from the account they belong to.
	Fleet []nats.Option
	// Agents is the AGENTS account, which signs the Agents' leaf credentials.
	Agents *natsauth.Account
	// Public is the public half of the trust that Agents verify their own server
	// against. None of it is secret.
	Public natsauth.PublicTrust
}

// Open restores the operator and the accounts from the keystore at path, making
// each key the first time, and sets their claims the same way on every start.
// revoked lists the leaf users fleet has revoked, so they stay revoked after a
// restart. The keystore is closed before Open returns: the keys are in memory
// after that.
func Open(ctx context.Context, path, secret string, revoked map[string]time.Time) (Trust, error) {
	store, err := keystore.Open(ctx, path, secret)
	if err != nil {
		return Trust{}, err
	}
	defer store.Close()

	operatorSeed, err := store.GetOrCreate(ctx, "operator", natsauth.NewOperatorSeed)
	if err != nil {
		return Trust{}, err
	}
	operator, err := natsauth.OperatorFromSeed(operatorSeed, "janus")
	if err != nil {
		return Trust{}, err
	}
	account := func(key, name string) (*natsauth.Account, error) {
		seed, err := store.GetOrCreate(ctx, key, natsauth.NewAccountSeed)
		if err != nil {
			return nil, err
		}
		return natsauth.AccountFromSeed(operator, seed, name)
	}
	system, err := account("system", "SYS")
	if err != nil {
		return Trust{}, err
	}
	janus, err := account("janus", "JANUS")
	if err != nil {
		return Trust{}, err
	}
	agents, err := account("agents", "AGENTS")
	if err != nil {
		return Trust{}, err
	}

	if err := operator.SetSystemAccount(system); err != nil {
		return Trust{}, err
	}
	// AGENTS exports janus.agent.> to JANUS, and both accounts get JetStream.
	if err := agents.Export("agent commands", "janus.agent.>"); err != nil {
		return Trust{}, err
	}
	if err := janus.Import(agents, "agent commands", "janus.agent.>"); err != nil {
		return Trust{}, err
	}
	for pub, at := range revoked {
		if err := agents.Revoke(pub, at); err != nil {
			return Trust{}, err
		}
	}
	if err := agents.EnableJetStream(); err != nil {
		return Trust{}, err
	}
	if err := janus.EnableJetStream(); err != nil {
		return Trust{}, err
	}

	// The control plane's connection is the one user of JANUS. It may use every
	// subject, as the services that share it need.
	control, err := janus.IssueUser("control-plane", []string{">"}, []string{">"}, false)
	if err != nil {
		return Trust{}, err
	}
	fleet, err := agents.IssueUser("fleet", []string{">"}, []string{">"}, false)
	if err != nil {
		return Trust{}, err
	}
	return Trust{
		Server: &natsx.Trust{
			Operator:      operator,
			SystemAccount: system,
			Accounts:      []*natsauth.Account{system, janus, agents},
		},
		Client: control.Options(),
		Fleet:  fleet.Options(),
		Agents: agents,
		Public: natsauth.PublicTrust{
			OperatorJWT:   operator.JWT,
			SystemAccount: system.Pub,
			SystemJWT:     system.JWT,
			AgentsAccount: agents.Pub,
			AgentsJWT:     agents.JWT,
		},
	}, nil
}
