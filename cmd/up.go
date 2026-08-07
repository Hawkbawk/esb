package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/rhawk/esb/internal/route"
	"github.com/rhawk/esb/internal/sbx"
)

func newUpCmd() *cobra.Command {
	var agent string

	cmd := &cobra.Command{
		Use:   "up <label> <sandbox-port> <workspace> [-- extra sbx create args...]",
		Short: "Create a sandbox and route it at https://<label>.<domain>",
		Long: `Create a sandbox in --clone mode and route it.

--clone means the agent works on a private in-container clone and its commits
come back out through the sandbox-<label> git remote on the host. Nothing the
agent does can touch your worktree.`,
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, client, err := load()
			if err != nil {
				return err
			}

			raw := args[0]
			sandboxPort, err := strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("sandbox port %q is not a number", args[1])
			}
			workspace := args[2]
			extra := args[3:]

			label := route.Sanitize(raw)
			if label == "" {
				return fmt.Errorf("%q does not sanitise to a usable hostname label", raw)
			}
			if label != raw {
				fmt.Printf("note: using %q as the hostname label (sanitised from %q)\n", label, raw)
			}

			exists, err := sbx.Exists(label)
			if err != nil {
				return err
			}
			if exists {
				return fmt.Errorf("sandbox %q already exists\nRun 'esb down %s' first, or 'esb route %s %d' to just refresh its route",
					label, label, label, sandboxPort)
			}

			// Reserve the route first: the host port has to be known before
			// the sandbox is created, because it goes in --publish.
			rt, err := client.Upsert(label, sandboxPort)
			if err != nil {
				return err
			}

			createArgs := []string{
				"create", "--clone",
				"--name", label,
				"--publish", fmt.Sprintf("127.0.0.1:%d:%d", rt.HostPort, sandboxPort),
			}
			createArgs = append(createArgs, extra...)
			createArgs = append(createArgs, agent, workspace)

			if err := sbx.Run(createArgs...); err != nil {
				// Don't leave a route pointing at a sandbox that never existed.
				_ = client.Remove(label)
				return err
			}

			fmt.Printf("\nsandbox   %s\n", label)
			fmt.Printf("url       https://%s.%s\n", label, cfg.Domain)
			fmt.Printf("port      127.0.0.1:%d -> %d in sandbox\n", rt.HostPort, sandboxPort)
			fmt.Printf("git       git fetch sandbox-%s\n", label)
			fmt.Printf("\nAttach with: sbx run --name %s\n", label)
			return nil
		},
	}

	cmd.Flags().StringVarP(&agent, "agent", "a", "claude", "agent to start in the sandbox")
	return cmd
}
