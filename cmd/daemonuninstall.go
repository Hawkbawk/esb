package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/usher/internal/config"
)

func newDaemonUninstallCmd() *cobra.Command {
	var purge bool

	cmd := &cobra.Command{
		Use:     "uninstall",
		Aliases: []string{"rm", "delete"},
		Short:   "Uninstall the daemon component of usher",
		Long: `Stop and remove the usher daemon installed by "usher daemon install".

Unloads and deletes the launchd job. With --purge, also removes
/etc/resolver/<domain>, the config file, and the state directory (routes,
Caddy storage, logs).

Must be run as root, since it removes files from /etc and
/Library/LaunchDaemons.

If you installed usher via nix, use the nix-darwin module instead of this
command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return fmt.Errorf("this command needs root; re-run with sudo")
			}

			if managed, detail := installedViaNix(); managed {
				return fmt.Errorf("usher looks like it's managed by nix-darwin (%s); use services.usher in your darwin configuration instead of `usher daemon uninstall`", detail)
			}

			if err := uninstallLaunchdJob(); err != nil {
				return err
			}

			// Config may already be gone or was never written by this
			// installer; that's fine, there's just nothing left to purge.
			cfg, cfgErr := config.Load()

			if cfgErr == nil {
				if err := os.Remove(filepath.Join("/etc/resolver", cfg.Domain)); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("removing resolver file: %w", err)
				}
			}

			if purge {
				if cfgErr == nil {
					if err := os.RemoveAll(cfg.StateDir); err != nil {
						return fmt.Errorf("removing state dir: %w", err)
					}
				}
				if err := os.Remove(config.DefaultPath); err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("removing config file: %w", err)
				}
			}

			fmt.Println("usher daemon uninstalled")
			return nil
		},
	}

	cmd.Flags().BoolVar(&purge, "purge", false, "also remove the config file, resolver file, and state directory (routes, Caddy storage, logs)")

	return cmd
}

// uninstallLaunchdJob unloads the launchd job (if loaded) and removes its
// plist. It tolerates the job already being gone, so re-running uninstall is
// safe.
func uninstallLaunchdJob() error {
	plistPath := filepath.Join("/Library/LaunchDaemons", launchdLabel+".plist")

	if launchctlIsLoaded() {
		if out, err := exec.Command("launchctl", "unload", "-w", plistPath).CombinedOutput(); err != nil {
			return fmt.Errorf("launchctl unload: %w: %s", err, out)
		}
	}

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing launchd plist: %w", err)
	}
	return nil
}
