package main

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"

	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/natsauth"
	"janus/internal/platform/natstrust"
	"janus/internal/platform/natsx"
)

// natsTrust is the NATS trust the control plane runs with, in the shape the
// startup code uses it.
type natsTrust struct {
	trust      *natsx.Trust
	clientOpts []nats.Option
	fleetOpts  []nats.Option
	agents     *natsauth.Account
	public     *contractfleet.AgentTrust
}

// openNATSTrust opens the trust from the keystore at path. See natstrust.Open.
func openNATSTrust(ctx context.Context, path, secret string, revoked map[string]time.Time) (natsTrust, error) {
	trust, err := natstrust.Open(ctx, path, secret, revoked)
	if err != nil {
		return natsTrust{}, err
	}
	return natsTrust{
		trust:      trust.Server,
		clientOpts: trust.Client,
		fleetOpts:  trust.Fleet,
		agents:     trust.Agents,
		public: &contractfleet.AgentTrust{
			OperatorJWT:   trust.Public.OperatorJWT,
			SystemAccount: trust.Public.SystemAccount,
			SystemJWT:     trust.Public.SystemJWT,
			AgentsAccount: trust.Public.AgentsAccount,
			AgentsJWT:     trust.Public.AgentsJWT,
		},
	}, nil
}
