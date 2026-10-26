// Package firecracker is a minimal client for the Firecracker REST API.
// Each Firecracker process serves its API on its own Unix socket.
package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// APIError is returned when Firecracker answers with a non-2xx status.
type APIError struct {
	Status int
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("firecracker %s: status %d: %s", e.Path, e.Status, e.Body)
}

// Client talks to one Firecracker process over its API socket.
type Client struct {
	http *http.Client
}

// NewClient returns a Client for the API socket at socketPath.
func NewClient(socketPath string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 10 * time.Second}}
}

type machineConfig struct {
	VCPUCount  int `json:"vcpu_count"`
	MemSizeMiB int `json:"mem_size_mib"`
}

type bootSource struct {
	KernelImagePath string `json:"kernel_image_path"`
	BootArgs        string `json:"boot_args,omitempty"`
}

type drive struct {
	DriveID      string `json:"drive_id"`
	PathOnHost   string `json:"path_on_host"`
	IsRootDevice bool   `json:"is_root_device"`
	IsReadOnly   bool   `json:"is_read_only"`
}

type action struct {
	ActionType string `json:"action_type"`
}

type vmState struct {
	State string `json:"state"`
}

// ConfigureMachine sets the vCPU count and memory size.
func (c *Client) ConfigureMachine(ctx context.Context, vcpus, memMiB int) error {
	return c.do(ctx, http.MethodPut, "/machine-config", machineConfig{VCPUCount: vcpus, MemSizeMiB: memMiB})
}

// SetBootSource sets the guest kernel image and kernel command line.
func (c *Client) SetBootSource(ctx context.Context, kernelPath, bootArgs string) error {
	return c.do(ctx, http.MethodPut, "/boot-source", bootSource{KernelImagePath: kernelPath, BootArgs: bootArgs})
}

// SetRootDrive attaches a writable root block device.
func (c *Client) SetRootDrive(ctx context.Context, driveID, pathOnHost string) error {
	return c.do(ctx, http.MethodPut, "/drives/"+driveID, drive{
		DriveID:      driveID,
		PathOnHost:   pathOnHost,
		IsRootDevice: true,
		IsReadOnly:   false,
	})
}

// Action runs an instance action such as "InstanceStart" or "SendCtrlAltDel".
func (c *Client) Action(ctx context.Context, actionType string) error {
	return c.do(ctx, http.MethodPut, "/actions", action{ActionType: actionType})
}

// SetVMState pauses ("Paused") or resumes ("Resumed") the microVM.
func (c *Client) SetVMState(ctx context.Context, state string) error {
	return c.do(ctx, http.MethodPatch, "/vm", vmState{State: state})
}

func (c *Client) do(ctx context.Context, method, path string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://firecracker"+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("firecracker %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &APIError{Status: resp.StatusCode, Path: path, Body: strings.TrimSpace(string(msg))}
}

// WaitForSocket blocks until the API socket at path accepts connections, or ctx ends.
func WaitForSocket(ctx context.Context, path string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for firecracker socket %s: %w", path, ctx.Err())
		case <-ticker.C:
		}
	}
}
