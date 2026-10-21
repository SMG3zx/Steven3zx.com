package projects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"

	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/scan"
	"janus/internal/platform/outbox"
	"janus/internal/platform/tursodb"
)

// migrations is projects.db's schema. Its tables have the columns janus.db's
// had, so `janus migrate split` copies rows as they are; an Owner is a plain
// ID here, as Users live in the identity service.
var migrations = []string{`
CREATE TABLE projects (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX projects_owner_updated ON projects(owner_id, updated_at DESC);
CREATE TABLE project_snapshots (
	id TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	owner_id TEXT NOT NULL,
	repository_id INTEGER NOT NULL,
	repository TEXT NOT NULL,
	branch TEXT NOT NULL,
	commit_sha TEXT NOT NULL,
	default_branch TEXT NOT NULL DEFAULT '',
	language TEXT NOT NULL DEFAULT '',
	runtime TEXT NOT NULL DEFAULT '',
	stars INTEGER NOT NULL DEFAULT 0,
	forks INTEGER NOT NULL DEFAULT 0,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	manifest_json TEXT NOT NULL DEFAULT '',
	entry_point TEXT NOT NULL DEFAULT '',
	file_count INTEGER NOT NULL DEFAULT 0,
	total_bytes INTEGER NOT NULL DEFAULT 0,
	is_active INTEGER NOT NULL DEFAULT 0,
	job_id TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT 'staging',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX project_snapshots_latest ON project_snapshots(project_id, state, created_at DESC);
CREATE TABLE project_files (
	snapshot_id TEXT NOT NULL REFERENCES project_snapshots(id) ON DELETE CASCADE,
	path TEXT NOT NULL,
	mode TEXT NOT NULL,
	content BLOB NOT NULL,
	sha TEXT NOT NULL,
	PRIMARY KEY(snapshot_id, path)
);
CREATE TABLE project_import_jobs (
	id TEXT PRIMARY KEY,
	owner_id TEXT NOT NULL,
	project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	repository TEXT NOT NULL,
	branch TEXT NOT NULL,
	commit_sha TEXT NOT NULL,
	status TEXT NOT NULL,
	progress INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now')),
	updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f','now'))
);
CREATE INDEX import_jobs_owner ON project_import_jobs(owner_id, updated_at DESC);
CREATE UNIQUE INDEX one_active_import_per_project ON project_import_jobs(project_id) WHERE status IN ('queued','cloning','scanning','storing');
` + outbox.Schema}

// activeImportStatuses are the statuses of an Import that has not finished.
const activeImportStatuses = `('queued','cloning','scanning','storing')`

const projectSelect = `SELECT p.id,p.owner_id,p.name,p.description,p.created_at,p.updated_at,COALESCE(s.id,''),
COALESCE(s.repository_id,0),COALESCE(s.repository,''),COALESCE(s.default_branch,''),COALESCE(s.branch,''),COALESCE(s.commit_sha,''),COALESCE(s.entry_point,''),COALESCE(s.language,''),COALESCE(s.runtime,''),COALESCE(s.stars,0),COALESCE(s.forks,0),COALESCE(s.file_count,0),COALESCE(s.total_bytes,0),COALESCE(s.metadata_json,'{}'),
(SELECT COUNT(*) FROM project_snapshots all_s WHERE all_s.project_id=p.id AND all_s.state='ready')
FROM projects p LEFT JOIN project_snapshots s ON s.id=(SELECT id FROM project_snapshots WHERE project_id=p.id AND state='ready' AND is_active=1 LIMIT 1)`

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func scanProject(row interface{ Scan(...any) error }) (contractprojects.Project, error) {
	var p contractprojects.Project
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.SnapshotID, &p.RepositoryID, &p.Repository, &p.DefaultBranch, &p.Branch, &p.Commit, &p.EntryPoint, &p.Language, &p.Runtime, &p.Stars, &p.Forks, &p.FileCount, &p.TotalBytes, &p.MetadataJSON, &p.SnapshotCount)
	return p, err
}

func getProject(ctx context.Context, q querier, id, ownerID string) (contractprojects.Project, bool, error) {
	p, err := scanProject(q.QueryRowContext(ctx, projectSelect+` WHERE p.id=? AND p.owner_id=?`, id, ownerID))
	if errors.Is(err, sql.ErrNoRows) {
		return contractprojects.Project{}, false, nil
	}
	return p, err == nil, err
}

func validProject(name, description string) error {
	if strings.TrimSpace(name) == "" || len(name) > 120 || len(description) > 2000 {
		return invalid("project name (1-120 characters) and description (up to 2000 characters) are required")
	}
	return nil
}

func (s *Service) createProject(ctx context.Context, project contractprojects.CreateProject) (contractprojects.Project, error) {
	if project.ID == "" || project.OwnerID == "" {
		return contractprojects.Project{}, invalid("a project needs an ID and an Owner")
	}
	if err := validProject(project.Name, project.Description); err != nil {
		return contractprojects.Project{}, err
	}
	var created contractprojects.Project
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects(id,owner_id,name,description) VALUES(?,?,?,?)`, project.ID, project.OwnerID, strings.TrimSpace(project.Name), strings.TrimSpace(project.Description)); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
				return conflict("project could not be created")
			}
			return err
		}
		var err error
		created, _, err = getProject(ctx, tx, project.ID, project.OwnerID)
		return err
	})
	return created, err
}

func (s *Service) updateProject(ctx context.Context, project contractprojects.UpdateProject) (contractprojects.Project, error) {
	if err := validProject(project.Name, project.Description); err != nil {
		return contractprojects.Project{}, err
	}
	var updated contractprojects.Project
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE projects SET name=?,description=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, strings.TrimSpace(project.Name), strings.TrimSpace(project.Description), project.ID, project.OwnerID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return notFound("project not found")
		}
		updated, _, err = getProject(ctx, tx, project.ID, project.OwnerID)
		return err
	})
	return updated, err
}

// deleteProject deletes a Project and everything in it, unless an Import of
// it is in progress, and tells the other services.
func (s *Service) deleteProject(ctx context.Context, key contractprojects.ProjectKey) (contractprojects.ProjectDeleted, error) {
	var deleted bool
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_import_jobs WHERE project_id=? AND owner_id=? AND status IN `+activeImportStatuses, key.ProjectID, key.OwnerID).Scan(&active); err != nil {
			return err
		}
		if active > 0 {
			return conflict("project has an import in progress")
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id=? AND owner_id=?`, key.ProjectID, key.OwnerID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 1 {
			deleted = true
			return addEvent(ctx, tx, contractprojects.SubjectProjectDeleted, "project_deleted:"+key.ProjectID, contractprojects.ProjectDeletedEvent{ProjectID: key.ProjectID, OwnerID: key.OwnerID})
		}
		return nil
	})
	if deleted {
		s.relay.Kick()
	}
	return contractprojects.ProjectDeleted{Deleted: deleted}, err
}

// addEvent adds an event to the outbox in tx.
func addEvent(ctx context.Context, tx *sql.Tx, subject, msgID string, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return outbox.Add(ctx, tx, subject, msgID, data)
}

func (s *Service) listProjects(ctx context.Context, owner contractprojects.Owner) (contractprojects.Projects, error) {
	rows, err := s.db.QueryContext(ctx, projectSelect+` WHERE p.owner_id=? ORDER BY p.updated_at DESC`, owner.OwnerID)
	if err != nil {
		return contractprojects.Projects{}, err
	}
	defer rows.Close()
	list := contractprojects.Projects{Projects: []contractprojects.Project{}}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return contractprojects.Projects{}, err
		}
		list.Projects = append(list.Projects, p)
	}
	return list, rows.Err()
}

func (s *Service) getProject(ctx context.Context, key contractprojects.ProjectKey) (contractprojects.ProjectFound, error) {
	p, found, err := getProject(ctx, s.db, key.ProjectID, key.OwnerID)
	return contractprojects.ProjectFound{Found: found, Project: p}, err
}

func (s *Service) listSnapshots(ctx context.Context, key contractprojects.ProjectKey) (contractprojects.Snapshots, error) {
	if _, found, err := getProject(ctx, s.db, key.ProjectID, key.OwnerID); err != nil {
		return contractprojects.Snapshots{}, err
	} else if !found {
		return contractprojects.Snapshots{}, notFound("project not found")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,repository,branch,commit_sha,entry_point,language,runtime,default_branch,stars,forks,file_count,total_bytes,created_at,is_active FROM project_snapshots WHERE project_id=? AND owner_id=? AND state='ready' ORDER BY created_at DESC,id DESC`, key.ProjectID, key.OwnerID)
	if err != nil {
		return contractprojects.Snapshots{}, err
	}
	defer rows.Close()
	list := contractprojects.Snapshots{Snapshots: make([]contractprojects.Snapshot, 0, 5)}
	for rows.Next() {
		var snapshot contractprojects.Snapshot
		var active int
		if err := rows.Scan(&snapshot.ID, &snapshot.Repository, &snapshot.Branch, &snapshot.Commit, &snapshot.EntryPoint, &snapshot.Language, &snapshot.Runtime, &snapshot.DefaultBranch, &snapshot.Stars, &snapshot.Forks, &snapshot.FileCount, &snapshot.TotalBytes, &snapshot.CreatedAt, &active); err != nil {
			return contractprojects.Snapshots{}, err
		}
		snapshot.Active = active != 0
		list.Snapshots = append(list.Snapshots, snapshot)
	}
	return list, rows.Err()
}

func (s *Service) activateSnapshot(ctx context.Context, key contractprojects.SnapshotKey) (contractprojects.Activated, error) {
	var activated bool
	err := tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_snapshots WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, key.SnapshotID, key.ProjectID, key.OwnerID).Scan(&exists); err != nil || exists == 0 {
			return err
		}
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`UPDATE project_snapshots SET is_active=0 WHERE project_id=? AND owner_id=? AND state='ready'`, []any{key.ProjectID, key.OwnerID}},
			{`UPDATE project_snapshots SET is_active=1 WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, []any{key.SnapshotID, key.ProjectID, key.OwnerID}},
			{`UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, []any{key.ProjectID, key.OwnerID}},
		} {
			if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
				return err
			}
		}
		activated = true
		return nil
	})
	return contractprojects.Activated{Activated: activated && err == nil}, err
}

// snapshotMetadata is what a Snapshot keeps about where it came from and what
// the scan found in it, as metadata_json.
type snapshotMetadata struct {
	Repository contractprojects.Repository `json:"repository"`
	Scan       scan.Result                 `json:"scan"`
}

// setEntryPoint records the Entrypoint the Account confirmed for the Project's
// active Snapshot, when it is one of the candidates the scan found.
func (s *Service) setEntryPoint(ctx context.Context, entry contractprojects.SetEntryPoint) (contractprojects.Empty, error) {
	project, found, err := getProject(ctx, s.db, entry.ProjectID, entry.OwnerID)
	switch {
	case err != nil:
		return contractprojects.Empty{}, err
	case !found:
		return contractprojects.Empty{}, notFound("project not found")
	case project.SnapshotID != entry.SnapshotID:
		return contractprojects.Empty{}, invalid("entrypoint selection must target the latest snapshot")
	}
	var metadata snapshotMetadata
	if err := json.Unmarshal([]byte(project.MetadataJSON), &metadata); err != nil {
		return contractprojects.Empty{}, invalid("project scan metadata is unavailable")
	}
	if !metadata.Scan.AcceptsEntrypoint(entry.EntryPoint) {
		return contractprojects.Empty{}, invalid("entrypoint must match a detected candidate")
	}
	return contractprojects.Empty{}, tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET entry_point=? WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, entry.EntryPoint, entry.SnapshotID, entry.ProjectID, entry.OwnerID)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return notFound("project snapshot not found")
		}
		_, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, entry.ProjectID, entry.OwnerID)
		return err
	})
}

// snapshotFiles returns one page of a ready Snapshot's files, ordered by path,
// with at most MaxBytes of content. The first file starts at ContentOffset; a
// file that does not fit is cut, and the caller asks for it again from where
// the cut was.
func (s *Service) snapshotFiles(ctx context.Context, page contractprojects.SnapshotFiles) (contractprojects.Files, error) {
	if page.Offset < 0 || page.ContentOffset < 0 {
		return contractprojects.Files{}, invalid("file page offsets cannot be negative")
	}
	limit := page.Limit
	if limit <= 0 {
		limit = contractprojects.DefaultFilePage
	}
	limit = min(limit, contractprojects.MaxFilePage)
	budget := page.MaxBytes
	if budget <= 0 || budget > contractprojects.DefaultPageBytes {
		budget = contractprojects.DefaultPageBytes
	}
	if _, found, err := getProject(ctx, s.db, page.ProjectID, page.OwnerID); err != nil {
		return contractprojects.Files{}, err
	} else if !found {
		return contractprojects.Files{}, notFound("project not found")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.path,f.mode,f.sha,f.content FROM project_files f JOIN project_snapshots s ON s.id=f.snapshot_id WHERE s.id=? AND s.project_id=? AND s.owner_id=? AND s.state='ready' ORDER BY f.path LIMIT ? OFFSET ?`, page.SnapshotID, page.ProjectID, page.OwnerID, limit+1, page.Offset)
	if err != nil {
		return contractprojects.Files{}, err
	}
	defer rows.Close()
	result := contractprojects.Files{Files: []contractprojects.File{}}
	for rows.Next() {
		if len(result.Files) == limit {
			result.More = true
			break
		}
		var file contractprojects.File
		var content []byte
		if err := rows.Scan(&file.Path, &file.Mode, &file.Hash, &content); err != nil {
			return contractprojects.Files{}, err
		}
		file.Size = int64(len(content))
		if len(result.Files) == 0 {
			file.ContentOffset = min(page.ContentOffset, len(content))
		}
		content = content[file.ContentOffset:]
		if len(content) > budget {
			if len(result.Files) > 0 {
				result.More = true // it starts the next page
				break
			}
			file.Content = content[:budget]
			result.Files = append(result.Files, file)
			result.More = true
			break
		}
		budget -= len(content)
		file.Content = content
		result.Files = append(result.Files, file)
	}
	return result, rows.Err()
}

func newID() string { return uuid.NewString() }
