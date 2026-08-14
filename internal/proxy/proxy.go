// Package proxy runs Caddy in-process.
//
// This binary *is* the xcaddy output: importing the desec DNS provider below
// is exactly what an xcaddy build would generate, so there is no separate
// custom Caddy binary to build, install, or keep in sync.
package proxy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"

	// The Caddyfile config adapter.
	_ "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	// Every standard Caddy module: the HTTP app, reverse_proxy, TLS, and the
	// Caddyfile directives for all of them.
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	// The deSEC DNS-01 solver, so `dns desec` resolves to a real module.
	_ "github.com/caddy-dns/desec"

	"github.com/hawkbawk/usher/internal/config"
	"github.com/hawkbawk/usher/internal/route"
)

// LoadToken puts the deSEC API token in the environment, where the Caddyfile's
// {env.DESEC_TOKEN} placeholder picks it up at provision time. launchd can't
// read a secret out of a file into a daemon's environment, so we do it here.
func LoadToken(cfg *config.Config) error {
	data, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return fmt.Errorf("unable to read deSEC at %s due to missing permissions", cfg.TokenFile)
		} else if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("deSEC token file %s does not exist", cfg.TokenFile)
		} else {
			return fmt.Errorf("reading deSEC token at %s: %w", cfg.TokenFile, err)
		}
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return fmt.Errorf("deSEC token at %s is empty", cfg.TokenFile)
	}
	return os.Setenv("DESEC_TOKEN", token)
}

// Apply adapts the rendered Caddyfile and hands it to the running Caddy
// instance. Caddy diffs the config itself, so calling this on every route
// change is cheap and does not drop in-flight connections or re-issue certs.
func Apply(cfg *config.Config, routes []route.Route) error {
	src := Caddyfile(cfg, routes)

	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("caddyfile adapter not registered")
	}

	cfgJSON, warnings, err := adapter.Adapt([]byte(src), map[string]any{"filename": "usher.caddyfile"})
	if err != nil {
		return fmt.Errorf("adapting caddyfile: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "usher: caddy warning: %s:%d %s\n", w.File, w.Line, w.Message)
	}

	if err := caddy.Load(cfgJSON, true); err != nil {
		return fmt.Errorf("loading caddy config: %w", err)
	}
	return nil
}

// Stop shuts Caddy down cleanly so it flushes storage on the way out.
func Stop() error { return caddy.Stop() }
