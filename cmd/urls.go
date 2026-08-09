package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newURLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "urls",
		Aliases: []string{"ls"},
		Short:   "List routed sandboxes and their URLs",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := load()
			if err != nil {
				return err
			}

			routes, err := client.List()
			if err != nil {
				return err
			}
			if len(routes) == 0 {
				fmt.Println("(no routed sandboxes)")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "HOST\tSANDBOX\tHOST PORT\tSANDBOX PORT\tURL")
			for _, r := range routes {
				fmt.Fprintf(w, "%s\t%s\t%d\t%d\thttps://%s.%s\n",
					r.Host, r.Sandbox, r.HostPort, r.SandboxPort, r.Host, cfg.Domain)
			}
			return w.Flush()
		},
	}
	return cmd
}
