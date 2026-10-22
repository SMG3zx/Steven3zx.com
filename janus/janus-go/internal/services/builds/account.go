package builds

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"janus/internal/contract/account"
	contractbuilds "janus/internal/contract/builds"
	"janus/internal/platform/accountevents"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// accountConsumer is the durable consumer of identity's account_deleting events.
const accountConsumer = "builds-account-deleting"

// startAccountEvents starts deleting an Account's WASM Builds when identity
// announces it.
func (s *Service) startAccountEvents() error {
	events, err := accountevents.Consume(s.js, accountConsumer, account.DeletingSubject, s.clearAccount)
	if err != nil {
		return fmt.Errorf("consume account deletions: %w", err)
	}
	s.accountEvents = events
	return nil
}

// clearAccount deletes the Owner's WASM Builds, then confirms it through the
// outbox, so the confirmation is published once the deletion is stored.
func (s *Service) clearAccount(ctx context.Context, data []byte) error {
	var deleting account.Deleting
	if err := json.Unmarshal(data, &deleting); err != nil {
		return accountevents.Permanent(err)
	}
	if _, err := s.deleteOwner(ctx, contractbuilds.Owner{OwnerID: deleting.UserID}); err != nil {
		return err
	}
	return s.confirmAccount(ctx, account.Cleared{OwnerID: deleting.UserID, Service: contractbuilds.ServiceName})
}

// confirmAccount queues the service's confirmation in the outbox, so it is
// published once the teardown it confirms is stored.
func (s *Service) confirmAccount(ctx context.Context, cleared account.Cleared) error {
	data, err := json.Marshal(cleared)
	if err != nil {
		return err
	}
	err = tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		return outbox.Add(ctx, tx, account.ClearedSubject(contractbuilds.ServiceName),
			account.ClearedMsgID(contractbuilds.ServiceName, cleared.OwnerID), data)
	})
	if err == nil {
		s.relay.Kick()
	}
	return err
}

// activeBuilds lists the Owner's Builds that are queued or building.
func (s *Service) activeBuilds(ctx context.Context, owner contractbuilds.Owner) (contractbuilds.ActiveBuilds, error) {
	if owner.OwnerID == "" {
		return contractbuilds.ActiveBuilds{}, invalid("an owner is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,project_id,status FROM project_wasm_builds WHERE owner_id=? AND status IN ('queued','building') ORDER BY created_at,id`, owner.OwnerID)
	if err != nil {
		return contractbuilds.ActiveBuilds{}, err
	}
	defer rows.Close()
	active := contractbuilds.ActiveBuilds{Builds: []contractbuilds.ActiveBuild{}}
	for rows.Next() {
		var build contractbuilds.ActiveBuild
		if err := rows.Scan(&build.ID, &build.ProjectID, &build.Status); err != nil {
			return contractbuilds.ActiveBuilds{}, err
		}
		active.Builds = append(active.Builds, build)
	}
	return active, rows.Err()
}
