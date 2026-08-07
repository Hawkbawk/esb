package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rhawk/esb/internal/sbx"
)

func newFromTemplateCmd() *cobra.Command {
	var (
		tag        string
		port       string
		name       string
		workspace  string
		agent      string
		buildArgs  []string
		createArgs []string
		verbose    bool
	)

	cmd := &cobra.Command{
		Use:   "from-template [directory]",
		Short: "Build a sandbox template from a repo's Dockerfile and create a sandbox from it",
		Long: `Build a Docker Sandbox template image straight from a repo's checked-in
Dockerfile, load it, and create a sandbox from it in one shot.

Expects a .docker-sandbox/ layout with a Dockerfile plus optional kit
subfolders, each containing a spec.yaml. The directory defaults to
.docker-sandbox in the current directory.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ".docker-sandbox"
			if len(args) == 1 {
				dir = args[0]
			}

			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("directory %q not found", dir)
			}
			dockerfile := filepath.Join(dir, "Dockerfile")
			if _, err := os.Stat(dockerfile); err != nil {
				return fmt.Errorf("no Dockerfile at %q\nfrom-template expects a Dockerfile directly inside the given directory", dockerfile)
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			parentDir := filepath.Dir(absDir)

			baseName := tag
			if baseName == "" {
				baseName = filepath.Base(parentDir)
			}
			templateTag := baseName + "-sandbox-template"

			// The image tag is pinned to the commit, so a rebuild after a
			// Dockerfile change is visibly a different image.
			gitSHA, err := gitShortSHA(parentDir)
			if err != nil {
				return fmt.Errorf("%q is not a git repository (needed to derive the image tag)", parentDir)
			}

			sandboxName := name
			if sandboxName == "" {
				sandboxName = baseName
			}

			run := func(bin string, argv ...string) error {
				if verbose {
					fmt.Fprintf(os.Stderr, "+ %s %s\n", bin, strings.Join(argv, " "))
				}
				c := exec.Command(bin, argv...)
				c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
				return c.Run()
			}

			fmt.Printf("Building %s:%s (and :latest) from %s ...\n", templateTag, gitSHA, dockerfile)
			build := []string{
				"build", "-f", dockerfile,
				"-t", templateTag + ":" + gitSHA,
				"-t", templateTag + ":latest",
			}
			build = append(build, buildArgs...)
			build = append(build, parentDir)
			if err := run("docker", build...); err != nil {
				return fmt.Errorf("docker build: %w", err)
			}

			tmpDir, err := os.MkdirTemp("", "esb-template-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmpDir)
			tarPath := filepath.Join(tmpDir, templateTag+".tar")

			fmt.Printf("Saving image to %s ...\n", tarPath)
			if err := run("docker", "save", "-o", tarPath, templateTag+":latest"); err != nil {
				return fmt.Errorf("docker save: %w", err)
			}

			fmt.Println("Loading template into docker sandbox ...")
			if err := sbx.Run("template", "load", tarPath); err != nil {
				return err
			}

			kits, err := discoverKits(dir)
			if err != nil {
				return err
			}

			fmt.Printf("Creating sandbox %q from template %q ...\n", sandboxName, templateTag)
			create := []string{"create", "--clone", "--template", templateTag, "--name", sandboxName}
			for _, kit := range kits {
				create = append(create, "--kit", kit)
			}
			create = append(create, createArgs...)
			create = append(create, agent, workspace)
			if err := sbx.Run(create...); err != nil {
				return err
			}

			if port == "" {
				return nil
			}
			sandboxPort, err := strconv.Atoi(port)
			if err != nil {
				return fmt.Errorf("port %q is not a number", port)
			}
			return routeExisting(sandboxName, sandboxPort)
		},
	}

	f := cmd.Flags()
	f.StringVarP(&tag, "tag", "t", "", "base name for the template image (default: the repo directory name)")
	f.StringVarP(&port, "port", "p", "", "route this sandbox port once the sandbox is created")
	f.StringVarP(&name, "name", "n", "", "sandbox name (default: the template base name)")
	f.StringVarP(&workspace, "workspace", "w", ".", "workspace path passed to sbx create")
	f.StringVarP(&agent, "agent", "a", "claude", "agent to start in the sandbox")
	f.StringArrayVarP(&buildArgs, "build-arg", "b", nil, "extra argument for docker build (repeatable)")
	f.StringArrayVarP(&createArgs, "create-arg", "c", nil, "extra argument for sbx create (repeatable)")
	f.BoolVarP(&verbose, "verbose", "v", false, "echo the commands being run")
	return cmd
}

// discoverKits finds every immediate subdirectory holding a spec.yaml.
func discoverKits(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var kits []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		kit := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(kit, "spec.yaml")); err == nil {
			kits = append(kits, kit)
		}
	}
	return kits, nil
}

func gitShortSHA(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// routeExisting mirrors `esb route` for a sandbox this command just created.
func routeExisting(label string, sandboxPort int) error {
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
