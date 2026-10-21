package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"janus/internal/contract"
	contractbuilds "janus/internal/contract/builds"
	contractfleet "janus/internal/contract/fleet"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/domain/artifact"
	"janus/internal/domain/templates"
	"janus/internal/platform/natsx"
)

// The WebAssembly targets an Agent runs.
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

// findBuild returns one of the Owner's WASM Builds from the builds service.
func (s *Service) findBuild(ctx context.Context, owner, buildID string) (contractbuilds.Build, bool, error) {
	var found contractbuilds.BuildFound
	err := s.callService(ctx, contractbuilds.SubjectFindBuild, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return found.Build, found.Found, err
}

// findCheck returns the Check of one of the Owner's WASM Builds.
func (s *Service) findCheck(ctx context.Context, owner, buildID string) (contractbuilds.Check, bool, error) {
	var found contractbuilds.CheckFound
	err := s.callService(ctx, contractbuilds.SubjectFindCheck, contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}, &found)
	return found.Check, found.Found, err
}

// artifactModule returns the WASM module of a complete Build's Artifact. The
// bytes come from the artifacts store; when the object is not there, the builds
// service copies it back from builds.db and the read is tried once more.
func (s *Service) artifactModule(ctx context.Context, owner, buildID string) ([]byte, error) {
	key := contractbuilds.BuildKey{OwnerID: owner, BuildID: buildID}
	var stored contractbuilds.ArtifactFound
	if err := s.callService(ctx, contractbuilds.SubjectFindArtifact, key, &stored); err != nil {
		return nil, err
	}
	if !stored.Found {
		return nil, invalid("WASM artifact not found")
	}
	data, found, err := natsx.GetObject(ctx, s.artifacts, stored.ObjectName)
	if err != nil {
		return nil, err
	}
	if !found {
		if err = s.callService(ctx, contractbuilds.SubjectRestoreArtifact, key, nil); err != nil {
			return nil, err
		}
		if data, found, err = natsx.GetObject(ctx, s.artifacts, stored.ObjectName); err != nil || !found {
			return nil, errors.Join(err, errors.New("the artifact could not be restored"))
		}
	}
	module, _, err := artifact.Module(data, stored.ArtifactName)
	if err != nil {
		return nil, fmt.Errorf("open the Artifact: %w", err)
	}
	return module, nil
}

// runnableBuild returns the Build a Deployment may run, with the reason it may
// not. Errors are meant for the Account.
func (s *Service) runnableBuild(ctx context.Context, owner, buildID string) (contractbuilds.Build, error) {
	build, found, err := s.findBuild(ctx, owner, buildID)
	if err != nil {
		return contractbuilds.Build{}, err
	}
	switch {
	case !found:
		return contractbuilds.Build{}, invalid("completed WASM build not found")
	case build.Status != contractbuilds.StatusComplete:
		return contractbuilds.Build{}, invalid("WASM build is not complete")
	case !wasiTargets[build.Target]:
		return contractbuilds.Build{}, invalid("only WASI WebAssembly targets can run")
	}
	return build, nil
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

// hostedDeployment returns the Deployment an Agent is handed for a complete
// WASM Build: its module, the module's SHA-256, and the template's limits.
func (s *Service) hostedDeployment(ctx context.Context, owner, projectID, buildID, deploymentID string) (contractfleet.HostedDeployment, error) {
	build, err := s.runnableBuild(ctx, owner, buildID)
	if err != nil {
		return contractfleet.HostedDeployment{}, err
	}
	module, err := s.artifactModule(ctx, owner, build.ID)
	if err != nil {
		return contractfleet.HostedDeployment{}, err
	}
	sum := sha256.Sum256(module)
	return contractfleet.HostedDeployment{ID: deploymentID, SHA256: hex.EncodeToString(sum[:]), Module: module,
		Limits: s.limits(ctx, owner, projectID, build.SnapshotID)}, nil
}
