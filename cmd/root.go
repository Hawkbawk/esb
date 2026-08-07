// Package cmd defines the esb command tree.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rhawk/esb/internal/api"
	"github.com/rhawk/esb/internal/config"
)

// version is set at build time via -ldflags.
var version = "dev"

func Execute() {
	root := &cobra.Command{
		Use:   "esb",
		Short: "Extended sandbox: name-based HTTPS routing for Docker Sandboxes",
		Long: `esb gives every Docker Sandbox a real hostname.

Sandboxes are microVMs, not containers, so they can't share a Docker network.
Each one publishes its app to a host port, which gives you ugly localhost:31847
URLs and no way for two sandboxes to reach each other over verifiable HTTPS.

esb runs a DNS server and a Caddy proxy that together make every sandbox
reachable at https://<label>.<domain>, with a publicly trusted wildcard cert
from Let's Encrypt via a DNS-01 challenge against deSEC. Because the cert is
publicly trusted, there is no CA to install in the host or in any sandbox
image, so cross-sandbox calls verify without disabling TLS checks.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newUpCmd(),
		newRouteCmd(),
		newDownCmd(),
		newURLsCmd(),
		newFromTemplateCmd(),
		newDaemonCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "esb: %v\n", err)
		os.Exit(1)
	}
}

// load resolves the shared config and a client for the daemon that owns it.
func load() (*config.Config, *api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	return cfg, api.NewClient(cfg.SocketPath()), nil
}
