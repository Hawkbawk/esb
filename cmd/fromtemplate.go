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
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hawkbawk/esb/internal/docker"
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

An optional .esb.json at the root of the repo (the CWD from-template is
expected to be run from) lists the kits to pass to sbx create. Each entry is
whatever --kit accepts: a local path, a URL, or an OCI reference. The
ordering of kits is important. If one kit depends on another, its
dependencies must be listed before it to ensure the install and setup
scripts run in the correct order. Note that local paths are resolved
relative to the repo root, just like the dockerfile key.

.esb.json can also set "dockerfile" to use a Dockerfile other than the one
directly inside the given directory. It's either a path relative to the repo
root or an absolute path.

.esb.json can also set "setupScript" to a shell script, resolved the same way
as "dockerfile", that's copied into the sandbox and run there once everything
else in from-template has finished.

Example:

{"kits": ["./my-kit", "docker/kit-node:latest"], "dockerfile": "docker/Dockerfile.sandbox", "setupScript": "docker/setup.sh"}

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
			return New(dir, tag, port, name, workspace, agent, agentPrompt, buildArgs, createArgs, secrets, verbose, force).Run()
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

// randomSuffix returns a short random hex string used to disambiguate a
// sandbox name when none was explicitly requested.
func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// FromTemplateCommand holds the CLI arguments for the from-template command
// and executes it.
type FromTemplateCommand struct {
	dir         string
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

	// verboseLog is set up by New and used by run() and its helper methods.
	verboseLog *log.Logger
}

// New builds a fromTemplateCommand from the from-template command's flags
// and configures its verbose logger.
func New(dir, tag, port, name, workspace, agent, agentPrompt string, buildArgs, createArgs, secrets []string, verbose, force bool) *FromTemplateCommand {
	opts := &FromTemplateCommand{
		dir:         dir,
		tag:         tag,
		port:        port,
		name:        name,
		workspace:   workspace,
		agent:       agent,
		agentPrompt: agentPrompt,
		buildArgs:   buildArgs,
		createArgs:  createArgs,
		secrets:     secrets,
		verbose:     verbose,
		force:       force,
	}
	opts.verboseLog = log.New(io.Discard, "verbose: ", 0)
	if opts.verbose {
		opts.verboseLog.SetOutput(os.Stderr)
	}
	return opts
}

// Run builds a Docker Sandbox template image from opts.Dir's Dockerfile,
// loads it, and creates a sandbox from it.
func (opts *FromTemplateCommand) Run() error {
	opts.verboseLog.Printf("dir=%q tag=%q port=%q name=%q workspace=%q agent=%q",
		opts.dir, opts.tag, opts.port, opts.name, opts.workspace, opts.agent)

	if opts.agentPrompt == "" {
		if stat, err := os.Stdin.Stat(); err == nil && stat.Mode()&os.ModeCharDevice == 0 {
			opts.verboseLog.Printf("reading agent prompt from stdin")
			piped, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("reading agent prompt from stdin: %w", err)
			}
			opts.agentPrompt = strings.TrimSpace(string(piped))
		}
	}

	info, err := os.Stat(opts.dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("directory %q not found", opts.dir)
	}

	absDir, err := filepath.Abs(opts.dir)
	if err != nil {
		return err
	}
	parentDir := filepath.Dir(absDir)
	opts.verboseLog.Printf("absDir=%q parentDir=%q", absDir, parentDir)

	proj, err := project.LoadConfig(parentDir)
	if err != nil {
		return err
	}
	opts.verboseLog.Printf("loaded project config: kits=%v dockerfile=%q", proj.Kits, proj.Dockerfile)

	dockerfile, err := opts.resolveDockerfile(proj, parentDir)
	if err != nil {
		return err
	}
	absDockerfile, err := filepath.Abs(dockerfile)
	if err != nil {
		return err
	}

	baseName := opts.tag
	if baseName == "" {
		baseName = filepath.Base(parentDir)
	}
	templateTag := baseName + "-sandbox-template"

	// The image tag is pinned to the commit, so a rebuild after a
	// Dockerfile change is visibly a different image.
	gitSHA, err := project.GitShortSHA(parentDir)
	if err != nil {
		fmt.Printf("%q is not a git repository, skipping gitSHA resolution\n", parentDir)
	}
	opts.verboseLog.Printf("templateTag=%q gitSHA=%q", templateTag, gitSHA)

	sandboxName, err := opts.resolveSandboxName(baseName)
	if err != nil {
		return err
	}
	opts.verboseLog.Printf("absDockerfile=%q sandboxName=%q", absDockerfile, sandboxName)

	if err := opts.buildImage(absDockerfile, parentDir, templateTag, gitSHA); err != nil {
		return err
	}
	if err := opts.loadTemplateImage(templateTag); err != nil {
		return err
	}
	if err := opts.createSandbox(templateTag, sandboxName, proj.Kits); err != nil {
		return err
	}
	if err := opts.routePort(sandboxName); err != nil {
		return err
	}

	return opts.runSetupScript(proj.SetupScript, sandboxName)
}

// routePort routes --port to the new sandbox and exports it as PORT inside
// the sandbox, so whatever runs in there knows which port to listen on. It's
// a no-op when no --port was given.
func (opts *FromTemplateCommand) routePort(sandboxName string) error {
	if opts.port == "" {
		opts.verboseLog.Printf("no --port given, skipping route setup")
		return nil
	}
	sandboxPort, err := strconv.Atoi(opts.port)
	if err != nil {
		return fmt.Errorf("port %q is not a number", opts.port)
	}

	opts.verboseLog.Printf("setting PORT=%d inside sandbox %s", sandboxPort, sandboxName)
	if err := sbx.AddPermanentEnvVar(sandboxName, "PORT", strconv.Itoa(sandboxPort)); err != nil {
		return err
	}

	opts.verboseLog.Printf("routing host %s -> sandbox %s port %d", sandboxName, sandboxName, sandboxPort)
	return RouteHost(sandboxName, sandboxName, sandboxPort)
}

// runSetupScript runs the .esb.json "setupScript" against sandboxName, if
// one was given. The path is resolved the same way as the "dockerfile" key:
// relative to the repo root (the CWD from-template is run from), or absolute.
func (opts *FromTemplateCommand) runSetupScript(scriptPath, sandboxName string) error {
	if scriptPath == "" {
		opts.verboseLog.Printf("no setupScript given, skipping")
		return nil
	}

	fmt.Printf("Running setup script %s in sandbox %q ...\n", scriptPath, sandboxName)
	opts.verboseLog.Printf("+ sbx.RunScript(%s, %s)", sandboxName, scriptPath)
	return sbx.RunScript(sandboxName, scriptPath)
}

// resolveDockerfile finds the Dockerfile to build from: the .esb.json
// "dockerfile" override if set, or a Dockerfile directly inside opts.Dir.
func (opts *FromTemplateCommand) resolveDockerfile(proj *project.Config, parentDir string) (string, error) {
	dockerfile := filepath.Join(opts.dir, "Dockerfile")
	if proj.Dockerfile != "" {
		dockerfile = proj.Dockerfile
	}
	if _, err := os.Stat(dockerfile); err != nil {
		if proj.Dockerfile != "" {
			return "", fmt.Errorf("no Dockerfile at %q (from the %q key in %s)", dockerfile, "dockerfile", filepath.Join(parentDir, project.ConfigName))
		}
		return "", fmt.Errorf("no Dockerfile at %q\nfrom-template expects a Dockerfile directly inside the given directory", dockerfile)
	}
	return dockerfile, nil
}

// resolveSandboxName decides what name to create the sandbox under, given
// the (possibly empty) --name flag and the default derived name.
//
// If opts.Name was explicitly given and is already in use, it destroys the
// existing sandbox (and its routes) when opts.Force is set, or fails
// otherwise. If opts.Name was left to default and the default is already in
// use, it picks a new name instead of touching the existing sandbox.
func (opts *FromTemplateCommand) resolveSandboxName(defaultName string) (string, error) {
	explicitName := opts.name != ""
	sandboxName := opts.name
	if sandboxName == "" {
		sandboxName = defaultName
	}

	exists, err := sbx.Exists(sandboxName)
	if err != nil {
		return "", err
	}
	if !exists {
		return sandboxName, nil
	}

	switch {
	case !explicitName:
		// No --name was given, so there's no reason to fight over the
		// default: just pick a name that isn't taken.
		suffix, err := randomSuffix()
		if err != nil {
			return "", err
		}
		newName := sandboxName + "-" + suffix
		fmt.Printf("sandbox %q already exists; using %q instead\n", sandboxName, newName)
		return newName, nil
	case !opts.force:
		return "", fmt.Errorf("sandbox %q already exists; pass -f to replace it", sandboxName)
	default:
		fmt.Printf("Sandbox %q already exists, removing it ...\n", sandboxName)
		_, client, err := load()
		if err != nil {
			return "", err
		}
		if _, err := client.RemoveSandbox(sandboxName); err != nil {
			return "", err
		}
		if err := sbx.Run("rm", "--force", sandboxName); err != nil {
			return "", err
		}
		return sandboxName, nil
	}
}

// buildImage runs `docker build` against absDockerfile, tagging the result
// as templateTag at both gitSHA and :latest.
func (opts *FromTemplateCommand) buildImage(absDockerfile, parentDir, templateTag, gitSHA string) error {
	for _, arg := range opts.buildArgs {
		if !strings.Contains(arg, "=") {
			return fmt.Errorf("--build-arg %q must be in KEY=VALUE form", arg)
		}
	}

	fmt.Printf("Building %s:%s (and :latest) from %s ...\n", templateTag, gitSHA, absDockerfile)

	buildCmdArgs := []string{"build", "-f", absDockerfile, "-t", templateTag + ":" + gitSHA, "-t", templateTag + ":latest"}
	if opts.verbose {
		buildCmdArgs = append(buildCmdArgs, "--progress", "plain")
	}
	for _, arg := range opts.buildArgs {
		buildCmdArgs = append(buildCmdArgs, "--build-arg", arg)
	}
	buildCmdArgs = append(buildCmdArgs, "--build-arg", "WORKSPACE_DIR="+parentDir)
	for _, secret := range opts.secrets {
		buildCmdArgs = append(buildCmdArgs, "--secret", secret)
	}
	buildCmdArgs = append(buildCmdArgs, parentDir)

	opts.verboseLog.Printf("+ docker %s", strings.Join(buildCmdArgs, " "))
	buildCmd := exec.Command("docker", buildCmdArgs...)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	return nil
}

// loadTemplateImage saves templateTag:latest and loads it into docker
// sandbox, unless docker sandbox already has a template image for
// templateTag whose layers match what was just built.
//
// This looks at what's actually loaded right now rather than remembering
// what a previous run built, so it stays correct across any number of
// consecutive builds that all produce the same layers: each run's image ID
// differs (buildkit stamps a fresh timestamp into the config), but as long
// as one of the loaded images for this template shares those layers, the
// save/load cycle is redundant.
func (opts *FromTemplateCommand) loadTemplateImage(templateTag string) error {
	built, err := docker.InspectImage(templateTag + ":latest")
	if err != nil {
		return err
	}
	opts.verboseLog.Printf("built image ID=%q layers=%d", built.ID, len(built.Layers))

	loadedIdentities, err := sbx.LoadedTemplateIdentities(templateTag)
	if err != nil {
		return err
	}
	opts.verboseLog.Printf("found %d loaded image(s) for template %q", len(loadedIdentities), templateTag)

	var matched string

	i := slices.IndexFunc(loadedIdentities, func(ident docker.ImageIdentity) bool {
		return ident.ID == built.ID || built.SameContent(ident)
	})
	if i != -1 {
		matched = loadedIdentities[i].ID
	}

	if matched != "" {
		fmt.Printf("Template image %s is already loaded into docker sandbox, skipping save/load ...\n", matched)
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "esb-template-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	tarPath := filepath.Join(tmpDir, templateTag+".tar")
	opts.verboseLog.Printf("tarPath=%q", tarPath)

	fmt.Printf("Saving image to %s ...\n", tarPath)
	opts.verboseLog.Printf("+ docker save -o %s %s:latest", tarPath, templateTag)
	if err := docker.SaveImage(templateTag+":latest", tarPath); err != nil {
		return fmt.Errorf("docker save: %w", err)
	}

	fmt.Println("Loading template into docker sandbox ...")
	opts.verboseLog.Printf("+ sbx template load %s", tarPath)
	return sbx.Run("template", "load", tarPath)
}

// createSandbox creates sandboxName from templateTag and kits, then starts
// the agent prompt in the background if one was given.
func (opts *FromTemplateCommand) createSandbox(templateTag, sandboxName string, kits []string) error {
	fmt.Printf("Creating sandbox %q from template %q ...\n", sandboxName, templateTag)
	create := []string{"create", "--clone", "--template", templateTag, "--name", sandboxName}
	for _, kit := range kits {
		create = append(create, "--kit", kit)
	}
	create = append(create, opts.createArgs...)
	create = append(create, opts.agent, opts.workspace)
	opts.verboseLog.Printf("+ sbx %s", strings.Join(create, " "))
	if err := sbx.Run(create...); err != nil {
		return err
	}

	if opts.agentPrompt == "" {
		return nil
	}
	opts.verboseLog.Printf("+ sbx run --name %s -- --bg <agentPrompt>", sandboxName)
	return sbx.Run("run", "--name", sandboxName, "--", "--bg", opts.agentPrompt)
}
