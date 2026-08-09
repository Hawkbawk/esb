// Package daemon runs the long-lived half of esb: the loopback alias, the DNS
// server for *.<domain>, Caddy on 443, and the gRPC API over a unix socket
// that the CLI drives.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/caddyserver/caddy/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/hawkbawk/esb/internal/api"
	"github.com/hawkbawk/esb/internal/api/esbv1"
	"github.com/hawkbawk/esb/internal/config"
	"github.com/hawkbawk/esb/internal/dnsd"
	"github.com/hawkbawk/esb/internal/netalias"
	"github.com/hawkbawk/esb/internal/proxy"
	"github.com/hawkbawk/esb/internal/route"
)

type Daemon struct {
	esbv1.UnimplementedRouteServiceServer

	cfg   *config.Config
	store *route.Store

	// applyMu serialises Caddy reloads so two concurrent CLI calls can't race
	// each other into loading stale route sets.
	applyMu sync.Mutex
}

// Run starts everything and blocks until SIGINT or SIGTERM.
func Run(cfg *config.Config) error {
	for _, dir := range []string{cfg.StateDir, cfg.CaddyStorageDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	store, err := route.NewStore(cfg.RoutesPath())
	if err != nil {
		return err
	}
	d := &Daemon{cfg: cfg, store: store}

	if err := proxy.LoadToken(cfg); err != nil {
		return err
	}

	// Both the DNS server and Caddy bind this address, so it has to exist
	// before either starts.
	if err := netalias.Ensure(cfg.ListenAddress); err != nil {
		return err
	}

	// Caddy needs a running instance before Load will attach a config to it.
	if err := caddy.Run(&caddy.Config{}); err != nil {
		return fmt.Errorf("starting caddy: %w", err)
	}

	dnsServer, err := dnsd.New(cfg.Domain, cfg.ListenAddress)
	if err != nil {
		return err
	}
	// 127.0.0.1 is where /etc/resolver/<domain> points the host resolver; the
	// alias is where sandbox microVMs reach us.
	if err := dnsServer.Start(cfg.DNSPort, "127.0.0.1", cfg.ListenAddress); err != nil {
		return err
	}
	defer dnsServer.Stop()

	if err := d.apply(); err != nil {
		return err
	}

	ln, err := d.listen()
	if err != nil {
		return err
	}
	srv := grpc.NewServer()
	esbv1.RegisterRouteServiceServer(srv, d)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Printf("esb: api server: %v", err)
		}
	}()

	log.Printf("esb: serving *.%s on %s (dns port %d, %d route(s))",
		cfg.Domain, cfg.ListenAddress, cfg.DNSPort, len(store.List()))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Print("esb: shutting down")

	srv.GracefulStop()
	return proxy.Stop()
}

// listen creates the API socket. It is world-writable because the whole point
// is that the unprivileged CLI can drive a root daemon; the socket only ever
// accepts loopback-local route changes, and anyone who can reach it can
// already run sbx.
func (d *Daemon) listen() (net.Listener, error) {
	path := d.cfg.SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// A stale socket from an unclean shutdown would make Listen fail.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o666); err != nil {
		return nil, err
	}
	return ln, nil
}

func (d *Daemon) ListRoutes(_ context.Context, _ *esbv1.ListRoutesRequest) (*esbv1.ListRoutesResponse, error) {
	stored := d.store.List()
	routes := make([]*esbv1.Route, 0, len(stored))
	for _, r := range stored {
		routes = append(routes, api.ToProto(r))
	}
	return &esbv1.ListRoutesResponse{Routes: routes}, nil
}

func (d *Daemon) UpsertRoute(_ context.Context, req *esbv1.UpsertRouteRequest) (*esbv1.UpsertRouteResponse, error) {
	host := route.Sanitize(req.GetHost())
	if host == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%q does not sanitise to a usable hostname", req.GetHost())
	}
	sandbox := route.Sanitize(req.GetSandbox())
	if sandbox == "" {
		return nil, status.Errorf(codes.InvalidArgument, "%q does not sanitise to a usable sandbox name", req.GetSandbox())
	}
	port := req.GetSandboxPort()
	if port < 1 || port > 65535 {
		return nil, status.Errorf(codes.InvalidArgument, "sandbox port %d out of range", port)
	}

	d.applyMu.Lock()
	defer d.applyMu.Unlock()

	rt, err := d.store.Upsert(host, sandbox, int(port), d.cfg.PortMin, d.cfg.PortMax)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := d.applyLocked(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &esbv1.UpsertRouteResponse{Route: api.ToProto(rt)}, nil
}

// RemoveRoute tolerates a host with no route: `esb down` should still tear
// the sandbox down when only the route is already gone.
func (d *Daemon) RemoveRoute(_ context.Context, req *esbv1.RemoveRouteRequest) (*esbv1.RemoveRouteResponse, error) {
	host := route.Sanitize(req.GetHost())

	d.applyMu.Lock()
	defer d.applyMu.Unlock()

	rt, existed, err := d.store.Remove(host)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := d.applyLocked(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !existed {
		return &esbv1.RemoveRouteResponse{}, nil
	}
	return &esbv1.RemoveRouteResponse{Route: api.ToProto(rt)}, nil
}

// RemoveSandboxRoutes tolerates a sandbox with no routes, for the same reason
// RemoveRoute does.
func (d *Daemon) RemoveSandboxRoutes(_ context.Context, req *esbv1.RemoveSandboxRoutesRequest) (*esbv1.RemoveSandboxRoutesResponse, error) {
	sandbox := route.Sanitize(req.GetSandbox())

	d.applyMu.Lock()
	defer d.applyMu.Unlock()

	removed, err := d.store.RemoveSandbox(sandbox)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := d.applyLocked(); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	routes := make([]*esbv1.Route, 0, len(removed))
	for _, r := range removed {
		routes = append(routes, api.ToProto(r))
	}
	return &esbv1.RemoveSandboxRoutesResponse{Routes: routes}, nil
}

func (d *Daemon) apply() error {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	return d.applyLocked()
}

func (d *Daemon) applyLocked() error {
	return proxy.Apply(d.cfg, d.store.List())
}
