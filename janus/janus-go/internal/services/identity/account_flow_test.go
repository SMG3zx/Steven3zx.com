package identity_test

import (
	"context"
	"encoding/json"
	"janus/internal/domain/agent"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nats.go/micro"

	"janus/cmd/janus/fleetfixture"
	"janus/internal/adapters/identitynats"
	"janus/internal/adapters/localstatetest"
	"janus/internal/adapters/runnertest"
	"janus/internal/contract/account"
	contractbuilds "janus/internal/contract/builds"
	contractid "janus/internal/contract/identity"
	contractprojects "janus/internal/contract/projects"
	"janus/internal/platform/accountevents"
	"janus/internal/platform/natstest"
	"janus/internal/platform/natsx"
	"janus/internal/services/identity"
)

const tokenSecret = "identity-flow-test-secret-long-enough"

// startIdentity starts the identity service on the database at path.
func startIdentity(t *testing.T, conn *nats.Conn, path string) *identity.Service {
	t.Helper()
	service := identity.New(identity.Config{DatabasePath: path, TokenSecret: tokenSecret})
	if err := service.Start(context.Background(), conn); err != nil {
		t.Fatalf("start the identity service: %v", err)
	}
	return service
}

// fakeActiveJobs answers the projects and builds requests with one unfinished
// import and one unfinished build, as an Account with running jobs has.
func fakeActiveJobs(t *testing.T, conn *nats.Conn) {
	t.Helper()
	service, err := micro.AddService(conn, micro.Config{Name: "fake-jobs", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop() })
	if err := natsx.AddEndpoint(service, "active-imports", contractprojects.SubjectActiveImports,
		func(context.Context, contractprojects.Owner) (contractprojects.ActiveImports, error) {
			return contractprojects.ActiveImports{Imports: []contractprojects.ActiveImport{{ID: "import-1", ProjectID: "project-1", Status: "queued"}}}, nil
		}); err != nil {
		t.Fatal(err)
	}
	if err := natsx.AddEndpoint(service, "active-builds", contractbuilds.SubjectActiveBuilds,
		func(context.Context, contractbuilds.Owner) (contractbuilds.ActiveBuilds, error) {
			return contractbuilds.ActiveBuilds{Builds: []contractbuilds.ActiveBuild{{ID: "build-1", ProjectID: "project-1", Status: "building"}}}, nil
		}); err != nil {
		t.Fatal(err)
	}
}

// confirmer stands in for a service that keeps records of an Account. It removes
// nothing itself and confirms once gate is closed, so the test decides when that
// service has finished.
func confirmer(t *testing.T, conn *nats.Conn, service string, gate <-chan struct{}) {
	t.Helper()
	js, err := jetstream.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := accountevents.Consume(js, service+"-fake", account.DeletingSubject,
		func(ctx context.Context, data []byte) error {
			var deleting account.Deleting
			if err := json.Unmarshal(data, &deleting); err != nil {
				return accountevents.Permanent(err)
			}
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
			cleared, err := json.Marshal(account.Cleared{OwnerID: deleting.UserID, Service: service})
			if err != nil {
				return err
			}
			_, err = js.Publish(ctx, account.ClearedSubject(service), cleared,
				jetstream.WithMsgID(account.ClearedMsgID(service, deleting.UserID)))
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Stop)
}

// TestDeletingAnAccountRemovesItsRecordsAcrossServicesAfterARestart deletes an
// Account with an unfinished import and build, and a running Agent. It restarts
// identity while the other services have not yet confirmed, then checks that the
// deletion resumes, the User is gone, and nothing is left on the tailnet.
func TestDeletingAnAccountRemovesItsRecordsAcrossServicesAfterARestart(t *testing.T) {
	hub := natstest.Start(t)
	conn := hub.Conn()
	ctx := context.Background()
	path := t.TempDir() + "/identity.db"

	service := startIdentity(t, conn, path)
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	ids := identitynats.NewGateway(conn)
	if err := ids.CreateLocalUser(ctx, contractid.CreateLocalUser{ID: "alice", Email: "alice@example.com", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}

	fixture := fleetfixture.Start(t, fleetfixture.Options{Hub: hub})
	enrollment := fixture.Enroll("alice", "laptop")
	fixture.Attach("alice", enrollment, "laptop", agent.NewRuntime(runnertest.NewFake(), localstatetest.NewMemory(), "", time.Now))
	if agents, err := fixture.Fleet.ListAgents(ctx, "alice"); err != nil || len(agents) != 1 {
		t.Fatalf("agents before the deletion = %+v, %v", agents, err)
	}

	fakeActiveJobs(t, conn)
	gate := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	confirmer(t, conn, "projects", gate)
	confirmer(t, conn, "builds", gate)

	refused, err := ids.DeleteAccount(ctx, "alice", false)
	if err != nil || refused.Status != contractid.AccountRefused || len(refused.ActiveJobs) != 2 {
		t.Fatalf("a deletion without cancelling the jobs = %+v, %v", refused, err)
	}
	started, err := ids.DeleteAccount(ctx, "alice", true)
	if err != nil || started.Status != contractid.AccountDeleting {
		t.Fatalf("a deletion that cancels the jobs = %+v, %v", started, err)
	}
	if found, err := ids.FindLocalUser(ctx, "alice@example.com"); err != nil || found.Found {
		t.Fatalf("the account can still sign in while it is deleting: %+v, %v", found, err)
	}

	// Restart identity mid-deletion. Its announcement and the services' confirmations
	// are kept, so the new process picks the deletion up where the old one left it.
	if err := service.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	service = startIdentity(t, conn, path)
	t.Cleanup(func() { _ = service.Stop(context.Background()) })
	close(gate)

	deadline := time.Now().Add(30 * time.Second)
	for {
		deletion, err := ids.DeleteAccount(ctx, "alice", true)
		if err != nil {
			t.Fatalf("resume the deletion: %v", err)
		}
		if deletion.Status == contractid.AccountMissing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the deletion did not finish: %+v", deletion)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if found, err := ids.FindUser(ctx, "alice"); err != nil || found.Found {
		t.Fatalf("the User still exists: %+v, %v", found, err)
	}
	if agents, err := fixture.Fleet.ListAgents(ctx, "alice"); err != nil || len(agents) != 0 {
		t.Fatalf("agents after the deletion = %+v, %v", agents, err)
	}
	if nodes, err := fixture.Tailnet.Nodes(ctx); err != nil || len(nodes) != 0 {
		t.Fatalf("tailnet nodes after the deletion = %+v, %v", nodes, err)
	}
}
