package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/esb/internal/route"
	"github.com/hawkbawk/esb/internal/sbx"
)

func newRouteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "route <label> <sandbox-port>",
		Short: "Route a port on a sandbox that already exists",
		Long: `Route a port on an existing sandbox.

Separate from 'esb up' so you can re-route a sandbox you created another way,
or add a second port to one (Canvas plus a webpack dev server, say).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			label := route.Sanitize(args[0])
			sandboxPort, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("sandbox port %q is not a number", args[1])
			}

			exists, err := sbx.Exists(label)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("no sandbox named %q", label)
			}

			return RouteSandbox(label, sandboxPort)
		},
	}
}

// RouteSandbox publishes a host port for sandboxPort on the sandbox named
// label and records the route, so `<label>.<domain>` proxies to it.
func RouteSandbox(label string, sandboxPort int) error {
	cfg, client, err := load()
	if err != nil {
		return err
	}

	rt, err := client.Upsert(label, sandboxPort)
	if err != nil {
		return err
	}
	if err := sbx.Run("ports", label, "--publish",
		fmt.Sprintf("127.0.0.1:%d:%d", rt.HostPort, sandboxPort)); err != nil {
		return err
	}

	fmt.Printf("https://%s.%s -> 127.0.0.1:%d -> %d\n", label, cfg.Domain, rt.HostPort, sandboxPort)
	return nil
}
