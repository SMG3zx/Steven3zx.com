// Package client talks to the indexd HTTP API.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Result struct {
	Path    string   `json:"path"`
	Offset  int      `json:"offset"`
	Snippet string   `json:"snippet"`
	Score   float64  `json:"score"`
	Sources []string `json:"sources"`
}

type Status struct {
	Files    int64 `json:"files"`
	Chunks   int64 `json:"chunks"`
	Embedded int64 `json:"embedded_chunks"`
}

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Search(ctx context.Context, q string, limit int) ([]Result, error) {
	var out struct {
		Results []Result `json:"results"`
	}
	v := url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}}
	if err := c.get(ctx, "/search?"+v.Encode(), &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	err := c.get(ctx, "/status", &s)
	return s, err
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("indexd is not reachable at %s (is it running?): %w", c.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("indexd: %s", e.Error)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
