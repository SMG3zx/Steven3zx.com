package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// deployOnce queues a Deploy of build through the API, waits until it is healthy,
// and returns the new Deployment's ID.
func (a *deployApp) deployOnce(t *testing.T, build string) string {
	t.Helper()
	response := perform(a.handler, http.MethodPost, a.deployPath(), `{"build_id":"`+build+`","agent_id":"`+a.agent+`"}`, a.session)
	if response.Code != http.StatusAccepted {
		t.Fatalf("deploy status = %d, body = %s", response.Code, response.Body.String())
	}
	var queued struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil || queued.ID == "" {
		t.Fatalf("queued = %+v, %v", queued, err)
	}
	a.waitState(t, queued.ID, "healthy")
	return queued.ID
}

func TestRedeployRollbackAndStopThroughTheAPI(t *testing.T) {
	env := newDeployApp(t)
	build := env.addBuild("passed", pageModule)
	first := env.deployOnce(t, build)
	second := env.deployOnce(t, build)

	rollback := perform(env.handler, http.MethodPost, "/api/deployments/"+first+"/rollback", "", env.session)
	if rollback.Code != http.StatusAccepted {
		t.Fatalf("rollback status = %d, body = %s", rollback.Code, rollback.Body.String())
	}
	var rolled struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rollback.Body.Bytes(), &rolled); err != nil || rolled.ID == "" {
		t.Fatalf("rollback = %s, %v", rollback.Body.String(), err)
	}
	env.waitState(t, rolled.ID, "healthy")
	env.fixture.Eventually("the rolled back deployment to be replaced", func() bool {
		read := perform(env.handler, http.MethodGet, "/api/deployments/"+second, "", env.session)
		return strings.Contains(read.Body.String(), `"reason":"rolled back"`)
	})

	if other := perform(env.handler, http.MethodPost, "/api/deployments/"+rolled.ID+"/stop", "", env.intruder); other.Code != http.StatusNotFound {
		t.Fatalf("another Account stopped the Deployment: %d %s", other.Code, other.Body.String())
	}
	stop := perform(env.handler, http.MethodPost, "/api/deployments/"+rolled.ID+"/stop", "", env.session)
	if stop.Code != http.StatusOK || !strings.Contains(stop.Body.String(), `"state":"stopped"`) {
		t.Fatalf("stop status = %d, body = %s", stop.Code, stop.Body.String())
	}
	read := perform(env.handler, http.MethodGet, "/api/deployments/"+rolled.ID, "", env.session)
	if !strings.Contains(read.Body.String(), `"state":"stopped"`) || !strings.Contains(read.Body.String(), `"reason":"removed"`) {
		t.Fatalf("stopped Deployment = %s", read.Body.String())
	}
}
