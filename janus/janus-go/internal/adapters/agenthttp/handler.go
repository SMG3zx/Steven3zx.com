// Package agenthttp is the Agent's HTTP endpoint on the tailnet: the edge
// proxy sends each visitor request for a Deployment here, naming it in the
// agent.DeploymentHeader header, and the Agent answers it by running the module.
package agenthttp

import (
	"context"
	"errors"
	"io"
	"janus/internal/domain/agent"
	"net"
	"net/http"
	"strings"

	"janus/internal/domain/cgi"
)

// Handler serves Deployments through runtime. It answers only callers that
// authorize allows, by remote address; on the tailnet that is the control
// plane.
func Handler(runtime *agent.Runtime, authorize func(ctx context.Context, remoteAddr string) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := authorize(r.Context(), r.RemoteAddr); err != nil {
			http.Error(w, "only the Janus control plane may call this Agent", http.StatusForbidden)
			return
		}
		deploymentID := r.Header.Get(agent.DeploymentHeader)
		if deploymentID == "" {
			http.Error(w, "name the Deployment in the "+agent.DeploymentHeader+" header", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, agent.MaxBodyBytes+1))
		if err != nil {
			http.Error(w, "the request body could not be read", http.StatusBadRequest)
			return
		}
		header := r.Header.Clone()
		header.Del(agent.DeploymentHeader)
		request := agent.ServeRequest{Body: body, Request: cgi.Request{
			Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Host: r.Host,
			RemoteAddr: visitor(r), Header: header,
		}}
		response, err := runtime.Serve(r.Context(), deploymentID, request)
		switch {
		case errors.Is(err, agent.ErrAgentPaused):
			http.Error(w, "this Agent is paused", http.StatusServiceUnavailable)
		case errors.Is(err, agent.ErrAgentBusy):
			w.Header().Set("Retry-After", "1")
			http.Error(w, "this Agent is busy", http.StatusServiceUnavailable)
		case errors.Is(err, agent.ErrDeploymentNotFound):
			http.Error(w, "this Agent does not serve that Deployment", http.StatusNotFound)
		case errors.Is(err, agent.ErrBodyTooLarge):
			http.Error(w, "the request body is larger than 10 MiB", http.StatusRequestEntityTooLarge)
		case err != nil:
			http.Error(w, "the Agent could not serve the request", http.StatusInternalServerError)
		default:
			for name, values := range response.Header {
				for _, value := range values {
					w.Header().Add(name, value)
				}
			}
			w.WriteHeader(response.Status)
			_, _ = w.Write(response.Body)
		}
	})
}

// visitor is the address of whoever the request is for: the last address the
// edge added to X-Forwarded-For, or the caller itself.
func visitor(r *http.Request) string {
	if forwarded := r.Header.Values("X-Forwarded-For"); len(forwarded) > 0 {
		hops := strings.Split(forwarded[len(forwarded)-1], ",")
		return strings.TrimSpace(hops[len(hops)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
