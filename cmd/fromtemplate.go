package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/esb/internal/project"
	"github.com/hawkbawk/esb/internal/sbx"
)

func newFromTemplateCmd() *cobra.Command {
	var (
		tag         string
		port        string
		name        string
		workspace   string
		agent       string
		agentPrompt string
		buildArgs   []string
		createArgs  []string
		secrets     []string
		verbose     bool
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "from-template [directory]",
		Short: "Build a sandbox template from a repo's Dockerfile and create a sandbox from it",
		Long: `Build a Docker Sandbox template image straight from a repo's checked-in
Dockerfile, load it, and create a sandbox from it in one shot.

Expects a .esb/ layout with a Dockerfile in it. The directory defaults to
.esb in the current directory.

An optional esb.json in that same directory lists the kits to pass to
sbx create. Each entry is whatever --kit accepts: a local path, a URL,
or an OCI reference. The ordering of kits is important. If one kit depends on another,
its dependencies must be listed before it to ensure the install and setup scripts run in
the correct order. Note that local paths are resolved relative to the repo root, not the location
of the esb.json file, just like the dockerfile key.

esb.json can also set "dockerfile" to use a Dockerfile other than the one
directly inside the given directory. It's either a path relative to the repo
root (the CWD from-template is expected to be run from) or an absolute path.

Example:

{"kits": ["./my-kit", "docker/kit-node:latest"], "dockerfile": "docker/Dockerfile.sandbox"}

A WORKSPACE_DIR build argument is always sent to the Docker build process. This can be used
to set the working directory inside the Docker container to match the same workspace location
that Docker Sandbox will use, which is always the absolute path to the repository root on the
host machine.

--secret follows the same convention as docker build --secret: id=<name>,src=<path>
or id=<name>,env=<var>. Secrets are made available to RUN --mount=type=secret
instructions during the build and are never baked into the resulting image layers.

    `,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ".esb"
			if len(args) == 1 {
				dir = args[0]
			}
			return runFromTemplate(FromTemplateOptions{
				Dir:         dir,
				Tag:         tag,
				Port:        port,
				Name:        name,
				Workspace:   workspace,
				Agent:       agent,
				AgentPrompt: agentPrompt,
				BuildArgs:   buildArgs,
				CreateArgs:  createArgs,
				Secrets:     secrets,
				Verbose:     verbose,
				Force:       force,
			})
		},
	}

	f := cmd.Flags()
	f.StringVarP(&tag, "tag", "t", "", "base name for the template image (default: the repo directory name)")
	f.StringVarP(&port, "port", "p", "", "route this sandbox port once the sandbox is created")
	f.StringVarP(&name, "name", "n", "", "sandbox name (default: the template base name)")
	f.StringVarP(&workspace, "workspace", "w", ".", "workspace path passed to sbx create")
	f.StringVarP(&agent, "agent", "a", "claude", "agent to start in the sandbox")
	f.StringVarP(&agentPrompt, "agent-prompt", "P", "", "prompt to pass to the agent (or piped via stdin); if set, runs the agent in the background once the sandbox is created. NOTE: Only works with Claude Code. Use `claude agents` inside the sandbox to reconnect")
	f.StringArrayVarP(&buildArgs, "build-arg", "b", nil, "extra argument for docker build (repeatable)")
	f.StringArrayVarP(&createArgs, "create-arg", "c", nil, "extra argument for sbx create (repeatable)")
	f.StringArrayVar(&secrets, "secret", nil, "secret to expose to the build, same syntax as docker build --secret: id=<name>,src=<path> or id=<name>,env=<var> (repeatable)")
	f.BoolVarP(&verbose, "verbose", "v", false, "enable verbose logging to stderr")
	f.BoolVarP(&force, "force", "f", false, "if a sandbox with the target name already exists, destroy it (and its routes) before creating the new one")
	return cmd
}

// builtImageID returns the short (12-hex-char) image ID docker assigned to
// ref, matching the form docker sandbox reports in `sbx template ls --json`.
func builtImageID(ref string) (string, error) {
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Id}}", ref).Output()
	if err != nil {
		return "", fmt.Errorf("docker image inspect %s: %w", ref, err)
	}
	id := strings.TrimPrefix(strings.TrimSpace(string(out)), "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id, nil
}

// randomSuffix returns a short random hex string used to disambiguate a
// sandbox name when none was explicitly requested.
func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// saveImage writes ref to tarPath, equivalent to `docker save -o tarPath ref`.
func saveImage(ref, tarPath string) error {
	cmd := exec.Command("docker", "save", "-o", tarPath, ref)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// FromTemplateOptions holds the CLI arguments for the from-template command.
type FromTemplateOptions struct {
	Dir         string
	Tag         string
	Port        string
	Name        string
	Workspace   string
	Agent       string
	AgentPrompt string
	BuildArgs   []string
	CreateArgs  []string
	Secrets     []string
	Verbose     bool
	Force       bool
}

// runFromTemplate builds a Docker Sandbox template image from opts.Dir's
// Dockerfile, loads it, and creates a sandbox from it.
func runFromTemplate(opts FromTemplateOptions) error {
	dir, tag, port, name, workspace, agent, agentPrompt := opts.Dir, opts.Tag, opts.Port, opts.Name, opts.Workspace, opts.Agent, opts.AgentPrompt
	buildArgs, createArgs, secrets, verbose := opts.BuildArgs, opts.CreateArgs, opts.Secrets, opts.Verbose

	verboseLog := log.New(io.Discard, "verbose: ", 0)
	if verbose {
		verboseLog.SetOutput(os.Stderr)
	}

	verboseLog.Printf("dir=%q tag=%q port=%q name=%q workspace=%q agent=%q", dir, tag, port, name, workspace, agent)

	if agentPrompt == "" {
		if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			verboseLog.Printf("reading agent prompt from stdin")
			piped, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("reading agent prompt from stdin: %w", err)
			}
			agentPrompt = strings.TrimSpace(string(piped))
		}
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("directory %q not found", dir)
	}

	proj, err := project.LoadConfig(dir)
	if err != nil {
		return err
	}
	verboseLog.Printf("loaded project config: kits=%v dockerfile=%q", proj.Kits, proj.Dockerfile)

	dockerfile := filepath.Join(dir, "Dockerfile")
	if proj.Dockerfile != "" {
		dockerfile = proj.Dockerfile
	}
	if _, err := os.Stat(dockerfile); err != nil {
		if proj.Dockerfile != "" {
			return fmt.Errorf("no Dockerfile at %q (from the %q key in %s)", dockerfile, "dockerfile", filepath.Join(dir, project.ConfigName))
		}
		return fmt.Errorf("no Dockerfile at %q\nfrom-template expects a Dockerfile directly inside the given directory", dockerfile)
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(absDir)
	verboseLog.Printf("absDir=%q parentDir=%q", absDir, parentDir)

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
	verboseLog.Printf("templateTag=%q gitSHA=%q", templateTag, gitSHA)

	explicitName := name != ""
	sandboxName := name
	if sandboxName == "" {
		sandboxName = baseName
	}

	exists, err := sbx.Exists(sandboxName)
	if err != nil {
		return err
	}
	if exists {
		switch {
		case !explicitName:
			// No --name was given, so there's no reason to fight over the
			// default: just pick a name that isn't taken.
			suffix, err := randomSuffix()
			if err != nil {
				return err
			}
			newName := sandboxName + "-" + suffix
			verboseLog.Printf("sandbox %q already exists; using %q instead", sandboxName, newName)
			sandboxName = newName
		case !opts.Force:
			return fmt.Errorf("sandbox %q already exists; pass -f to replace it", sandboxName)
		default:
			fmt.Printf("Sandbox %q already exists, removing it ...\n", sandboxName)
			_, client, err := load()
			if err != nil {
				return err
			}
			if _, err := client.RemoveSandbox(sandboxName); err != nil {
				return err
			}
			if err := sbx.Run("rm", "--force", sandboxName); err != nil {
				return err
			}
		}
	}

	for _, arg := range buildArgs {
		if !strings.Contains(arg, "=") {
			return fmt.Errorf("--build-arg %q must be in KEY=VALUE form", arg)
		}
	}

	absDockerfile, err := filepath.Abs(dockerfile)
	if err != nil {
		return err
	}
	verboseLog.Printf("absDockerfile=%q sandboxName=%q", absDockerfile, sandboxName)

	fmt.Printf("Building %s:%s (and :latest) from %s ...\n", templateTag, gitSHA, dockerfile)

	buildCmdArgs := []string{"build", "-f", absDockerfile, "-t", templateTag + ":" + gitSHA, "-t", templateTag + ":latest"}
	if verbose {
		buildCmdArgs = append(buildCmdArgs, "--progress", "plain")
	}
	for _, arg := range buildArgs {
		buildCmdArgs = append(buildCmdArgs, "--build-arg", arg)
	}
	buildCmdArgs = append(buildCmdArgs, "--build-arg", "WORKSPACE_DIR="+parentDir)
	for _, secret := range secrets {
		buildCmdArgs = append(buildCmdArgs, "--secret", secret)
	}
	buildCmdArgs = append(buildCmdArgs, parentDir)

	verboseLog.Printf("+ docker %s", strings.Join(buildCmdArgs, " "))
	buildCmd := exec.Command("docker", buildCmdArgs...)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	imageID, err := builtImageID(templateTag + ":latest")
	if err != nil {
		return err
	}
	verboseLog.Printf("built image ID=%q", imageID)

	alreadyLoaded, err := sbx.HasTemplateImage(imageID)
	if err != nil {
		return err
	}
	verboseLog.Printf("alreadyLoaded=%v", alreadyLoaded)

	if alreadyLoaded {
		fmt.Printf("Template image %s is already loaded into docker sandbox, skipping save/load ...\n", imageID)
	} else {
		tmpDir, err := os.MkdirTemp("", "esb-template-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		tarPath := filepath.Join(tmpDir, templateTag+".tar")
		verboseLog.Printf("tarPath=%q", tarPath)

		fmt.Printf("Saving image to %s ...\n", tarPath)
		verboseLog.Printf("+ docker save -o %s %s:latest", tarPath, templateTag)
		if err := saveImage(templateTag+":latest", tarPath); err != nil {
			return fmt.Errorf("docker save: %w", err)
		}

		fmt.Println("Loading template into docker sandbox ...")
		verboseLog.Printf("+ sbx template load %s", tarPath)
		if err := sbx.Run("template", "load", tarPath); err != nil {
			return err
		}
	}

	fmt.Printf("Creating sandbox %q from template %q ...\n", sandboxName, templateTag)
	create := []string{"create", "--clone", "--template", templateTag, "--name", sandboxName}
	for _, kit := range proj.Kits {
		create = append(create, "--kit", kit)
	}
	create = append(create, createArgs...)
	create = append(create, agent, workspace)
	verboseLog.Printf("+ sbx %s", strings.Join(create, " "))
	if err := sbx.Run(create...); err != nil {
		return err
	}

	if agentPrompt != "" {
		verboseLog.Printf("+ sbx run --name %s -- --bg <agentPrompt>", sandboxName)
		if err := sbx.Run("run", "--name", sandboxName, "--", "--bg", agentPrompt); err != nil {
			return err
		}
	}

	if port == "" {
		verboseLog.Printf("no --port given, skipping route setup")
		return nil
	}
	sandboxPort, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("port %q is not a number", port)
	}
	verboseLog.Printf("routing host %s -> sandbox %s port %d", sandboxName, sandboxName, sandboxPort)
	return RouteHost(sandboxName, sandboxName, sandboxPort)
}
