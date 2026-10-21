// Package headscale is the Tailnet over the Headscale REST API. It makes
// the single-use pre-auth keys Agents enroll with, lists and removes their
// nodes, and installs the one policy that keeps the tailnet default-deny.
package headscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	contractfleet "janus/internal/contract/fleet"
)

const defaultUser = "janus"

// Config is what the adapter needs from the outside. It reads no environment.
type Config struct {
	// URL is the base URL of the Headscale server, such as http://127.0.0.1:8080.
	URL string
	// APIKey authenticates the adapter to the REST API. It is never logged.
	APIKey string
	// User is the Headscale user that owns the keys the adapter makes. It
	// defaults to "janus" and is created if it does not exist.
	User string
	// HTTPClient makes the requests. It defaults to a client with a 30 s timeout.
	HTTPClient *http.Client
}

// Key is a single-use pre-auth key that enrolls one node carrying Tag. ID is the
// key's ID on the control server, and Key is the secret the node joins with.
type Key struct {
	ID, Key string
	Tag     string
	Expires time.Time
}

// Node is a node on the tailnet. KeyID is the pre-auth key it joined with, and is
// empty for a node that was registered another way.
type Node struct {
	ID, Name, KeyID string
	Tags            []string
	Addresses       []string
	Online          bool
	LastSeen        time.Time
}

// Tailnet is the private network Agents join, run by a control server such as
// Headscale. The fleet service asks it for the keys an Agent enrolls with and for
// the nodes that joined, and removes a node to cut an Agent off. It never reaches
// an Agent itself: that is a connection over the network, made by other code.
//
// Whoever implements Tailnet enforces one access rule: nodes tagged
// contract/fleet's ControlTag may open connections to every other tagged node,
// and no other connection is allowed. So an Agent can reach neither the control
// plane nor another Account's Agent.
//
// An ID is whatever the control server calls a key or a node, as a string. A key
// is single-use: once a node has joined with it, or after it is expired, a second
// join fails. Removing or expiring something that does not exist is not an error,
// so a retry after a partial failure finishes the job.
type Tailnet interface {
	// CreatePreAuthKey makes a single-use key that enrolls one node carrying tag,
	// valid until expires.
	CreatePreAuthKey(ctx context.Context, tag string, expires time.Time) (Key, error)
	// ExpirePreAuthKey ends a key's validity at once. Nodes that already joined
	// with it stay.
	ExpirePreAuthKey(ctx context.Context, keyID string) error
	// Nodes lists every node on the tailnet. Online reflects the control server's
	// last report and may lag a disconnect by several seconds.
	Nodes(ctx context.Context) ([]Node, error)
	// DeleteNode removes a node, so it can no longer be reached.
	DeleteNode(ctx context.Context, nodeID string) error
}

// Client implements Tailnet over Headscale.
type Client struct {
	baseURL, apiKey, user string
	http                  *http.Client

	mu     sync.Mutex
	userID string
}

var _ Tailnet = (*Client)(nil)

// New returns a Client for the Headscale server in config.
func New(config Config) *Client {
	c := &Client{baseURL: strings.TrimRight(config.URL, "/"), apiKey: config.APIKey, user: config.User, http: config.HTTPClient}
	if c.user == "" {
		c.user = defaultUser
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	return c
}

// Prepare makes sure the Headscale user exists and installs the policy the Tailnet
// needs. Nodes tagged contractfleet.ControlTag may open connections to any
// tagged node. Every tagged node, an Agent included, may open connections to the
// control plane's leaf port and nothing else there. No other connection is
// allowed, so an Agent cannot reach another Agent. A tag need not be listed for a
// node to carry it, so enrolling an Account needs no policy change. Prepare is
// safe to call again.
func (c *Client) Prepare(ctx context.Context) error {
	if _, err := c.ensureUser(ctx); err != nil {
		return err
	}
	document, err := json.Marshal(map[string]any{
		"tagOwners": map[string][]string{contractfleet.ControlTag: {c.user + "@"}},
		"acls": []map[string]any{
			{"action": "accept", "src": []string{contractfleet.ControlTag}, "dst": []string{"autogroup:tagged:*"}},
			{"action": "accept", "src": []string{"autogroup:tagged"}, "dst": []string{fmt.Sprintf("%s:%d", contractfleet.ControlTag, contractfleet.AgentLeafPort)}},
		},
	})
	if err != nil {
		return err
	}
	if err = c.do(ctx, http.MethodPut, "/api/v1/policy", map[string]string{"policy": string(document)}, nil); err != nil {
		return fmt.Errorf("install the tailnet policy: %w", err)
	}
	return nil
}

func (c *Client) CreatePreAuthKey(ctx context.Context, tag string, expires time.Time) (Key, error) {
	userID, err := c.ensureUser(ctx)
	if err != nil {
		return Key{}, err
	}
	var reply struct {
		PreAuthKey struct {
			ID         string `json:"id"`
			Key        string `json:"key"`
			Expiration string `json:"expiration"`
		} `json:"preAuthKey"`
	}
	body := map[string]any{"user": userID, "reusable": false, "ephemeral": false, "expiration": expires.UTC().Format(time.RFC3339), "acl_tags": []string{tag}}
	if err = c.do(ctx, http.MethodPost, "/api/v1/preauthkey", body, &reply); err != nil {
		return Key{}, fmt.Errorf("create a pre-auth key: %w", err)
	}
	if reply.PreAuthKey.ID == "" || reply.PreAuthKey.Key == "" {
		return Key{}, errors.New("create a pre-auth key: the reply has no key")
	}
	return Key{ID: reply.PreAuthKey.ID, Key: reply.PreAuthKey.Key, Tag: tag, Expires: expires}, nil
}

func (c *Client) ExpirePreAuthKey(ctx context.Context, keyID string) error {
	if !numeric(keyID) {
		return nil // Headscale numbers its keys, so this names none
	}
	err := c.do(ctx, http.MethodPost, "/api/v1/preauthkey/expire", map[string]string{"id": keyID}, nil)
	if err != nil && !isMissing(err) {
		return fmt.Errorf("expire a pre-auth key: %w", err)
	}
	return nil
}

type node struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	IPAddresses []string `json:"ipAddresses"`
	Tags        []string `json:"tags"`
	Online      bool     `json:"online"`
	LastSeen    string   `json:"lastSeen"`
	PreAuthKey  *struct {
		ID string `json:"id"`
	} `json:"preAuthKey"`
}

func (c *Client) Nodes(ctx context.Context) ([]Node, error) {
	var reply struct {
		Nodes []node `json:"nodes"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/node", nil, &reply); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	nodes := make([]Node, 0, len(reply.Nodes))
	for _, n := range reply.Nodes {
		out := Node{ID: n.ID, Name: n.Name, Tags: n.Tags, Addresses: n.IPAddresses, Online: n.Online}
		if n.PreAuthKey != nil {
			out.KeyID = n.PreAuthKey.ID
		}
		if seen, err := time.Parse(time.RFC3339Nano, n.LastSeen); err == nil {
			out.LastSeen = seen.UTC()
		}
		nodes = append(nodes, out)
	}
	return nodes, nil
}

func (c *Client) DeleteNode(ctx context.Context, nodeID string) error {
	if !numeric(nodeID) {
		return nil // Headscale numbers its nodes, so this names none
	}
	err := c.do(ctx, http.MethodDelete, "/api/v1/node/"+url.PathEscape(nodeID), nil, nil)
	if err != nil && !isMissing(err) {
		return fmt.Errorf("delete a node: %w", err)
	}
	return nil
}

// numeric reports whether id is a number, the only kind of ID Headscale gives a
// key or a node. It rejects anything else with a 400, which is not worth a call.
func numeric(id string) bool {
	_, err := strconv.ParseUint(id, 10, 64)
	return err == nil
}

// ensureUser returns the ID of the Headscale user, creating the user if needed.
func (c *Client) ensureUser(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.userID != "" {
		return c.userID, nil
	}
	type user struct {
		ID string `json:"id"`
	}
	var found struct {
		Users []user `json:"users"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/user?name="+url.QueryEscape(c.user), nil, &found); err != nil {
		return "", fmt.Errorf("look up the Headscale user: %w", err)
	}
	if len(found.Users) > 0 {
		c.userID = found.Users[0].ID
		return c.userID, nil
	}
	var created struct {
		User user `json:"user"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/user", map[string]string{"name": c.user}, &created); err != nil {
		return "", fmt.Errorf("create the Headscale user: %w", err)
	}
	c.userID = created.User.ID
	return c.userID, nil
}

// apiError is a non-success reply. Its text holds the status and Headscale's
// message, and never the request or the API key.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("headscale answered %d: %s", e.Status, e.Message)
}

func isMissing(err error) bool {
	var reply *apiError
	if !errors.As(err, &reply) {
		return false
	}
	return reply.Status == http.StatusNotFound || strings.Contains(strings.ToLower(reply.Message), "not found")
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return err
	}
	if response.StatusCode >= 300 {
		var reply struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &reply)
		if reply.Message == "" {
			reply.Message = http.StatusText(response.StatusCode)
		}
		return &apiError{Status: response.StatusCode, Message: reply.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
