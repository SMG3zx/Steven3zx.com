package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"janus/internal/contract/account"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/accountevents"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// accountConsumer is the durable consumer of identity's account_deleting events.
const accountConsumer = "projects-account-deleting"

// startAccountEvents starts deleting an Account's Projects when identity announces
// it.
func (s *Service) startAccountEvents() error {
	events, err := accountevents.Consume(s.js, accountConsumer, account.DeletingSubject, s.clearAccount)
	if err != nil {
		return fmt.Errorf("consume account deletions: %w", err)
	}
	s.accountEvents = events
	return nil
}

// clearAccount deletes the Owner's Projects and cancels its Imports, then confirms
// it through the outbox, so the confirmation is published once the deletion is
// stored.
func (s *Service) clearAccount(ctx context.Context, data []byte) error {
	var deleting account.Deleting
	if err := json.Unmarshal(data, &deleting); err != nil {
		return accountevents.Permanent(err)
	}
	if _, err := s.deleteOwner(ctx, contractprojects.DeleteOwner{OwnerID: deleting.UserID}); err != nil {
		return err
	}
	return s.confirmAccount(ctx, account.Cleared{OwnerID: deleting.UserID, Service: contractprojects.ServiceName})
}

// confirmAccount queues the service's confirmation in the outbox.
func (s *Service) confirmAccount(ctx context.Context, cleared account.Cleared) error {
	data, err := json.Marshal(cleared)
	if err != nil {
		return err
	}
	err = tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		return outbox.Add(ctx, tx, account.ClearedSubject(contractprojects.ServiceName),
			account.ClearedMsgID(contractprojects.ServiceName, cleared.OwnerID), data)
	})
	if err == nil {
		s.relay.Kick()
	}
	return err
}
