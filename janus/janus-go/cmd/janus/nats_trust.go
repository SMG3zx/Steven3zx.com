package main

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"

	"janus/internal/platform/keystore"
	"janus/internal/platform/natsauth"
	"janus/internal/platform/natsx"
)

// natsTrust is the decentralised NATS trust the control plane runs with: what
// the embedded server trusts, the credentials the control plane connects with,
// and the AGENTS account fleet signs Agents' leaf credentials with.
type natsTrust struct {
	trust      *natsx.Trust
	clientOpts []nats.Option
	agents     *natsauth.Account
}

// openNATSTrust restores the operator and the SYS, JANUS and AGENTS accounts
// from the keystore at path, making each key the first time, and sets their
// claims the same way on every start. revoked lists the leaf users fleet has
// revoked, so they stay revoked after a restart. The keystore is closed before
// it returns: the keys are in memory after that.
func openNATSTrust(ctx context.Context, path, secret, leafAddress string, revoked map[string]time.Time) (natsTrust, error) {
	store, err := keystore.Open(ctx, path, secret)
	if err != nil {
		return natsTrust{}, err
	}
	defer store.Close()

	operatorSeed, err := store.GetOrCreate(ctx, "operator", natsauth.NewOperatorSeed)
	if err != nil {
		return natsTrust{}, err
	}
	operator, err := natsauth.OperatorFromSeed(operatorSeed, "janus")
	if err != nil {
		return natsTrust{}, err
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
		return natsTrust{}, err
	}
	janus, err := account("janus", "JANUS")
	if err != nil {
		return natsTrust{}, err
	}
	agents, err := account("agents", "AGENTS")
	if err != nil {
		return natsTrust{}, err
	}

	if err := operator.SetSystemAccount(system); err != nil {
		return natsTrust{}, err
	}
	// AGENTS exports janus.agent.> to JANUS, and both accounts get JetStream.
	if err := agents.Export("agent commands", "janus.agent.>"); err != nil {
		return natsTrust{}, err
	}
	if err := janus.Import(agents, "agent commands", "janus.agent.>"); err != nil {
		return natsTrust{}, err
	}
	for pub, at := range revoked {
		if err := agents.Revoke(pub, at); err != nil {
			return natsTrust{}, err
		}
	}
	if err := agents.EnableJetStream(); err != nil {
		return natsTrust{}, err
	}
	if err := janus.EnableJetStream(); err != nil {
		return natsTrust{}, err
	}

	// The control plane's connection is the one user of JANUS. It may use
	// every subject, as the services that share it need.
	control, err := janus.IssueUser("control-plane", []string{">"}, []string{">"}, false)
	if err != nil {
		return natsTrust{}, err
	}
	return natsTrust{
		trust: &natsx.Trust{
			Operator:      operator,
			SystemAccount: system,
			Accounts:      []*natsauth.Account{system, janus, agents},
			LeafAddress:   leafAddress,
		},
		clientOpts: control.Options(),
		agents:     agents,
	}, nil
}
