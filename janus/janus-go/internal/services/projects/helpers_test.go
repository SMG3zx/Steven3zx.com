package projects

import (
	"context"
	"database/sql"

	"janus/internal/platform/tursodb"
)

// addOutboxOnly queues an Import's job message as the service does when it
// records the Import, without asking the relay to publish it now.
func addOutboxOnly(s *Service, jobID string) error {
	ctx := context.Background()
	return tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error { return queueImport(ctx, tx, jobID) })
}

// queueImportNow queues an Import's job message and publishes it.
func queueImportNow(s *Service, jobID string) error {
	if err := addOutboxOnly(s, jobID); err != nil {
		return err
	}
	s.relay.Kick()
	return nil
}
