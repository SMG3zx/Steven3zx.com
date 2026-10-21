package runs

import (
	"context"
	"errors"

	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/templates"
	"janus/internal/platform/natsx"
)

// The WebAssembly targets a Run can execute.
var wasiTargets = map[string]bool{"wasm32-wasip1": true, "wasip1-wasm": true}

// callService sends one request to another service. A refusal the service
// reported comes back as the contract error with its status and sentence.
func (s *Service) callService(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, s.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if errors.As(err, &remote) {
		return contract.FromStatus(remote.Status, remote.Message)
	}
	return err
}

// runnableBuild returns the Build a Run may execute: one of the Owner's complete
// WASI Builds. Its errors are meant for the Account.
func (s *Service) runnableBuild(ctx context.Context, owner, buildID string) (contractbuilds.Build, error) {
	var found contractbuilds.BuildFound
	if err := s.callService(ctx, contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found); err != nil {
		return contractbuilds.Build{}, err
	}
	switch {
	case !found.Found:
		return contractbuilds.Build{}, contract.Errorf(contract.StatusNotFound, "completed WASM build not found")
	case found.Build.Status != contractbuilds.StatusComplete:
		return contractbuilds.Build{}, contract.Errorf(contract.StatusInvalid, "WASM build is not complete")
	case !wasiTargets[found.Build.Target]:
		return contractbuilds.Build{}, contract.Errorf(contract.StatusInvalid, "only WASI WebAssembly targets can run")
	}
	return found.Build, nil
}

// artifact returns the name and the stored bytes of a complete Build's Artifact.
// The bytes come from the artifacts store; when the object is not there, the
// builds service copies it back from builds.db and the read is tried once more.
func (s *Service) artifact(ctx context.Context, owner, buildID string) (string, []byte, error) {
	key := contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}
	var stored contractbuilds.ArtifactFound
	if err := s.callService(ctx, contractbuilds.SubjectFindArtifact, key, &stored); err != nil {
		return "", nil, err
	}
	if !stored.Found {
		return "", nil, contract.Errorf(contract.StatusInvalid, "WASM artifact not found")
	}
	data, found, err := natsx.GetObject(ctx, s.artifacts, stored.ObjectName)
	if err != nil {
		return "", nil, err
	}
	if !found {
		if err = s.callService(ctx, contractbuilds.SubjectRestoreArtifact, key, nil); err != nil {
			return "", nil, err
		}
		if data, found, err = natsx.GetObject(ctx, s.artifacts, stored.ObjectName); err != nil || !found {
			return "", nil, errors.Join(err, errors.New("the artifact could not be restored"))
		}
	}
	return stored.ArtifactName, data, nil
}

// limits returns the resource limits of the template that built a Snapshot, or
// the defaults when the Snapshot cannot be found.
func (s *Service) limits(ctx context.Context, owner, projectID, snapshotID string) templates.Resources {
	defaults := templates.Resources{Fuel: templates.DefaultFuel, MemoryBytes: templates.DefaultMemoryBytes, Timeout: templates.DefaultTimeout}
	var snapshots contractprojects.Snapshots
	if err := s.callService(ctx, contractprojects.SubjectListSnapshots, contractprojects.ProjectKey{OwnerID: owner, ProjectID: projectID}, &snapshots); err != nil {
		return defaults
	}
	for _, snapshot := range snapshots.Snapshots {
		if snapshot.ID != snapshotID {
			continue
		}
		if template, found := templates.Find(snapshot.Language, snapshot.Runtime); found {
			return template.Resources
		}
	}
	return defaults
}
