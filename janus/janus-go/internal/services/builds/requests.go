package builds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/templates"
	"janus/internal/platform/natsx"
	"janus/internal/platform/tursodb"
)

// requestBuild checks the Project and Snapshot with the projects service, then
// records the Build and queues its job, in one transaction.
func (s *Service) requestBuild(ctx context.Context, request contractbuilds.RequestBuild) (contractbuilds.BuildFound, error) {
	if s.closing.Load() {
		return contractbuilds.BuildFound{}, contract.Errorf(contract.StatusUnavailable, "WASM builds are shutting down")
	}
	if request.ID == "" || request.OwnerID == "" || request.ProjectID == "" {
		return contractbuilds.BuildFound{}, invalid("project is required")
	}
	if !templates.KnownTarget(request.Target) {
		return contractbuilds.BuildFound{}, invalid("unsupported WASM target")
	}
	snapshot, err := s.buildSnapshot(ctx, request)
	if err != nil {
		return contractbuilds.BuildFound{}, err
	}
	template, ok := templates.Buildable(snapshot.Language)
	if !ok {
		return contractbuilds.BuildFound{}, invalid("%s", unbuildableLanguageError(snapshot.Language))
	}
	if err = template.CheckTarget(request.Target); err != nil {
		return contractbuilds.BuildFound{}, invalid("%s", err.Error())
	}
	err = tursodb.InTx(ctx, s.db, func(tx *sql.Tx) error {
		return insertBuild(ctx, tx, request, snapshot.ID)
	})
	if err != nil {
		return contractbuilds.BuildFound{}, err
	}
	s.relay.Kick()
	s.audit(ctx, "info", "project.wasm.build.queued", map[string]string{"user_id": request.OwnerID, "project_id": request.ProjectID, "job_id": request.ID, "target": request.Target})
	build, found, err := getBuild(ctx, s.db, request.ID, request.OwnerID)
	return contractbuilds.BuildFound{Found: found, Build: build}, err
}

// buildSnapshot returns the Snapshot a Build compiles: the one the request names,
// or the Project's active one.
func (s *Service) buildSnapshot(ctx context.Context, request contractbuilds.RequestBuild) (contractprojects.Snapshot, error) {
	var project contractprojects.ProjectFound
	if err := natsx.Call(ctx, s.conn, contractprojects.SubjectGetProject, contractprojects.ProjectKey{OwnerID: request.OwnerID, ProjectID: request.ProjectID}, &project); err != nil {
		return contractprojects.Snapshot{}, err
	}
	if !project.Found {
		return contractprojects.Snapshot{}, invalid("project not found")
	}
	if request.SnapshotID == "" {
		if project.Project.SnapshotID == "" {
			return contractprojects.Snapshot{}, invalid("import a repository before building it")
		}
		request.SnapshotID = project.Project.SnapshotID
	}
	snapshot, found, err := s.snapshot(ctx, request.OwnerID, request.ProjectID, request.SnapshotID)
	if err != nil {
		return contractprojects.Snapshot{}, err
	}
	if !found {
		return contractprojects.Snapshot{}, invalid("snapshot not found")
	}
	return snapshot, nil
}

// snapshot returns one of a Project's Snapshots from the projects service.
func (s *Service) snapshot(ctx context.Context, owner, projectID, snapshotID string) (contractprojects.Snapshot, bool, error) {
	var list contractprojects.Snapshots
	if err := natsx.Call(ctx, s.conn, contractprojects.SubjectListSnapshots, contractprojects.ProjectKey{OwnerID: owner, ProjectID: projectID}, &list); err != nil {
		return contractprojects.Snapshot{}, false, err
	}
	for _, snapshot := range list.Snapshots {
		if snapshot.ID == snapshotID {
			return snapshot, true, nil
		}
	}
	return contractprojects.Snapshot{}, false, nil
}

// retryBuild starts a new Build of a failed Build's Snapshot.
func (s *Service) retryBuild(ctx context.Context, key contractbuilds.BuildKey) (contractbuilds.BuildFound, error) {
	build, found, err := getBuild(ctx, s.db, key.BuildID, key.OwnerID)
	switch {
	case err != nil:
		return contractbuilds.BuildFound{}, err
	case !found:
		return contractbuilds.BuildFound{}, notFound("WASM build not found")
	case build.Status != contractbuilds.StatusFailed:
		return contractbuilds.BuildFound{}, invalid("only a failed WASM build can be retried")
	}
	return s.requestBuild(ctx, contractbuilds.RequestBuild{ID: uuid.NewString(), OwnerID: key.OwnerID, ProjectID: build.ProjectID, SnapshotID: build.SnapshotID, Target: build.Target})
}

func (s *Service) findBuild(ctx context.Context, key contractbuilds.BuildKey) (contractbuilds.BuildFound, error) {
	if key.OwnerID == "" {
		return contractbuilds.BuildFound{}, invalid("an owner is required")
	}
	build, found, err := getBuild(ctx, s.db, key.BuildID, key.OwnerID)
	return contractbuilds.BuildFound{Found: found, Build: build}, err
}

func (s *Service) findCheck(ctx context.Context, key contractbuilds.BuildKey) (contractbuilds.CheckFound, error) {
	if key.OwnerID == "" {
		return contractbuilds.CheckFound{}, invalid("an owner is required")
	}
	check, found, err := getCheck(ctx, s.db, key.BuildID, key.OwnerID)
	return contractbuilds.CheckFound{Found: found, Check: check}, err
}

// findArtifact says whether a complete Build has an Artifact, and where its
// object is. The bytes are read from the artifacts store, not from this reply.
func (s *Service) findArtifact(ctx context.Context, key contractbuilds.BuildKey) (contractbuilds.ArtifactFound, error) {
	if key.OwnerID == "" {
		return contractbuilds.ArtifactFound{}, invalid("an owner is required")
	}
	var name string
	err := s.db.QueryRowContext(ctx, `SELECT artifact_name FROM project_wasm_builds WHERE id=? AND owner_id=? AND status=? AND artifact IS NOT NULL`,
		key.BuildID, key.OwnerID, contractbuilds.StatusComplete).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return contractbuilds.ArtifactFound{}, nil
	}
	if err != nil {
		return contractbuilds.ArtifactFound{}, err
	}
	return contractbuilds.ArtifactFound{Found: true, ObjectName: key.BuildID, ArtifactName: name}, nil
}

// restoreArtifact copies a complete Build's Artifact from builds.db to the
// artifacts store, under the Build's ID.
func (s *Service) restoreArtifact(ctx context.Context, key contractbuilds.BuildKey) (contractbuilds.Empty, error) {
	if key.OwnerID == "" {
		return contractbuilds.Empty{}, invalid("an owner is required")
	}
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT artifact FROM project_wasm_builds WHERE id=? AND owner_id=? AND status=? AND artifact IS NOT NULL`,
		key.BuildID, key.OwnerID, contractbuilds.StatusComplete).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return contractbuilds.Empty{}, notFound("WASM artifact not found")
	}
	if err != nil {
		return contractbuilds.Empty{}, err
	}
	_, err = s.artifacts.PutBytes(ctx, key.BuildID, data)
	return contractbuilds.Empty{}, err
}

// deleteOwner cancels an Owner's running Builds and deletes its Builds, Checks
// and Artifacts. It is idempotent.
func (s *Service) deleteOwner(ctx context.Context, owner contractbuilds.Owner) (contractbuilds.Empty, error) {
	if owner.OwnerID == "" {
		return contractbuilds.Empty{}, invalid("an owner is required")
	}
	s.cancelOwner(owner.OwnerID)
	_, err := s.deleteWhere(ctx, "owner_id=?", owner.OwnerID)
	return contractbuilds.Empty{}, err
}

// deleteWhere deletes the Builds where selects and their Artifacts, once the
// rows are gone. A Build whose object cannot be deleted is left as an object
// with no record, which costs storage and nothing else.
func (s *Service) deleteWhere(ctx context.Context, where string, args ...any) (int, error) {
	ids, err := deleteBuilds(ctx, s.db, where, args...)
	if err != nil {
		return 0, fmt.Errorf("delete WASM builds: %w", err)
	}
	var errs []error
	for _, id := range ids {
		if err := natsx.DeleteObject(ctx, s.artifacts, id); err != nil {
			errs = append(errs, fmt.Errorf("delete artifact %s: %w", id, err))
		}
	}
	return len(ids), errors.Join(errs...)
}

// unbuildableLanguageError says why a Snapshot's Language cannot be built.
func unbuildableLanguageError(language string) error {
	if language == "" {
		return errors.New("no buildable language was detected in this snapshot")
	}
	return fmt.Errorf("WASM builds are not available for %s projects yet", language)
}

// languageLabel is the name of a Language as an Account sees it in a message.
func languageLabel(language string) string {
	switch {
	case strings.EqualFold(language, "Go"):
		return "Go"
	case strings.EqualFold(language, "JavaScript/TypeScript"):
		return "JavaScript/TypeScript"
	}
	return "Rust"
}
