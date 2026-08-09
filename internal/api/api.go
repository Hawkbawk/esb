// Package api is the client half of the wire between the esb CLI and the esb
// daemon.
//
// The daemon owns the route table and the Caddy config; the CLI only asks it
// for changes. That is why `esb up` needs no sudo and why there is no shared
// directory of config fragments for the two halves to disagree about.
//
// The schema lives in proto/esb/v1/esb.proto; this package wraps the generated
// stubs so callers keep working in route.Route rather than protobuf types.
package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/hawkbawk/esb/internal/api/esbv1"
	"github.com/hawkbawk/esb/internal/route"
)

// callTimeout bounds a single RPC. An upsert reloads Caddy, which can take a
// moment on first run when the wildcard cert still has to be issued.
const callTimeout = 60 * time.Second

// Client talks to the daemon over its unix socket.
type Client struct {
	conn   *grpc.ClientConn
	routes esbv1.RouteServiceClient
}

// NewClient dials lazily: grpc.NewClient does not connect until the first RPC,
// so a missing daemon surfaces as a call error rather than a construction one.
func NewClient(socketPath string) (*Client, error) {
	conn, err := grpc.NewClient("unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dialing the esb daemon socket %s: %w", socketPath, err)
	}
	return &Client{conn: conn, routes: esbv1.NewRouteServiceClient(conn)}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) List() ([]route.Route, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	resp, err := c.routes.ListRoutes(ctx, &esbv1.ListRoutesRequest{})
	if err != nil {
		return nil, callErr(err)
	}
	out := make([]route.Route, 0, len(resp.GetRoutes()))
	for _, r := range resp.GetRoutes() {
		out = append(out, FromProto(r))
	}
	return out, nil
}

func (c *Client) Upsert(host, sandbox string, sandboxPort int) (route.Route, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	resp, err := c.routes.UpsertRoute(ctx, &esbv1.UpsertRouteRequest{
		Host:        host,
		Sandbox:     sandbox,
		SandboxPort: uint32(sandboxPort),
	})
	if err != nil {
		return route.Route{}, callErr(err)
	}
	return FromProto(resp.GetRoute()), nil
}

// Remove removes host's route, if it has one, and reports whether it did.
func (c *Client) Remove(host string) (route.Route, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	resp, err := c.routes.RemoveRoute(ctx, &esbv1.RemoveRouteRequest{Host: host})
	if err != nil {
		return route.Route{}, false, callErr(err)
	}
	if resp.GetRoute() == nil {
		return route.Route{}, false, nil
	}
	return FromProto(resp.GetRoute()), true, nil
}

// RemoveSandbox removes every route pointing at sandbox, e.g. when the
// sandbox itself is being destroyed.
func (c *Client) RemoveSandbox(sandbox string) ([]route.Route, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	resp, err := c.routes.RemoveSandboxRoutes(ctx, &esbv1.RemoveSandboxRoutesRequest{Sandbox: sandbox})
	if err != nil {
		return nil, callErr(err)
	}
	out := make([]route.Route, 0, len(resp.GetRoutes()))
	for _, r := range resp.GetRoutes() {
		out = append(out, FromProto(r))
	}
	return out, nil
}

// ToProto and FromProto keep route.Route as the type the rest of esb passes
// around. The store persists it as JSON, so making the generated type the
// storage type would tie the on-disk format to the wire format.
func ToProto(r route.Route) *esbv1.Route {
	return &esbv1.Route{
		Host:        r.Host,
		Sandbox:     r.Sandbox,
		HostPort:    uint32(r.HostPort),
		SandboxPort: uint32(r.SandboxPort),
	}
}

func FromProto(r *esbv1.Route) route.Route {
	return route.Route{
		Host:        r.GetHost(),
		Sandbox:     r.GetSandbox(),
		HostPort:    int(r.GetHostPort()),
		SandboxPort: int(r.GetSandboxPort()),
	}
}

// callErr unwraps a gRPC status into something worth printing at a prompt. A
// refused or missing socket means the daemon isn't up, which is by far the
// most common failure here, so name the fix.
func callErr(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	if st.Code() == codes.Unavailable {
		return fmt.Errorf("cannot reach the esb daemon: %s\nCheck: sudo launchctl print system/org.nixos.esb-daemon", st.Message())
	}
	return errors.New(st.Message())
}
