package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"janus/internal/contract/account"
	contractfleet "janus/internal/contract/fleet"
	"janus/internal/platform/accountevents"
)

// accountConsumer is the durable consumer of identity's account_deleting events.
const accountConsumer = "fleet-account-deleting"

// startAccountEvents starts clearing an Account's Deployments and Agents when
// identity announces its deletion.
func (s *Service) startAccountEvents() error {
	events, err := accountevents.Consume(s.js, accountConsumer, account.DeletingSubject, s.clearAccount)
	if err != nil {
		return fmt.Errorf("consume account deletions: %w", err)
	}
	s.accountEvents = events
	return nil
}

// clearAccount stops the Owner's Deployments, takes its Agents off the tailnet and
// revokes their keys, then deletes its records and confirms. Each step is
// idempotent, so a deletion delivered again repeats them safely.
func (s *Service) clearAccount(ctx context.Context, data []byte) error {
	var deleting account.Deleting
	if err := json.Unmarshal(data, &deleting); err != nil {
		return accountevents.Permanent(err)
	}
	owner := contractfleet.Owner{OwnerID: deleting.UserID}
	if _, err := s.teardownDeployments(ctx, owner); err != nil {
		return fmt.Errorf("stop the Account's Deployments: %w", err)
	}
	if _, err := s.revokeAgents(ctx, owner); err != nil {
		return fmt.Errorf("remove the Account's Agents from the tailnet: %w", err)
	}
	if _, err := s.deleteOwner(ctx, owner); err != nil {
		return fmt.Errorf("delete the Account's fleet records: %w", err)
	}
	return s.confirmAccount(ctx, account.Cleared{OwnerID: deleting.UserID, Service: contractfleet.ServiceName})
}

// confirmAccount publishes the fleet service's confirmation. The message ID makes
// a confirmation published twice count once.
func (s *Service) confirmAccount(ctx context.Context, cleared account.Cleared) error {
	data, err := json.Marshal(cleared)
	if err != nil {
		return err
	}
	_, err = s.js.Publish(ctx, account.ClearedSubject(contractfleet.ServiceName), data,
		jetstream.WithMsgID(account.ClearedMsgID(contractfleet.ServiceName, cleared.OwnerID)))
	return err
}
