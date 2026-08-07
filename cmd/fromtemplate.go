package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	archive "github.com/moby/go-archive"
	"github.com/spf13/cobra"

	"github.com/hawkbawk/esb/internal/project"
	"github.com/hawkbawk/esb/internal/sbx"
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

Expects a .docker-sandbox/ layout with a Dockerfile in it. The directory
defaults to .docker-sandbox in the current directory.

An optional esb.json in that same directory lists the kits to pass to
sbx create. Each entry is whatever --kit accepts: a local path, a URL,
or an OCI reference.

    {"kits": ["./my-kit", "docker/kit-node:latest"]}`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ".docker-sandbox"
			if len(args) == 1 {
				dir = args[0]
			}
			return runFromTemplate(dir, tag, port, name, workspace, agent, buildArgs, createArgs, verbose)
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

// builtImageID returns the short (12-hex-char) image ID docker assigned to
// ref, matching the form docker sandbox reports in `sbx template ls --json`.
func builtImageID(ctx context.Context, cli *client.Client, ref string) (string, error) {
	inspect, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w", ref, err)
	}
	id := strings.TrimPrefix(inspect.ID, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id, nil
}

// saveImage writes ref to tarPath in the same format as `docker save -o`.
func saveImage(ctx context.Context, cli *client.Client, ref, tarPath string) error {
	rc, err := cli.ImageSave(ctx, []string{ref})
	if err != nil {
		return err
	}
	defer rc.Close()

	f, err := os.Create(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, rc)
	return err
}

// runFromTemplate builds a Docker Sandbox template image from dir's
// Dockerfile, loads it, and creates a sandbox from it.
func runFromTemplate(dir, tag, port, name, workspace, agent string, buildArgs, createArgs []string, verbose bool) error {
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
	gitSHA, err := project.GitShortSHA(parentDir)
	if err != nil {
		return fmt.Errorf("%q is not a git repository (needed to derive the image tag)", parentDir)
	}

	sandboxName := name
	if sandboxName == "" {
		sandboxName = baseName
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("connecting to docker: %w", err)
	}
	defer cli.Close()

	ctx := context.Background()

	dockerfileRel, err := filepath.Rel(parentDir, dockerfile)
	if err != nil {
		return err
	}

	buildArgMap := make(map[string]*string, len(buildArgs))
	for _, arg := range buildArgs {
		k, v, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("--build-arg %q must be in KEY=VALUE form", arg)
		}
		buildArgMap[k] = &v
	}

	fmt.Printf("Building %s:%s (and :latest) from %s ...\n", templateTag, gitSHA, dockerfile)
	if verbose {
		fmt.Fprintf(os.Stderr, "+ docker build -f %s -t %s:%s -t %s:latest %s\n", dockerfile, templateTag, gitSHA, templateTag, parentDir)
	}
	buildCtx, err := archive.TarWithOptions(parentDir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	defer buildCtx.Close()

	buildResp, err := cli.ImageBuild(ctx, buildCtx, build.ImageBuildOptions{
		Dockerfile: dockerfileRel,
		Tags:       []string{templateTag + ":" + gitSHA, templateTag + ":latest"},
		BuildArgs:  buildArgMap,
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	defer buildResp.Body.Close()
	if err := jsonmessage.DisplayJSONMessagesStream(buildResp.Body, os.Stdout, os.Stdout.Fd(), false, nil); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	imageID, err := builtImageID(ctx, cli, templateTag+":latest")
	if err != nil {
		return err
	}

	alreadyLoaded, err := sbx.HasTemplateImage(imageID)
	if err != nil {
		return err
	}

	if alreadyLoaded {
		fmt.Printf("Template image %s is already loaded into docker sandbox, skipping save/load ...\n", imageID)
	} else {
		tmpDir, err := os.MkdirTemp("", "esb-template-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		tarPath := filepath.Join(tmpDir, templateTag+".tar")

		fmt.Printf("Saving image to %s ...\n", tarPath)
		if verbose {
			fmt.Fprintf(os.Stderr, "+ docker save -o %s %s:latest\n", tarPath, templateTag)
		}
		if err := saveImage(ctx, cli, templateTag+":latest", tarPath); err != nil {
			return fmt.Errorf("docker save: %w", err)
		}

		fmt.Println("Loading template into docker sandbox ...")
		if err := sbx.Run("template", "load", tarPath); err != nil {
			return err
		}
	}

	proj, err := project.LoadConfig(dir)
	if err != nil {
		return err
	}

	fmt.Printf("Creating sandbox %q from template %q ...\n", sandboxName, templateTag)
	create := []string{"create", "--clone", "--template", templateTag, "--name", sandboxName}
	for _, kit := range proj.Kits {
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
	return RouteSandbox(sandboxName, sandboxPort)
}
