package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newJobsTestServer(t *testing.T) (*server, string) {
	t.Helper()
	st, err := openStore(filepath.Join(t.TempDir(), "admin.db"), testKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	if err := st.createUser(ctx, "steven", "unused-hash"); err != nil {
		t.Fatal(err)
	}
	u, err := st.userByName(ctx, "steven")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := st.createSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srv := &server{store: st, ipLimiter: newLimiter(5, time.Minute), userLimiter: newLimiter(5, time.Minute)}
	return srv, token
}

func TestJobsStoreRoundTrip(t *testing.T) {
	srv, _ := newJobsTestServer(t)
	ctx := context.Background()

	created, err := srv.store.createJob(ctx, job{
		Company: "Acme", Title: "Engineer", Status: "saved", Notes: "referral",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedAt == 0 {
		t.Fatalf("createJob did not assign id and timestamps: %+v", created)
	}

	created.Status = "applied"
	created.Applied = "2026-10-01"
	updated, err := srv.store.updateJob(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Fatalf("updateJob changed created_at: %d -> %d", created.CreatedAt, updated.CreatedAt)
	}

	jobs, err := srv.store.listJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Status != "applied" || jobs[0].Applied != "2026-10-01" || jobs[0].Notes != "referral" {
		t.Fatalf("listJobs = %+v", jobs)
	}

	if err := srv.store.deleteJob(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.deleteJob(ctx, created.ID); !errors.Is(err, errNotFound) {
		t.Fatalf("deleting twice: %v, want errNotFound", err)
	}
	if _, err := srv.store.updateJob(ctx, job{ID: "missing", Company: "x", Title: "y", Status: "saved"}); !errors.Is(err, errNotFound) {
		t.Fatalf("updating a missing job: %v, want errNotFound", err)
	}
}

func TestJobsRoutesRequireSignInAndValidate(t *testing.T) {
	srv, token := newJobsTestServer(t)
	h := srv.routes()

	do := func(method, target, body, cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: cookie})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(http.MethodGet, "/admin/api/jobs", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("list without a session: %d", rec.Code)
	}

	valid := `{"company":"Acme","title":"Engineer","status":"saved","url":"https://example.com/job"}`
	if rec := do(http.MethodPost, "/admin/api/jobs", valid, token); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	bad := map[string]string{
		"missing title":  `{"company":"Acme","status":"saved"}`,
		"unknown status": `{"company":"Acme","title":"E","status":"hired"}`,
		"javascript url": `{"company":"Acme","title":"E","status":"saved","url":"javascript:alert(1)"}`,
		"bad date":       `{"company":"Acme","title":"E","status":"saved","applied":"yesterday"}`,
	}
	for name, body := range bad {
		if rec := do(http.MethodPost, "/admin/api/jobs", body, token); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, rec.Code, rec.Body)
		}
	}

	rec := do(http.MethodGet, "/admin/api/jobs", "", token)
	var out struct {
		Jobs []job `json:"jobs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Jobs) != 1 {
		t.Fatalf("list: %v %s", err, rec.Body)
	}

	if rec := do(http.MethodPut, "/admin/api/jobs/nope", valid, token); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: %d", rec.Code)
	}
}
