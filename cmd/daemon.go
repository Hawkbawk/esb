package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/usher/internal/config"
	"github.com/hawkbawk/usher/internal/daemon"
	"github.com/hawkbawk/usher/internal/proxy"
	"github.com/hawkbawk/usher/internal/route"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the DNS server and HTTPS proxy in the foreground",
		Long: `Run the usher daemon in the foreground.

This is what the launchd job starts. It needs root: it adds the loopback
alias, binds 443, and reads the deSEC token.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			return daemon.Run(cfg)
		},
	}

	// Handy when the Caddyfile itself is the suspect: this needs no daemon,
	// no root, and no token.
	cmd.AddCommand(&cobra.Command{
		Use:   "config",
		Short: "Print the Caddyfile the daemon would generate",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			store, err := route.NewStore(cfg.RoutesPath())
			if err != nil {
				return err
			}
			fmt.Print(proxy.Caddyfile(cfg, store.List()))
			return nil
		},
	})

	cmd.AddCommand(newDaemonInstallCmd())
	cmd.AddCommand(newDaemonUninstallCmd())

	return cmd
}
