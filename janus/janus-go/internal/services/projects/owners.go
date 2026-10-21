package projects

import (
	"context"
	"database/sql"
	"errors"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/tursodb"
)

// deleteOwner cancels the Owner's unfinished Imports, deletes its Projects
// with everything in them, and tells the other services which Projects went.
// Running Imports stop at their next step, as their job is no longer theirs;
// those in this process are also cancelled at once.
func (s *Service) deleteOwner(ctx context.Context, owner contractprojects.DeleteOwner) (contractprojects.Empty, error) {
	if owner.OwnerID == "" {
		return contractprojects.Empty{}, invalid("an Owner is required")
	}
	var deleted int
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE project_import_jobs SET status='cancelled',updated_at=CURRENT_TIMESTAMP WHERE owner_id=? AND status IN `+activeImportStatuses, owner.OwnerID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM projects WHERE owner_id=?`, owner.OwnerID)
		if err != nil {
			return err
		}
		var projects []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			projects = append(projects, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range projects {
			if _, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id); err != nil {
				return err
			}
			if err := addEvent(ctx, tx, contractprojects.SubjectProjectDeleted, "project_deleted:"+id, contractprojects.ProjectDeletedEvent{ProjectID: id, OwnerID: owner.OwnerID}); err != nil {
				return err
			}
		}
		deleted = len(projects)
		return nil
	})
	if err != nil {
		return contractprojects.Empty{}, err
	}
	s.cancelOwner(owner.OwnerID)
	if deleted > 0 {
		s.relay.Kick()
	}
	return contractprojects.Empty{}, nil
}
