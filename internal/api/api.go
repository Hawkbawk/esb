// Package api is the wire between the esb CLI and the esb daemon.
//
// The daemon owns the route table and the Caddy config; the CLI only asks it
// for changes. That is why `esb up` needs no sudo and why there is no shared
// directory of config fragments for the two halves to disagree about.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/hawkbawk/esb/internal/route"
)

// RouteRequest asks the daemon to route a label. The daemon picks the host
// port, because only it knows which ports its other routes already hold.
type RouteRequest struct {
	Label       string `json:"label"`
	SandboxPort int    `json:"sandboxPort"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// Client talks to the daemon over its unix socket.
type Client struct {
	http *http.Client
}

func NewClient(socketPath string) *Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &Client{
		http: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *Client) List() ([]route.Route, error) {
	var out []route.Route
	return out, c.do(http.MethodGet, "/routes", nil, &out)
}

func (c *Client) Upsert(label string, sandboxPort int) (route.Route, error) {
	var out route.Route
	err := c.do(http.MethodPost, "/routes", RouteRequest{Label: label, SandboxPort: sandboxPort}, &out)
	return out, err
}

func (c *Client) Remove(label string) error {
	return c.do(http.MethodDelete, "/routes/"+label, nil, nil)
}

func (c *Client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, "http://esb"+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// A refused or missing socket means the daemon isn't up, which is by
		// far the most common failure here, so name the fix.
		return fmt.Errorf("cannot reach the esb daemon: %w\nCheck: sudo launchctl print system/org.nixos.esb-daemon", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var e errorResponse
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("daemon returned %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
