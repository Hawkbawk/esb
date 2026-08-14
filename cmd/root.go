// Package cmd defines the usher command tree.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/usher/internal/api"
	"github.com/hawkbawk/usher/internal/config"
)

// version is set at build time via -ldflags.
var version = "dev"

func Execute() {
	root := &cobra.Command{
		Use:   "usher",
		Short: "Real HTTPS hostnames for local machines",
		Long: `usher gives anything running locally a real hostname.

A sandbox, an OrbStack VM, a container, or a bare ip:port becomes reachable at
https://<hostname>.<domain>, behind a publicly trusted wildcard cert from
Let's Encrypt via a DNS-01 challenge against deSEC. Because the cert is
publicly trusted, there is no CA to install anywhere, so calls between your
machines verify without disabling TLS checks.

usher runs a DNS server and a Caddy proxy to do it, and never creates or manages
the machines themselves.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newUpCmd(),
		newDownCmd(),
		newURLsCmd(),
		newDaemonCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "usher: %v\n", err)
		os.Exit(1)
	}
}

// load resolves the shared config and a client for the daemon that owns it.
func load() (*config.Config, *api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	client, err := api.NewClient(cfg.SocketPath())
	if err != nil {
		return nil, nil, err
	}
	return cfg, client, nil
}
