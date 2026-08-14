package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/usher/internal/adapter"
	"github.com/hawkbawk/usher/internal/api"
	"github.com/hawkbawk/usher/internal/route"
)

func newDownCmd() *cobra.Command {
	var destroy bool

	cmd := &cobra.Command{
		Use:   "down <hostname>",
		Short: "Remove a hostname's route",
		Long: `Remove a hostname's route.

Only that hostname goes away. Other hostnames pointing at the same machine keep
working, and the machine itself is left alone.

Pass --destroy to also tear down the machine and every other route pointing at
it. That works for sbx and docker machines; usher refuses to delete an OrbStack
VM, since that's rarely what you meant.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostname, err := sanitizeHostname(args[0])
			if err != nil {
				return err
			}

			_, client, err := load()
			if err != nil {
				return err
			}
			defer client.Close()

			r, existed, err := client.Remove(hostname)
			if err != nil {
				return err
			}
			if !existed {
				return fmt.Errorf("no route named %q (see `usher urls`)", hostname)
			}

			a, err := adapter.For(r.Adapter)
			if err != nil {
				return err
			}

			stillUsed, err := machinePortStillRouted(client, r)
			if err != nil {
				return err
			}
			if err := a.Detach(cmd.Context(), r, stillUsed); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", hostname)

			if !destroy {
				return nil
			}
			return destroyMachine(cmd, client, a, r)
		},
	}

	cmd.Flags().BoolVar(&destroy, "destroy", false,
		"also remove the machine and every other route pointing at it")
	return cmd
}

// destroyMachine drops the machine's remaining routes before removing the
// machine, so nothing is left pointing at something that no longer exists.
func destroyMachine(cmd *cobra.Command, client *api.Client, a adapter.Adapter, r route.Route) error {
	if r.Adapter == route.Static {
		return fmt.Errorf("--destroy has nothing to do for a static route")
	}

	orphans, err := client.RemoveMachine(r.Adapter, r.Machine)
	if err != nil {
		return err
	}
	for _, o := range orphans {
		fmt.Printf("removed %s\n", o.Host)
	}

	if err := a.Destroy(cmd.Context(), r.Machine); err != nil {
		return err
	}
	fmt.Printf("destroyed %s %s\n", r.Adapter, r.Machine)
	return nil
}

// machinePortStillRouted reports whether another hostname still points at the
// same machine port, in which case its published host port has to stay.
func machinePortStillRouted(client *api.Client, r route.Route) (bool, error) {
	routes, err := client.List()
	if err != nil {
		return false, err
	}
	for _, other := range routes {
		if other.SameMachine(r) {
			return true, nil
		}
	}
	return false, nil
}
