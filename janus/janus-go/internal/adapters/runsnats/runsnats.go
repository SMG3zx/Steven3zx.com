// Package runsnats is the app.WASMRuns adapter that asks the runs service over
// NATS. It translates app types to the runs contract, and a refusal of the service
// (400, 404, 409) to an *app.ReplyError carrying the service's sentence.
package runsnats

import (
	"context"
	"errors"

	"github.com/nats-io/nats.go"

	"janus/internal/app"
	"janus/internal/contract"
	contractruns "janus/internal/contract/runs"
	"janus/internal/platform/natsx"
)

// Client is an app.WASMRuns over a NATS connection.
type Client struct {
	conn *nats.Conn
}

var _ app.WASMRuns = (*Client)(nil)

// New returns a Client that sends its requests on conn.
func New(conn *nats.Conn) *Client {
	return &Client{conn: conn}
}

// call sends one request and turns a refusal the service reported into the
// error the port documents.
func (c *Client) call(ctx context.Context, subject string, request, reply any) error {
	err := natsx.Call(ctx, c.conn, subject, request, reply)
	var remote *natsx.RemoteError
	if !errors.As(err, &remote) {
		return err
	}
	switch remote.Status {
	case contract.StatusInvalid, contract.StatusNotFound, contract.StatusConflict:
		return &app.ReplyError{Err: errors.New(remote.Message)}
	}
	return contract.FromStatus(remote.Status, remote.Message)
}

// Request asks for a run of the Account's Build and returns the run's ID.
func (c *Client) Request(ctx context.Context, owner, buildID string, args []string) (string, error) {
	var accepted contractruns.RunAccepted
	err := c.call(ctx, contractruns.SubjectRequestRun, contractruns.RequestRun{OwnerID: owner, BuildID: buildID, Args: args}, &accepted)
	return accepted.RunID, err
}

// Get returns one of the Account's runs. found is false when the run is not kept
// or is not the Account's.
func (c *Client) Get(ctx context.Context, owner, runID string) (app.Run, bool, error) {
	var found contractruns.RunFound
	if err := c.call(ctx, contractruns.SubjectGetRun, contractruns.RunKey{OwnerID: owner, RunID: runID}, &found); err != nil || !found.Found {
		return app.Run{}, false, err
	}
	run := found.Run
	return app.Run{ID: run.ID, BuildID: run.BuildID, Module: run.Module, Status: run.Status, Stdout: run.Stdout,
		Stderr: run.Stderr, Error: run.Error, Usage: run.Usage, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, true, nil
}
