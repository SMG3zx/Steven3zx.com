package persistence

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
)

// migrateProjectSnapshotActiveColumn upgrades databases created before snapshot
// activation was introduced. CREATE TABLE IF NOT EXISTS does not evolve an
// existing table, so the column must be added explicitly. Existing snapshots
// are initialized to the newest successful revision exactly once.
func migrateProjectSnapshotActiveColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(project_snapshots)`)
	if err != nil {
		return err
	}
	hasActive := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err = rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if name == "is_active" {
			hasActive = true
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if hasActive {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`ALTER TABLE project_snapshots ADD COLUMN is_active INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE project_snapshots AS snapshot SET is_active=1
		WHERE snapshot.state='ready' AND snapshot.id=(
			SELECT candidate.id FROM project_snapshots candidate
			WHERE candidate.project_id=snapshot.project_id AND candidate.state='ready'
			ORDER BY candidate.created_at DESC,candidate.id DESC LIMIT 1
		)`); err != nil {
		return err
	}
	return tx.Commit()
}

func deriveTokenKey(secret string) []byte {
	sum := sha256.Sum256([]byte("janus/github-oauth-token/v1\x00" + secret))
	return sum[:]
}

func (a *Actor) encryptToken(token string) ([]byte, error) {
	if len(a.key) != 32 {
		return nil, errors.New("GitHub token encryption is unavailable")
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(token), nil), nil
}

func (a *Actor) decryptToken(value []byte) (string, error) {
	if len(a.key) != 32 {
		return "", errors.New("GitHub token encryption is unavailable")
	}
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(value) < gcm.NonceSize() {
		return "", errors.New("stored GitHub token is invalid")
	}
	nonce, ciphertext := value[:gcm.NonceSize()], value[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("cannot decrypt stored GitHub token; reconnect GitHub")
	}
	return string(plain), nil
}

func (a *Actor) resolveGitHubLogin(ctx context.Context, msg *ResolveGitHubLogin) (GitHubLoginResolved, error) {
	var result GitHubLoginResolved
	err := a.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name FROM oauth_identities i JOIN users u ON u.id=i.user_id WHERE i.provider='github' AND i.subject=?`, msg.Subject).Scan(&result.ID, &result.Email, &result.DisplayName)
	if err == nil {
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if strings.TrimSpace(msg.Email) == "" {
		return result, errors.New("GitHub did not return a verified primary email")
	}
	var count int
	if err = a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email<>'' AND email=? COLLATE NOCASE`, strings.TrimSpace(msg.Email)).Scan(&count); err != nil {
		return result, err
	}
	if count != 0 {
		result.EmailInUse = true
		return result, nil
	}
	result.ID = newID()
	result.Email, result.DisplayName = strings.TrimSpace(msg.Email), strings.TrimSpace(msg.DisplayName)
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name) VALUES(?,?,?)`, result.ID, result.Email, result.DisplayName); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('github',?,?)`, msg.Subject, result.ID); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (a *Actor) connectGitHub(ctx context.Context, msg *ConnectGitHub) error {
	if msg.UserID == "" || msg.Subject == "" || msg.Token == "" {
		return errors.New("incomplete GitHub connection")
	}
	ciphertext, err := a.encryptToken(msg.Token)
	if err != nil {
		return err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var linkedUser string
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM oauth_identities WHERE provider='github' AND subject=?`, msg.Subject).Scan(&linkedUser)
	if err == nil && linkedUser != msg.UserID {
		return errors.New("GitHub account is connected to another Janus account")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var existingSubject string
	err = tx.QueryRowContext(ctx, `SELECT subject FROM github_connections WHERE user_id=?`, msg.UserID).Scan(&existingSubject)
	if err == nil && existingSubject != msg.Subject {
		return errors.New("a different GitHub account is already connected")
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO oauth_identities(provider,subject,user_id) VALUES('github',?,?) ON CONFLICT(provider,subject) DO NOTHING`, msg.Subject, msg.UserID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO github_connections(user_id,subject,token_ciphertext) VALUES(?,?,?) ON CONFLICT(user_id) DO UPDATE SET subject=excluded.subject,token_ciphertext=excluded.token_ciphertext,connected_at=CURRENT_TIMESTAMP`, msg.UserID, msg.Subject, ciphertext); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *Actor) findGitHubToken(ctx context.Context, userID string) GitHubTokenFound {
	var ciphertext []byte
	err := a.db.QueryRowContext(ctx, `SELECT token_ciphertext FROM github_connections WHERE user_id=?`, userID).Scan(&ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubTokenFound{}
	}
	if err != nil {
		return GitHubTokenFound{Err: err}
	}
	token, err := a.decryptToken(ciphertext)
	return GitHubTokenFound{Token: token, Connected: err == nil, Err: err}
}

func (a *Actor) createProject(ctx context.Context, msg *CreateProject) (Project, error) {
	_, err := a.db.ExecContext(ctx, `INSERT INTO projects(id,owner_id,name,description) VALUES(?,?,?,?)`, msg.ID, msg.OwnerID, strings.TrimSpace(msg.Name), strings.TrimSpace(msg.Description))
	if err != nil {
		return Project{}, err
	}
	result, err := a.getProject(ctx, msg.ID, msg.OwnerID)
	return result.Project, err
}

func (a *Actor) updateProject(ctx context.Context, msg *UpdateProject) (Project, error) {
	result, err := a.db.ExecContext(ctx, `UPDATE projects SET name=?,description=?,updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, strings.TrimSpace(msg.Name), strings.TrimSpace(msg.Description), msg.ID, msg.OwnerID)
	if err != nil {
		return Project{}, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return Project{}, errors.New("project not found")
	}
	found, err := a.getProject(ctx, msg.ID, msg.OwnerID)
	return found.Project, err
}

func (a *Actor) deleteProject(ctx context.Context, msg *DeleteProject) (bool, error) {
	var active int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_import_jobs WHERE project_id=? AND owner_id=? AND status IN ('queued','cloning','scanning','storing')`, msg.ID, msg.OwnerID).Scan(&active); err != nil {
		return false, err
	}
	if active > 0 {
		return false, errors.New("project has an import in progress")
	}
	result, err := a.db.ExecContext(ctx, `DELETE FROM projects WHERE id=? AND owner_id=?`, msg.ID, msg.OwnerID)
	if err != nil {
		return false, err
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

const projectSelect = `SELECT p.id,p.owner_id,p.name,p.description,p.created_at,p.updated_at,COALESCE(s.id,''),
COALESCE(s.repository_id,0),COALESCE(s.repository,''),COALESCE(s.default_branch,''),COALESCE(s.branch,''),COALESCE(s.commit_sha,''),COALESCE(s.entry_point,''),COALESCE(s.language,''),COALESCE(s.stars,0),COALESCE(s.forks,0),COALESCE(s.file_count,0),COALESCE(s.total_bytes,0),COALESCE(s.metadata_json,'{}'),
(SELECT COUNT(*) FROM project_snapshots all_s WHERE all_s.project_id=p.id AND all_s.state='ready')
FROM projects p LEFT JOIN project_snapshots s ON s.id=(SELECT id FROM project_snapshots WHERE project_id=p.id AND state='ready' AND is_active=1 LIMIT 1)`

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Description, &p.CreatedAt, &p.UpdatedAt, &p.SnapshotID, &p.RepositoryID, &p.Repository, &p.DefaultBranch, &p.Branch, &p.Commit, &p.EntryPoint, &p.Language, &p.Stars, &p.Forks, &p.FileCount, &p.TotalBytes, &p.MetadataJSON, &p.SnapshotCount)
	return p, err
}

func (a *Actor) listProjects(ctx context.Context, owner string) ([]Project, error) {
	rows, err := a.db.QueryContext(ctx, projectSelect+` WHERE p.owner_id=? ORDER BY p.updated_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := make([]Project, 0)
	for rows.Next() {
		p, scanErr := scanProject(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (a *Actor) getProject(ctx context.Context, id, owner string) (ProjectFound, error) {
	p, err := scanProject(a.db.QueryRowContext(ctx, projectSelect+` WHERE p.id=? AND p.owner_id=?`, id, owner))
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectFound{}, nil
	}
	if err != nil {
		return ProjectFound{}, err
	}
	return ProjectFound{Project: p, Found: true}, nil
}

func (a *Actor) createImportJob(ctx context.Context, msg *CreateImportJob) (string, error) {
	_, err := a.db.ExecContext(ctx, `INSERT INTO project_import_jobs(id,owner_id,project_id,repository,branch,commit_sha,status) SELECT ?,owner_id,id,?,?,?,'queued' FROM projects WHERE id=? AND owner_id=?`, msg.ID, msg.Repository, msg.Branch, msg.Commit, msg.ProjectID, msg.OwnerID)
	if err != nil {
		return "", err
	}
	var id string
	err = a.db.QueryRowContext(ctx, `SELECT id FROM project_import_jobs WHERE id=?`, msg.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("project not found")
	}
	return id, err
}

func (a *Actor) findImportJob(ctx context.Context, id, owner string) (ImportJobFound, error) {
	var job ImportJob
	err := a.db.QueryRowContext(ctx, `SELECT id,owner_id,project_id,repository,branch,commit_sha,status,progress,error,created_at,updated_at FROM project_import_jobs WHERE id=? AND owner_id=?`, id, owner).Scan(&job.ID, &job.OwnerID, &job.ProjectID, &job.Repository, &job.Branch, &job.Commit, &job.Status, &job.Progress, &job.Error, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportJobFound{}, nil
	}
	return ImportJobFound{Job: job, Found: err == nil}, err
}

func (a *Actor) beginSnapshot(ctx context.Context, msg *BeginSnapshot) error {
	_, err := a.db.ExecContext(ctx, `INSERT INTO project_snapshots(id,project_id,owner_id,repository_id,repository,branch,commit_sha,default_branch,language,stars,forks,metadata_json,job_id,state) SELECT ?,p.id,p.owner_id,?,?,?,?,?,?,?,?,?,?,'staging' FROM projects p WHERE p.id=? AND p.owner_id=?`, msg.ID, msg.RepositoryID, msg.Repository, msg.Branch, msg.Commit, msg.DefaultBranch, msg.Language, msg.Stars, msg.Forks, msg.MetadataJSON, msg.JobID, msg.ProjectID, msg.OwnerID)
	if err != nil {
		return err
	}
	var status string
	if err = a.db.QueryRowContext(ctx, `SELECT state FROM project_snapshots WHERE id=?`, msg.ID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return errors.New("project not found")
	}
	return err
}

func (a *Actor) addSnapshotFiles(ctx context.Context, msg *AddSnapshotFiles) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO project_files(snapshot_id,path,mode,content,sha) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, file := range msg.Files {
		if _, err = stmt.ExecContext(ctx, msg.SnapshotID, file.Path, file.Mode, file.Content, file.Hash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *Actor) completeSnapshot(ctx context.Context, msg *CompleteSnapshot) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=0 WHERE project_id=(SELECT project_id FROM project_snapshots WHERE id=?) AND state='ready'`, msg.SnapshotID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE project_snapshots SET entry_point=?,file_count=?,total_bytes=?,state='ready',is_active=1 WHERE id=? AND state='staging'`, msg.EntryPoint, msg.FileCount, msg.TotalBytes, msg.SnapshotID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("staging snapshot not found")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=(SELECT project_id FROM project_snapshots WHERE id=?)`, msg.SnapshotID); err != nil {
		return err
	}
	result, err = tx.ExecContext(ctx, `UPDATE project_import_jobs SET status='complete',progress=100,error='',updated_at=CURRENT_TIMESTAMP WHERE id=? AND status='storing'`, msg.JobID)
	if err == nil {
		if n, _ := result.RowsAffected(); n != 1 {
			err = errors.New("import was cancelled before it completed")
		}
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM project_snapshots WHERE project_id=(SELECT project_id FROM project_snapshots WHERE id=?) AND state='ready' AND id NOT IN (SELECT id FROM project_snapshots WHERE project_id=(SELECT project_id FROM project_snapshots WHERE id=?) AND state='ready' ORDER BY created_at DESC,id DESC LIMIT 5)`, msg.SnapshotID, msg.SnapshotID); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *Actor) listSnapshots(ctx context.Context, msg *ListSnapshots) ([]ProjectSnapshot, error) {
	project, err := a.getProject(ctx, msg.ProjectID, msg.OwnerID)
	if err != nil {
		return nil, err
	}
	if !project.Found {
		return nil, errors.New("project not found")
	}
	rows, err := a.db.QueryContext(ctx, `SELECT id,repository,branch,commit_sha,entry_point,language,default_branch,stars,forks,file_count,total_bytes,created_at,is_active FROM project_snapshots WHERE project_id=? AND owner_id=? AND state='ready' ORDER BY created_at DESC,id DESC`, msg.ProjectID, msg.OwnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshots := make([]ProjectSnapshot, 0, 5)
	for rows.Next() {
		var s ProjectSnapshot
		var active int
		if err = rows.Scan(&s.ID, &s.Repository, &s.Branch, &s.Commit, &s.EntryPoint, &s.Language, &s.DefaultBranch, &s.Stars, &s.Forks, &s.FileCount, &s.TotalBytes, &s.CreatedAt, &active); err != nil {
			return nil, err
		}
		s.Active = active != 0
		snapshots = append(snapshots, s)
	}
	return snapshots, rows.Err()
}

func (a *Actor) activateSnapshot(ctx context.Context, msg *ActivateSnapshot) (bool, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_snapshots WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, msg.SnapshotID, msg.ProjectID, msg.OwnerID).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=0 WHERE project_id=? AND owner_id=? AND state='ready'`, msg.ProjectID, msg.OwnerID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE project_snapshots SET is_active=1 WHERE id=? AND project_id=? AND owner_id=? AND state='ready'`, msg.SnapshotID, msg.ProjectID, msg.OwnerID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE projects SET updated_at=CURRENT_TIMESTAMP WHERE id=? AND owner_id=?`, msg.ProjectID, msg.OwnerID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Actor) listProjectFiles(ctx context.Context, msg *ListProjectFiles) ([]SnapshotFile, error) {
	if msg.Offset < 0 {
		return nil, errors.New("file page offset cannot be negative")
	}
	limit := msg.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	project, err := a.getProject(ctx, msg.ProjectID, msg.OwnerID)
	if err != nil {
		return nil, err
	}
	if !project.Found {
		return nil, errors.New("project not found")
	}
	rows, err := a.db.QueryContext(ctx, `SELECT f.path,f.mode,f.content,f.sha FROM project_files f JOIN project_snapshots s ON s.id=f.snapshot_id WHERE s.id=? AND s.project_id=? AND s.owner_id=? AND s.state='ready' ORDER BY f.path LIMIT ? OFFSET ?`, msg.SnapshotID, msg.ProjectID, msg.OwnerID, limit, msg.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := make([]SnapshotFile, 0)
	for rows.Next() {
		var f SnapshotFile
		if err = rows.Scan(&f.Path, &f.Mode, &f.Content, &f.Hash); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func (a *Actor) String() string { return fmt.Sprintf("PersistenceActor(%s)", a.path) }
