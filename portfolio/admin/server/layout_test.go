package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func layoutRequest(t *testing.T, method, base, token, contentType, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, base+"/admin/api/layout", strings.NewReader(body))
	req.Header.Set("Cookie", cookieName+"="+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(out)
}

func TestLayoutRoundTripsPerUser(t *testing.T) {
	srv, token := newJobsTestServer(t)
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	code, body := layoutRequest(t, http.MethodGet, ts.URL, token, "", "")
	if code != http.StatusOK || !strings.Contains(body, `"positions":{}`) {
		t.Fatalf("fresh GET: %d %s", code, body)
	}

	saved := `{"positions":{"cpu":{"x":-480,"y":-330},"ctr-abc":{"x":800,"y":-470}}}`
	if code, body := layoutRequest(t, http.MethodPut, ts.URL, token, "application/json", saved); code != http.StatusNoContent {
		t.Fatalf("PUT: %d %s", code, body)
	}
	code, body = layoutRequest(t, http.MethodGet, ts.URL, token, "", "")
	if code != http.StatusOK {
		t.Fatalf("GET after save: %d", code)
	}
	var got layoutBody
	if err := json.NewDecoder(bytes.NewReader([]byte(body))).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Positions["cpu"] != (layoutPosition{X: -480, Y: -330}) || got.Positions["ctr-abc"] != (layoutPosition{X: 800, Y: -470}) {
		t.Fatalf("saved layout not returned: %+v", got.Positions)
	}
}

func TestLayoutRefusesBadRequests(t *testing.T) {
	srv, token := newJobsTestServer(t)
	ts := httptest.NewServer(srv.routes())
	t.Cleanup(ts.Close)

	if code, _ := layoutRequest(t, http.MethodPut, ts.URL, token, "text/plain", `{"positions":{}}`); code != http.StatusForbidden {
		t.Fatalf("cross-site PUT: %d, want 403", code)
	}
	if code, _ := layoutRequest(t, http.MethodPut, ts.URL, token, "application/json", `{"positions":`); code != http.StatusBadRequest {
		t.Fatalf("malformed PUT: %d, want 400", code)
	}
	long := `{"positions":{"` + strings.Repeat("x", maxLayoutIDLength+1) + `":{"x":1,"y":1}}}`
	if code, _ := layoutRequest(t, http.MethodPut, ts.URL, token, "application/json", long); code != http.StatusBadRequest {
		t.Fatalf("long id: %d, want 400", code)
	}
	if code, _ := layoutRequest(t, http.MethodGet, ts.URL, "", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("signed-out GET: %d, want 401", code)
	}
}
