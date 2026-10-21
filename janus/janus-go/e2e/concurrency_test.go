package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManySignInsAtOnceAllSucceed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, _, _, handler := startJanusWith(t, ctx)
	server := httptest.NewServer(handler)
	defer server.Close()
	registerUser(t, server.URL)

	const signIns = 16
	var failures atomic.Int32
	var wg sync.WaitGroup
	started := time.Now()
	for range signIns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response, err := http.Post(server.URL+"/auth/login", "application/json", strings.NewReader(`{"email":"playwright@example.com","password":"correct horse battery staple"}`))
			if err != nil {
				failures.Add(1)
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failed := failures.Load(); failed != 0 {
		t.Fatalf("%d of %d concurrent sign-ins failed", failed, signIns)
	}
	t.Logf("%d concurrent sign-ins took %s", signIns, time.Since(started))
}
