package projects

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"janus/actors/accounting"
	"janus/actors/persistence"
	actortrace "janus/actors/telemetry"
)

const (
	maxSnapshotBytes int64 = 256 << 20
	maxFileBytes     int64 = 16 << 20
	maxSnapshotFiles       = 50_000
	cloneDepth             = 50
	githubCloneBase        = "https://github.com/"
)

// Actor coordinates project operations and delegates durable state to the
// persistence actor.
type Actor struct {
	cloneBase   string
	persistence *goakt.PID
	accounting  *goakt.PID
	github      *GitHubClient
	jobs        *sync.WaitGroup
	slots       chan struct{}
	accepting   bool
}

// ImportWorker owns the long-running clone, scan, and snapshot persistence
// workflow for one repository import job.
type ImportWorker struct {
	cloneBase   string
	persistence *goakt.PID
	accounting  *goakt.PID
	jobs        *sync.WaitGroup
	slots       chan struct{}
}

var _ goakt.Actor = (*ImportWorker)(nil)

func (*ImportWorker) PreStart(*goakt.Context) error { return nil }

func (a *ImportWorker) Receive(rctx *goakt.ReceiveContext) {
	msg, ok := rctx.Message().(*RunImport)
	if !ok {
		rctx.Unhandled()
		return
	}
	ctx, span := actortrace.StartSpan(rctx.Context(), "actor.project_import.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	defer rctx.Shutdown()
	if a.jobs != nil {
		defer a.jobs.Done()
	}
	if a.slots != nil {
		defer func() { <-a.slots }()
	}
	if err := a.runImport(ctx, msg); err != nil {
		span.SetStatus(codes.Error, "repository import failed")
		actortrace.RecordError(span, err)
		a.audit(ctx, "error", "project.import.failed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID, "reason": "import_failed"})
	} else {
		a.audit(ctx, "info", "project.import.completed", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": msg.JobID, "repository": msg.Repository.FullName, "commit": msg.Commit})
	}
}

func (*ImportWorker) PostStop(*goakt.Context) error { return nil }

var _ goakt.Actor = (*Actor)(nil)

// New creates a project actor backed by persistencePID. If provided, the first
// accountingPID receives project audit events.
func New(persistencePID *goakt.PID, accountingPID ...*goakt.PID) *Actor {
	a := &Actor{persistence: persistencePID, github: NewGitHubClient(), cloneBase: githubCloneBase, jobs: &sync.WaitGroup{}, slots: make(chan struct{}, 2)}
	if len(accountingPID) > 0 {
		a.accounting = accountingPID[0]
	}
	return a
}
func (a *Actor) PreStart(*goakt.Context) error { a.accepting = true; return nil }

func (a *Actor) Receive(rctx *goakt.ReceiveContext) {
	ctx, span := actortrace.StartSpan(rctx.Context(), "actor.projects.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("actor.message.type", fmt.Sprintf("%T", rctx.Message())))
	switch msg := rctx.Message().(type) {
	case *Create:
		response, err := a.ask(ctx, &msg.Project)
		if err != nil {
			rctx.Response(&Created{Err: err})
			return
		}
		result, ok := response.(*persistence.ProjectCreated)
		if !ok {
			rctx.Response(&Created{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&Created{Project: result.Project, Err: result.Err})
	case *UpdateProject:
		response, err := a.ask(ctx, &persistence.UpdateProject{ID: msg.ProjectID, OwnerID: msg.OwnerID, Name: msg.Name, Description: msg.Description})
		if err != nil {
			rctx.Response(&Updated{Err: err})
			return
		}
		result, ok := response.(*persistence.ProjectUpdated)
		if !ok {
			rctx.Response(&Updated{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&Updated{Project: result.Project, Err: result.Err})
	case *DeleteProject:
		response, err := a.ask(ctx, &persistence.DeleteProject{ID: msg.ProjectID, OwnerID: msg.OwnerID})
		if err != nil {
			rctx.Response(&Deleted{Err: err})
			return
		}
		result, ok := response.(*persistence.ProjectDeleted)
		if !ok {
			rctx.Response(&Deleted{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&Deleted{Deleted: result.Deleted, Err: result.Err})
	case *List:
		response, err := a.ask(ctx, &persistence.ListProjects{OwnerID: msg.OwnerID})
		if err != nil {
			rctx.Response(&Listed{Err: err})
			return
		}
		result, ok := response.(*persistence.ProjectsListed)
		if !ok {
			rctx.Response(&Listed{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&Listed{Projects: result.Projects, Err: result.Err})
	case *Get:
		response, err := a.ask(ctx, &persistence.GetProject{ID: msg.ProjectID, OwnerID: msg.OwnerID})
		if err != nil {
			rctx.Response(&Found{Err: err})
			return
		}
		result, ok := response.(*persistence.ProjectFound)
		if !ok {
			rctx.Response(&Found{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&Found{Project: result.Project, Exists: result.Found, Err: result.Err})
	case *ListRepositories:
		token, connected, err := a.token(ctx, msg.OwnerID)
		if err != nil || !connected {
			rctx.Response(&RepositoriesListed{Connected: connected, Err: err})
			return
		}
		repos, err := a.github.Repositories(ctx, token)
		span.SetAttributes(attribute.Int("github.repositories.count", len(repos)))
		actortrace.RecordError(span, err)
		rctx.Response(&RepositoriesListed{Repositories: repos, Connected: true, Err: err})
	case *ListBranches:
		token, connected, err := a.token(ctx, msg.OwnerID)
		if err == nil && !connected {
			err = errors.New("connect GitHub before importing a repository")
		}
		var branches []Branch
		if err == nil {
			var repository Repository
			repository, err = a.github.Repository(ctx, token, msg.Repository)
			if err == nil {
				branches, err = a.github.Branches(ctx, token, msg.Repository)
			}
			if err == nil {
				sort.SliceStable(branches, func(i, j int) bool {
					return branches[i].Name == repository.DefaultBranch && branches[j].Name != repository.DefaultBranch
				})
			}
		}
		actortrace.RecordError(span, err)
		rctx.Response(&BranchesListed{Branches: branches, Err: err})
	case *ListCommits:
		token, connected, err := a.token(ctx, msg.OwnerID)
		if err == nil && !connected {
			err = errors.New("connect GitHub before importing a repository")
		}
		var commits []Commit
		if err == nil {
			commits, err = a.github.Commits(ctx, token, msg.Repository, msg.Branch)
		}
		actortrace.RecordError(span, err)
		rctx.Response(&CommitsListed{Commits: commits, Err: err})
	case *RequestImport:
		span.SetAttributes(
			attribute.String("project.id", msg.ProjectID),
			attribute.String("project.owner_id", msg.OwnerID),
			attribute.String("project.import.repository", msg.Repository),
			attribute.String("project.import.branch", msg.Branch),
			attribute.String("project.import.commit", msg.Commit),
		)
		jobID, err := a.startImport(rctx, msg)
		actortrace.RecordError(span, err)
		if err == nil {
			span.SetAttributes(attribute.String("project.import.job_id", jobID), attribute.String("project.import.repository", msg.Repository), attribute.String("project.import.commit", msg.Commit))
			a.audit(ctx, "info", "project.import.queued", map[string]string{"user_id": msg.OwnerID, "project_id": msg.ProjectID, "job_id": jobID, "repository": msg.Repository})
		}
		rctx.Response(&ImportRequested{JobID: jobID, Err: err})
	case *Job:
		response, err := a.ask(ctx, &persistence.FindImportJob{ID: msg.JobID, OwnerID: msg.OwnerID})
		if err != nil {
			rctx.Response(&JobFound{Err: err})
			return
		}
		result, ok := response.(*persistence.ImportJobFound)
		if !ok {
			rctx.Response(&JobFound{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&JobFound{Job: result.Job, Exists: result.Found, Err: result.Err})
	case *Disconnect:
		response, err := a.ask(ctx, &persistence.DisconnectGitHub{UserID: msg.OwnerID})
		if err == nil {
			result, ok := response.(*persistence.GitHubDisconnected)
			if !ok {
				err = fmt.Errorf("unexpected persistence response %T", response)
			} else {
				err = result.Err
			}
		}
		rctx.Response(&Disconnected{Err: err})
	case *Drain:
		a.accepting = false
		done := make(chan struct{})
		go func() { a.jobs.Wait(); close(done) }()
		select {
		case <-done:
			rctx.Response(&Drained{})
		case <-ctx.Done():
			rctx.Response(&Drained{Err: ctx.Err()})
		}
	case *SetEntryPoint:
		span.SetAttributes(
			attribute.String("project.id", msg.ProjectID),
			attribute.String("project.snapshot.id", msg.SnapshotID),
		)
		response, err := a.ask(ctx, &persistence.GetProject{ID: msg.ProjectID, OwnerID: msg.OwnerID})
		if err == nil {
			project, ok := response.(*persistence.ProjectFound)
			if !ok {
				err = fmt.Errorf("unexpected persistence response %T", response)
			} else if !project.Found {
				err = errors.New("project not found")
			} else if project.Project.SnapshotID != msg.SnapshotID {
				err = errors.New("entrypoint selection must target the latest snapshot")
			} else {
				err = validateEntrypoint(project.Project.MetadataJSON, msg.EntryPoint)
			}
		}
		if err == nil {
			response, err = a.ask(ctx, &persistence.SetProjectEntryPoint{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, SnapshotID: msg.SnapshotID, EntryPoint: msg.EntryPoint})
			if err == nil {
				if result, ok := response.(*persistence.ProjectEntryPointSet); ok {
					err = result.Err
				} else {
					err = fmt.Errorf("unexpected persistence response %T", response)
				}
			}
		}
		actortrace.RecordError(span, err)
		rctx.Response(&EntryPointSet{Err: err})
	case *History:
		response, err := a.ask(ctx, &persistence.ListSnapshots{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID})
		if err != nil {
			rctx.Response(&HistoryListed{Err: err})
			return
		}
		result, ok := response.(*persistence.SnapshotsListed)
		if !ok {
			rctx.Response(&HistoryListed{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&HistoryListed{Snapshots: result.Snapshots, Err: result.Err})
	case *ResumeImports:
		count, err := a.resumeImports(ctx, rctx.ActorSystem())
		rctx.Response(&ImportsResumed{Count: count, Err: err})
	case *RetryImport:
		jobID, err := a.retryImport(rctx, msg)
		rctx.Response(&ImportRequested{JobID: jobID, Err: err})
	case *DeleteAccount:
		result, err := a.deleteAccount(ctx, msg)
		result.Err = err
		rctx.Response(&result)
	case *ActivateSnapshot:
		response, err := a.ask(ctx, &persistence.ActivateSnapshot{OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, SnapshotID: msg.SnapshotID})
		if err != nil {
			rctx.Response(&SnapshotActivated{Err: err})
			return
		}
		result, ok := response.(*persistence.SnapshotActivated)
		if !ok {
			rctx.Response(&SnapshotActivated{Err: fmt.Errorf("unexpected persistence response %T", response)})
			return
		}
		rctx.Response(&SnapshotActivated{Activated: result.Activated, Err: result.Err})
	default:
		rctx.Unhandled()
	}
}

func (a *Actor) ask(ctx context.Context, message any) (any, error) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return goakt.Ask(c, a.persistence, message, 30*time.Second)
}
func (a *Actor) token(ctx context.Context, owner string) (string, bool, error) {
	response, err := a.ask(ctx, &persistence.FindGitHubToken{UserID: owner})
	if err != nil {
		return "", false, err
	}
	found, ok := response.(*persistence.GitHubTokenFound)
	if !ok {
		return "", false, fmt.Errorf("unexpected persistence response %T", response)
	}
	return found.Token, found.Connected, found.Err
}

func (a *Actor) updateJob(ctx context.Context, id, status string, progress int, reason string) error {
	response, err := a.ask(ctx, &persistence.UpdateImportJob{ID: id, Status: status, Progress: progress, Error: reason})
	if err != nil {
		return err
	}
	if result, ok := response.(*persistence.ImportJobCreated); ok {
		return result.Err
	}
	return fmt.Errorf("unexpected persistence response %T", response)
}

func (a *Actor) startImport(rctx *goakt.ReceiveContext, msg *RequestImport) (string, error) {
	if !a.accepting {
		return "", errors.New("project imports are shutting down")
	}
	if msg.OwnerID == "" || msg.ProjectID == "" || msg.Repository == "" || msg.Branch == "" || len(msg.Commit) != 40 {
		return "", errors.New("project, repository, branch and full commit SHA are required")
	}
	projectResponse, err := a.ask(rctx.Context(), &persistence.GetProject{ID: msg.ProjectID, OwnerID: msg.OwnerID})
	if err != nil {
		return "", err
	}
	project, ok := projectResponse.(*persistence.ProjectFound)
	if !ok {
		return "", fmt.Errorf("unexpected persistence response %T", projectResponse)
	}
	if !project.Found {
		return "", errors.New("project not found")
	}
	token, connected, err := a.token(rctx.Context(), msg.OwnerID)
	if err != nil {
		return "", err
	}
	if !connected {
		return "", errors.New("connect GitHub before importing a repository")
	}
	repository, err := a.github.Repository(rctx.Context(), token, msg.Repository)
	if err != nil {
		return "", err
	}
	if repository.FullName != msg.Repository {
		return "", errors.New("GitHub returned a different repository")
	}
	branches, err := a.github.Branches(rctx.Context(), token, msg.Repository)
	if err != nil {
		return "", err
	}
	branchFound := false
	for _, branch := range branches {
		if branch.Name == msg.Branch {
			branchFound = true
			break
		}
	}
	if !branchFound {
		return "", errors.New("selected branch does not belong to this repository")
	}
	commits, err := a.github.Commits(rctx.Context(), token, msg.Repository, msg.Branch)
	if err != nil {
		return "", err
	}
	commitFound := false
	for _, commit := range commits {
		if strings.EqualFold(commit.SHA, msg.Commit) {
			commitFound = true
			break
		}
	}
	if !commitFound {
		return "", errors.New("selected commit is not among the branch's 50 most recent commits")
	}
	jobID := uuid.NewString()
	response, err := a.ask(rctx.Context(), &persistence.CreateImportJob{ID: jobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, Repository: repository.FullName, Branch: msg.Branch, Commit: msg.Commit})
	if err != nil {
		return "", err
	}
	created, ok := response.(*persistence.ImportJobCreated)
	if !ok {
		return "", fmt.Errorf("unexpected persistence response %T", response)
	}
	if created.Err != nil {
		return "", created.Err
	}
	select {
	case a.slots <- struct{}{}:
	default:
		_ = a.updateJob(rctx.Context(), jobID, "failed", 100, "too many imports are already running")
		return "", errors.New("two repository imports are already running; retry when one finishes")
	}
	reserved := true
	defer func() {
		if reserved {
			<-a.slots
		}
	}()
	a.jobs.Add(1)
	workerActor := &ImportWorker{persistence: a.persistence, accounting: a.accounting, jobs: a.jobs, slots: a.slots, cloneBase: a.cloneBase}
	worker, err := rctx.ActorSystem().Spawn(rctx.Context(), "project-import-"+strings.ReplaceAll(jobID, "-", ""), workerActor)
	if err != nil {
		a.jobs.Done()
		_ = a.updateJob(rctx.Context(), jobID, "failed", 100, "import worker could not start")
		return "", err
	}
	workerCtx := context.WithoutCancel(rctx.Context())
	if err = goakt.Tell(workerCtx, worker, &RunImport{JobID: jobID, OwnerID: msg.OwnerID, ProjectID: msg.ProjectID, Repository: repository, Branch: msg.Branch, Commit: msg.Commit, Token: token}); err != nil {
		a.jobs.Done()
		_ = a.updateJob(rctx.Context(), jobID, "failed", 100, "import worker could not start")
		return "", err
	}
	reserved = false
	return jobID, nil
}

type fileData struct {
	path, mode string
	content    []byte
	hash       string
}
type snapshotMetadata struct {
	Repository Repository `json:"repository"`
	Scan       ScanResult `json:"scan"`
}

func readSnapshotTree(tree *object.Tree) ([]fileData, int64, error) {
	files := make([]fileData, 0)
	var total int64
	err := tree.Files().ForEach(func(file *object.File) error {
		name := path.Clean(file.Name)
		if name == "." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			return errors.New("repository contains an unsafe file path")
		}
		if len(files)+1 > maxSnapshotFiles {
			return errors.New("repository exceeds the 50,000 file snapshot limit")
		}
		if file.Mode == 0o160000 {
			contents := []byte(file.Hash.String())
			total += int64(len(contents))
			if total > maxSnapshotBytes {
				return errors.New("repository exceeds the 256 MiB snapshot limit")
			}
			files = append(files, fileData{path: name, mode: file.Mode.String(), content: contents, hash: file.Hash.String()})
			return nil
		}
		if file.Size > maxFileBytes {
			return fmt.Errorf("file %s exceeds the 16 MiB per-file limit", name)
		}
		total += file.Size
		if total > maxSnapshotBytes {
			return errors.New("repository exceeds the 256 MiB snapshot limit")
		}
		contents, err := file.Contents()
		if err != nil {
			return err
		}
		files = append(files, fileData{path: name, mode: file.Mode.String(), content: []byte(contents), hash: file.Hash.String()})
		return nil
	})
	return files, total, err
}

func (a *ImportWorker) updateJob(ctx context.Context, id, status string, progress int, reason string) error {
	response, err := a.ask(ctx, &persistence.UpdateImportJob{ID: id, Status: status, Progress: progress, Error: reason})
	if err != nil {
		return err
	}
	if result, ok := response.(*persistence.ImportJobCreated); ok {
		return result.Err
	}
	return fmt.Errorf("unexpected persistence response %T", response)
}
func (a *ImportWorker) fail(ctx context.Context, msg *RunImport, public string, err error) error {
	_ = a.updateJob(ctx, msg.JobID, "failed", 100, public)
	_ = discardStaging(ctx, a.persistence, msg.JobID)
	return fmt.Errorf("%s: %w", public, err)
}

func (a *ImportWorker) ask(ctx context.Context, message any) (any, error) {
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return goakt.Ask(c, a.persistence, message, 30*time.Second)
}

func (a *ImportWorker) audit(ctx context.Context, level, name string, fields map[string]string) {
	if a.accounting != nil {
		_ = goakt.Tell(ctx, a.accounting, &accounting.Event{Level: level, Name: name, Fields: fields})
	}
}

func (a *Actor) audit(ctx context.Context, level, name string, fields map[string]string) {
	if a.accounting == nil {
		return
	}
	_ = goakt.Tell(ctx, a.accounting, &accounting.Event{Level: level, Name: name, Fields: fields})
}
func validateEntrypoint(metadataJSON, entrypoint string) error {
	var metadata snapshotMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return errors.New("project scan metadata is unavailable")
	}
	if entrypoint == "" {
		return nil
	}
	for _, candidate := range metadata.Scan.Candidates {
		if candidate.Path == entrypoint {
			return nil
		}
	}
	return errors.New("entrypoint must match a detected candidate")
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (*Actor) PostStop(*goakt.Context) error { return nil }
