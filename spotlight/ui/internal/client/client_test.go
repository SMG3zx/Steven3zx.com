package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchAndStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "hello world" || r.URL.Query().Get("limit") != "3" {
			http.Error(w, `{"error":"bad query"}`, 400)
			return
		}
		w.Write([]byte(`{"query":"x","results":[{"path":"/a/b.go","snippet":"hi","sources":["keyword"]}]}`))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"files":2,"chunks":10,"embedded_chunks":9}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL + "/")
	res, err := c.Search(context.Background(), "hello world", 3)
	if err != nil || len(res) != 1 || res[0].Path != "/a/b.go" {
		t.Fatalf("res=%v err=%v", res, err)
	}
	st, err := c.Status(context.Background())
	if err != nil || st.Files != 2 || st.Embedded != 9 {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"search failed"}`, 500)
	}))
	if _, err := New(srv.URL).Search(context.Background(), "x", 1); err == nil || !strings.Contains(err.Error(), "search failed") {
		t.Fatalf("want server error, got %v", err)
	}
	srv.Close()
	if _, err := New(srv.URL).Status(context.Background()); err == nil || !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("want unreachable error, got %v", err)
	}
}
