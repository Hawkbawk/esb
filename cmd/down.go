package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rhawk/esb/internal/route"
	"github.com/rhawk/esb/internal/sbx"
)

func newDownCmd() *cobra.Command {
	var keepSandbox bool

	cmd := &cobra.Command{
		Use:   "down <label>",
		Short: "Remove a sandbox and its route",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, err := load()
			if err != nil {
				return err
			}
			label := route.Sanitize(args[0])

			if err := client.Remove(label); err != nil {
				return err
			}
			if keepSandbox {
				fmt.Printf("removed the route for %q; the sandbox is still running\n", label)
				return nil
			}

			// The sandbox may already be gone, which is fine: the route is
			// what this command really owns.
			if err := sbx.Run("rm", "--force", label); err != nil {
				fmt.Printf("removed the route for %q; sandbox removal reported: %v\n", label, err)
				return nil
			}
			fmt.Printf("removed sandbox %q and its route\n", label)
			return nil
		},
	}

	cmd.Flags().BoolVar(&keepSandbox, "keep-sandbox", false, "remove the route but leave the sandbox running")
	return cmd
}
