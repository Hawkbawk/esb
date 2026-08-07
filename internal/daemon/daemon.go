// Package daemon runs the long-lived half of esb: the loopback alias, the DNS
// server for *.<domain>, Caddy on 443, and the unix-socket API the CLI drives.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"

	"github.com/hawkbawk/esb/internal/api"
	"github.com/hawkbawk/esb/internal/config"
	"github.com/hawkbawk/esb/internal/dnsd"
	"github.com/hawkbawk/esb/internal/netalias"
	"github.com/hawkbawk/esb/internal/proxy"
	"github.com/hawkbawk/esb/internal/route"
)

type Daemon struct {
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
	srv := &http.Server{Handler: d.routes()}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("esb: api server: %v", err)
		}
	}()

	log.Printf("esb: serving *.%s on %s (dns port %d, %d route(s))",
		cfg.Domain, cfg.ListenAddress, cfg.DNSPort, len(store.List()))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Print("esb: shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
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

func (d *Daemon) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /routes", d.handleList)
	mux.HandleFunc("POST /routes", d.handleUpsert)
	mux.HandleFunc("DELETE /routes/{label}", d.handleRemove)
	return mux
}

func (d *Daemon) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.store.List())
}

func (d *Daemon) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var req api.RouteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	label := route.Sanitize(req.Label)
	if label == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("%q does not sanitise to a usable hostname label", req.Label))
		return
	}
	if req.SandboxPort < 1 || req.SandboxPort > 65535 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("sandbox port %d out of range", req.SandboxPort))
		return
	}

	d.applyMu.Lock()
	defer d.applyMu.Unlock()

	rt, err := d.store.Upsert(label, req.SandboxPort, d.cfg.PortMin, d.cfg.PortMax)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.applyLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rt)
}

func (d *Daemon) handleRemove(w http.ResponseWriter, r *http.Request) {
	label := route.Sanitize(r.PathValue("label"))

	d.applyMu.Lock()
	defer d.applyMu.Unlock()

	// Removing a label with no route is not an error: `esb down` should still
	// tear the sandbox down when only the route is already gone.
	if _, err := d.store.Remove(label); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if err := d.applyLocked(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Daemon) apply() error {
	d.applyMu.Lock()
	defer d.applyMu.Unlock()
	return d.applyLocked()
}

func (d *Daemon) applyLocked() error {
	return proxy.Apply(d.cfg, d.store.List())
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": strings.TrimSpace(err.Error())})
}
