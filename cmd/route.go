package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/esb/internal/api"
	"github.com/hawkbawk/esb/internal/route"
	"github.com/hawkbawk/esb/internal/sbx"
)

func newRouteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "route <hostname> <sandbox> <sandbox-port>",
		Short: "Route a hostname to a port on a sandbox that already exists",
		Long: `Route a hostname to a port on an existing sandbox.

Separate from 'esb up' so you can re-route a sandbox you created another way,
or add extra hostnames to a sandbox already running (e.g. one per tenant in a
multi-tenant app that switches on Host).`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := route.Sanitize(args[0])
			sandbox := route.Sanitize(args[1])
			sandboxPort, err := strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("sandbox port %q is not a number", args[2])
			}

			exists, err := sbx.Exists(sandbox)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("no sandbox named %q", sandbox)
			}

			return RouteHost(host, sandbox, sandboxPort)
		},
	}

	cmd.AddCommand(newRouteRemoveCmd())
	return cmd
}

func newRouteRemoveCmd() *cobra.Command {
	var sandbox string

	cmd := &cobra.Command{
		Use:     "remove [hostname]",
		Aliases: []string{"rm"},
		Short:   "Unpublish a route, or every route for a sandbox",
		Long: `Remove a single hostname's route, or every route pointing at a sandbox.

Pass a hostname to drop just that route. Pass --sandbox to drop every
hostname routed to that sandbox at once, e.g. when destroying it.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if (len(args) == 1) == (sandbox != "") {
				return fmt.Errorf("pass exactly one of a hostname argument or --sandbox")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if sandbox != "" {
				return UnrouteSandbox(route.Sanitize(sandbox))
			}
			return UnrouteHost(route.Sanitize(args[0]))
		},
	}

	cmd.Flags().StringVar(&sandbox, "sandbox", "", "remove every route for this sandbox instead of a single hostname")
	return cmd
}

// RouteHost publishes a host port for sandboxPort on sandbox and records the
// route, so `<host>.<domain>` proxies to it.
func RouteHost(host, sandbox string, sandboxPort int) error {
	cfg, client, err := load()
	if err != nil {
		return err
	}

	rt, err := client.Upsert(host, sandbox, sandboxPort)
	if err != nil {
		return err
	}
	if err := sbx.Run("ports", sandbox, "--publish",
		fmt.Sprintf("127.0.0.1:%d:%d", rt.HostPort, sandboxPort)); err != nil {
		return err
	}

	fmt.Printf("https://%s.%s -> 127.0.0.1:%d -> %s:%d\n", host, cfg.Domain, rt.HostPort, sandbox, sandboxPort)
	return nil
}

// UnrouteHost unpublishes host's route, unpublishing the underlying host port
// too, unless another hostname is still using it (as multi-tenant sandboxes
// do), leaving the sandbox itself running.
func UnrouteHost(host string) error {
	_, client, err := load()
	if err != nil {
		return err
	}

	rt, existed, err := client.Remove(host)
	if err != nil {
		return err
	}
	if !existed {
		return fmt.Errorf("no route for %q", host)
	}

	if portStillUsed(client, rt) {
		fmt.Printf("removed the route for %q; port %d is still routed for other hostnames on %q\n",
			host, rt.HostPort, rt.Sandbox)
		return nil
	}

	// The sandbox may already be gone or the port already unpublished, which
	// is fine: the route is what this command really owns.
	if err := sbx.Run("ports", rt.Sandbox, "--unpublish",
		fmt.Sprintf("%d:%d", rt.HostPort, rt.SandboxPort)); err != nil {
		fmt.Printf("removed the route for %q; port unpublish reported: %v\n", host, err)
		return nil
	}

	fmt.Printf("removed the route for %q and unpublished port %d\n", host, rt.HostPort)
	return nil
}

// UnrouteSandbox removes every route pointing at sandbox and unpublishes
// every host port they held, leaving the sandbox itself running.
func UnrouteSandbox(sandbox string) error {
	_, client, err := load()
	if err != nil {
		return err
	}

	removed, err := client.RemoveSandbox(sandbox)
	if err != nil {
		return err
	}
	if len(removed) == 0 {
		return fmt.Errorf("no routes for sandbox %q", sandbox)
	}

	unpublished := map[int]bool{}
	for _, rt := range removed {
		if unpublished[rt.HostPort] {
			continue
		}
		unpublished[rt.HostPort] = true
		if err := sbx.Run("ports", sandbox, "--unpublish",
			fmt.Sprintf("%d:%d", rt.HostPort, rt.SandboxPort)); err != nil {
			fmt.Printf("removed route for %q; port %d unpublish reported: %v\n", rt.Host, rt.HostPort, err)
		}
	}

	fmt.Printf("removed %d route(s) for sandbox %q\n", len(removed), sandbox)
	return nil
}

// portStillUsed reports whether any remaining route still holds rt.HostPort,
// which happens when several hostnames share one sandbox port.
func portStillUsed(client *api.Client, rt route.Route) bool {
	routes, err := client.List()
	if err != nil {
		return false
	}
	for _, r := range routes {
		if r.HostPort == rt.HostPort {
			return true
		}
	}
	return false
}
