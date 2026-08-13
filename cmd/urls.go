package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/usher/internal/route"
)

func newURLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "urls",
		Aliases: []string{"ls"},
		Short:   "List routed hostnames and where they point",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := load()
			if err != nil {
				return err
			}
			defer client.Close()

			routes, err := client.List()
			if err != nil {
				return err
			}
			if len(routes) == 0 {
				fmt.Println("(nothing routed)")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "URL\tADAPTER\tMACHINE\tUPSTREAM")
			for _, r := range routes {
				machine := r.Machine
				if r.Adapter == route.Static {
					// A static route's "machine" is just the address half of
					// its upstream, so printing it twice says nothing.
					machine = "-"
				}
				fmt.Fprintf(w, "https://%s.%s\t%s\t%s\t%s\n",
					r.Host, cfg.Domain, r.Adapter, machine, r.Upstream)
			}
			return w.Flush()
		},
	}
	return cmd
}
